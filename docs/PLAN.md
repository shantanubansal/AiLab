# Hosted Agent Platform — v1 Plan

## Context

The repo at `/Users/shantanu/uipath/repos/AiLab` is empty. You want to build a hosted platform where companies and individual developers run agents — competing in the space carved out by Modal, Fly.io, LangGraph Cloud, Replit Agents, and UiPath's own agent tooling. Target audiences for v1: multi-tenant SaaS customers (enterprises) **and** solo developers / OSS self-hosters.

The defining design insight: three of your four agent types — LLM/AI code agents, raw Docker containers, and MCP server hosts — are the **same primitive** with different lifecycles and templates. Build that one primitive well. Defer the visual no-code workflow builder; it's a separate 6+ month product and can layer on later through the same runtime.

## Scope

**In v1:**
- LLM/AI code agents (Python + TypeScript templates, built into containers by platform)
- Arbitrary container workloads (user pushes an OCI image)
- MCP server hosting (long-running containers exposing MCP endpoints, scale-to-zero)
- Multi-tenant orgs with public signup (solo devs) and SSO-capable enterprise tenants
- Helm chart for OSS self-host **and** a managed SaaS instance running from the same chart
- Manual / webhook / cron triggers
- Run history, logs, secrets, minimal Next.js UI

**Deferred (write this down explicitly so it doesn't creep in):**
- Visual no-code workflow builder
- Billing beyond raw usage counters (no Stripe in v1)
- Event-bus / queue / S3 triggers
- Multi-region, custom domains, marketplace
- Firecracker/WASM runtimes
- Teams/RBAC beyond owner+member

## Architecture

**Execution layer: Kubernetes.** One cluster per region. Two CRDs reconciled by a custom controller:
- `AgentRun` → maps to a `Job` (batch agents)
- `AgentDeployment` → maps to `Deployment`+`Service` (MCP servers, long-running)

Tenant isolation: namespace per tenant, `NetworkPolicy` (default-deny), `ResourceQuota`, dedicated `ServiceAccount`. **gVisor (runsc) RuntimeClass** for free/OSS tier; standard runc on dedicated node pools for paid tenants. Don't write your own scheduler.

**Services (all Go, monorepo with Go workspaces):**

| Service | Responsibility |
|---|---|
| `api` | Public REST + gRPC; auth, tenant/agent/run/secret/trigger CRUD; OpenAPI-generated |
| `controller` | Reconciles `AgentRun` and `AgentDeployment` CRDs (controller-runtime) |
| `builder` | Builds source pushes into images via Kaniko; Trivy scan; cosign sign; promote to registry |
| `gateway` | Ingress for MCP server hosting — routes `https://<agent>.<tenant>.run.<domain>` to pods; on-demand TLS via Caddy (MVP) |
| `triggers` | Manual (API), signed webhooks, cron (backed by k8s `CronJob`) |

**Data plane:**
- **Postgres** — tenants, users, agents, runs, secrets refs, triggers; `tenant_id` on every row, enforced by a thin query-layer guard
- **NATS JetStream** — internal event bus (`run.requested`, `run.started`, `run.completed`, `build.completed`); no Kafka
- **S3 / GCS** — run artifacts, cold logs
- **Loki** — hot logs (live tail in UI)
- **Vault** (or cloud secrets manager) — secret material; api stores only references; controller mounts via Secrets Store CSI driver at run time
- **Container registry** — Harbor (self-host) or cloud-native; per-tenant repo paths

**Observability:** Prometheus + Grafana, Loki, Tempo. Every run gets a trace ID propagated to user code via env var (`AGENT_TRACE_ID`).

**Auth: WorkOS.** AuthKit for public signup + social; SSO/Directory Sync for enterprise tenants. WorkOS Organizations map 1:1 to platform tenants. JWTs verified at api edge; `tenant_id` claim drives every authorization check.

## Hybrid hosting model

Same Helm chart deploys to a managed cluster (the SaaS) and to a customer's own cluster (OSS self-host). Differences captured in `values.yaml` profiles: `saas` (multi-tenant, gVisor, WorkOS, hosted gateway) vs `selfhost` (single-tenant defaults, optional gVisor, BYO auth via OIDC, no abuse pipeline). This forces clean separation of platform code from platform policy from day one.

## Repo layout

```
/cmd/{api,controller,builder,gateway,triggers}/main.go
/internal/{auth,tenants,agents,runs,secrets,triggers,abuse}/
/pkg/{sdk-go,manifest,events}/          # public surface
/api/openapi.yaml                       # REST source of truth
/api/proto/*.proto                      # internal gRPC
/deploy/helm/agent-platform/            # chart for SaaS + selfhost
/deploy/terraform/                      # SaaS infra (VPC, RDS, S3, etc.)
/sdks/{python,typescript}/              # generated clients + handwritten ergonomics
/web/                                   # Next.js UI
/templates/{python-llm,ts-llm,mcp-server,container}/
/docs/                                  # public docs site
```

## Trigger and dispatch flow

```
trigger (manual | webhook | cron)
   → api: INSERT runs (status=pending)
   → api: publish run.requested to NATS
   → controller: consume, create AgentRun CRD
   → k8s: schedule Job on tenant namespace + node pool
   → pod: stdin = inputs JSON, stdout = outputs JSON, logs → Loki
   → controller: publish run.completed, update Postgres
   → UI/SDK: subscribe via SSE on api or poll
```

MCP servers follow the same flow but use `AgentDeployment` → `Deployment` and stay up with scale-to-zero (~2s cold start budget).

## v1 build order (the spine first)

The first runnable end-to-end slice is the most important thing on this list. Everything after item 3 hangs off it.

1. Terraform a single dev cluster (EKS or GKE), Postgres (RDS), NATS, registry, Vault. WorkOS tenant set up.
2. `api` skeleton + WorkOS auth middleware + tenant/user/agent CRUD. OpenAPI-generated handlers. Postgres schema with `tenant_id` enforced.
3. **Spine:** `controller` + `AgentRun` CRD. End-to-end test: api creates run row → controller runs `hello-world` container in tenant namespace → logs stream to Loki → api serves logs.
4. `builder`: accept `docker push` to internal registry AND accept Git URL → Kaniko build → Trivy scan → cosign sign → promote.
5. Secrets via Vault + CSI driver. Run inputs/outputs convention (JSON via stdin/stdout). Artifact upload to S3.
6. Triggers: manual, signed webhooks, cron. Python + TypeScript SDKs (thin clients + manifest schema validation).
7. `gateway` + `AgentDeployment` CRD: per-agent subdomain, on-demand TLS, scale-to-zero. This is the MCP hosting path.
8. Next.js UI: login, agent list, create-from-template, run history, log viewer, secrets, trigger config. No visual editor.
9. Templates: `python-llm` (Anthropic SDK preconfigured), `ts-llm`, `mcp-server`, `container`. Quotas, egress firewall, abuse controls. Helm chart polish for OSS self-host. Public signup launch.

Right-size the team against this list later; with 4-6 engineers full-time the plan is ~12 weeks; solo it's 6-9 months even with these cuts.

## Top risks and how v1 de-risks each

1. **Tenant escape on the OSS/free tier.** gVisor RuntimeClass + default-deny NetworkPolicy + ResourceQuota from day 1. Quarterly red-team.
2. **MCP hosting is stateful and lives longer than batch runs.** Separate code path (`AgentDeployment`, not `AgentRun`) and don't promise SLAs in v1.
3. **The builder is a security boundary** (untrusted source → your registry). Kaniko in unprivileged pods, dedicated build namespace, Trivy scan + cosign sign before any image can be referenced by `AgentRun`.
4. **OSS free-tier abuse** (mining, spam, scraping). Egress allowlist (only known LLM/API hosts open outbound), wall-clock + CPU + RAM caps, GitHub-verified signup, abuse pipeline (rate limits + admin kill switch) at launch.
5. **Scope creep into the visual workflow builder.** Treat the "deferred" list above as a hard contract. Every workflow user story in v1 must be expressible as a code-agent template.

## Verification

There are three distinct end-to-end paths to verify before calling v1 done. Each should be scriptable as an integration test plus a manual UI walkthrough.

1. **Code agent path.** Sign up via WorkOS → create org → `agentctl init python-llm` → push to platform → builder builds + scans + signs → run manually with input JSON → see logs streaming in UI → see structured output JSON → verify it ran in the correct tenant namespace with gVisor RuntimeClass.
2. **Container path.** `docker push` an image directly to the platform registry under a tenant repo → create an `Agent` referencing it → trigger via signed webhook → verify run succeeds, logs land in Loki, artifacts in S3.
3. **MCP server path.** Deploy the `mcp-server` template → confirm `https://<agent>.<tenant>.run.<domain>` returns valid TLS → connect with an MCP client → verify scale-to-zero kicks in after idle, cold start under 2s.

Also verify: tenant isolation (a pod in tenant A cannot reach tenant B's pods or DBs — `kubectl exec` + `curl` test); NetworkPolicy denies arbitrary outbound; RBAC blocks cross-tenant API reads; same Helm chart deploys cleanly to a fresh `kind` cluster with the `selfhost` profile.

## Critical files (to create)

- `/api/openapi.yaml` — public REST contract
- `/pkg/manifest/manifest.go` — the `uipath-agent.yaml` schema users author against
- `/cmd/controller/main.go` + `/internal/runs/controller.go` — the spine
- `/deploy/helm/agent-platform/values.yaml` — saas vs selfhost profiles
- `/templates/python-llm/` — the on-ramp most users will hit first
