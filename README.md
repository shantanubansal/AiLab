# AiLab — Hosted Agent Platform

A platform for running agents in production. Companies and individual developers bring their agent — code, container, or MCP server — and the platform handles isolation, secrets, triggers, logs, and scaling.

> v1 in progress. See [the approved plan](./docs/PLAN.md) for scope and architecture.

## What you can run

| Kind | How you author it |
|---|---|
| **LLM/AI code agents** | `agentctl init python-llm` (or `ts-llm`) → push → platform builds it |
| **Container workloads** | `docker push` an OCI image to the platform registry |
| **MCP servers** | Same as code agents, but the manifest declares `mode: server` — gets a public TLS subdomain |

The visual no-code workflow builder is **not** in v1. Authoring happens via templates and code.

## Architecture (one paragraph)

A Go control plane (`api`, `controller`, `builder`, `gateway`, `triggers`) on top of Kubernetes. Each "run" is a `Job` reconciled from an `AgentRun` CRD; each long-running MCP server is a `Deployment` reconciled from an `AgentDeployment` CRD. Tenants get isolated namespaces with default-deny `NetworkPolicy`, gVisor `RuntimeClass`, and per-tenant secrets via Vault + CSI. Postgres holds metadata; NATS JetStream is the internal event bus; Loki holds hot logs, S3 holds artifacts and cold logs. Auth via WorkOS for both public signup and enterprise SSO. The same Helm chart deploys the managed SaaS and the OSS self-host (`values-saas.yaml` vs `values-selfhost.yaml`).

## Repo layout

```
/cmd/{api,controller,builder,gateway,triggers}/   # service entrypoints
/internal/                                        # service internals (auth, tenants, agents, runs, secrets, triggers, abuse)
/pkg/{manifest,events,sdk-go}/                    # public shared packages
/api/openapi.yaml + /api/proto/                   # REST + gRPC contracts
/deploy/helm/agent-platform/                      # SaaS + selfhost chart
/deploy/terraform/                                # SaaS infra
/sdks/{python,typescript}/                        # client SDKs
/web/                                             # Next.js UI
/templates/                                       # agent starter templates
```

## Local development

You'll need Go 1.25+, Docker, kubectl, and `kind` (for a local cluster).

```bash
make dev-up           # Postgres + NATS via docker-compose
make migrate          # apply migrations/*.sql
make dev-cluster      # create a kind cluster
make crds-apply       # apply AgentRun + AgentDeployment CRDs

# in two separate terminals:
make run-api          # listens on :8080
make run-controller   # against the kind cluster

# in a third terminal:
./scripts/smoketest.sh
```

See `Makefile` for the full target list and `scripts/smoketest.sh` for the
exact API calls the spine smoke test drives.

## Status

End-to-end paths working locally (all verified against a kind cluster):

- **Spine** — `POST /v1/agents` → `POST /v1/agents/{id}/runs` → `AgentRun` CR
  in tenant namespace → `Job` runs → `run.completed` flows back through NATS
  to Postgres. `./scripts/smoketest.sh` drives the full path and reports PASS.
- **WorkOS auth** — `api` selects `WorkOSVerifier` when `WORKOS_JWKS_URL` is
  set (JWKS auto-refresh every 15 min, `org_id` → tenant). Falls back to
  `dev:<tenantId>:<userId>` bearer when unset, for local dev only.
- **Logs** — `GET /v1/runs/{id}/logs` as `text/event-stream`, one line per
  `data:` frame, follows the pod until it exits.
- **Triggers** — webhook (`POST /v1/agents/{id}/webhooks/{name}` with
  `X-AiLab-Signature: sha256=<hex>` HMAC) and cron (in-process scheduler in
  `cmd/triggers` reading from the `triggers` table, refreshes every 30s).
  Webhook secrets are AES-GCM sealed at rest (`API_SECRETS_KEY`).
- **Usage metering** — `usage_events` rows written on every `run.start` /
  `run.end` / `run.seconds` transition. No invoicing in v1; the rows are
  the source of truth when Stripe/Orb wire-up lands in v1.5.
- **Builder** — `cmd/builder` subscribes to `build.requested`, launches a
  Kaniko `Job` in the `ailab-builds` namespace, polls to terminal, writes
  `builds` and pivots the agent's `image`. Trivy + cosign hooks reserved
  but no-op in v1. Needs `BUILDER_REGISTRY` set for the push to succeed.
- **AgentDeployment** — `POST /v1/agents/{id}/deploy` publishes
  `deployment.requested`; controller materializes an `AgentDeployment` CR;
  reconciler projects to `Deployment` + `Service` via
  `controllerutil.CreateOrUpdate` (in-place mutate, so k8s-filled defaults
  survive across reconciles — no ReplicaSet thrash). `DELETE /deploy`
  publishes `deployment.stopped`.
- **Gateway** — `cmd/gateway` parses `Host: <agent>.<tenant>.<domain>`,
  looks up the `AgentDeployment` via k8s, reverse-proxies to the in-cluster
  `Service`. Runs in-cluster on a `ClusterIP` (or `NodePort` for kind) and
  auto-switches to `apiserver/proxy` URLs when run outside the cluster.
  Local-test: `curl -H "Host: <agent>.<tenant>.run.local" http://localhost:8083/`.
- **UI** — Next.js 15 app at `web/`. Pages: `/login` (token entry),
  `/agents` (list + create), `/agents/{id}` (runs + triggers + create),
  `/runs/{id}` (status polling + live SSE log viewer). `make web-install
  && make web-dev` launches it on `:3000`; the api enables CORS for that
  origin in dev.
- **`agentctl` CLI** — `bin/agentctl` ships from `cmd/agentctl/`. Drives
  the API end-to-end: `agents list/get/create/delete`, `runs trigger/get/logs`,
  `triggers create webhook|cron`, `deploy/undeploy`, `builds create`, plus
  `init <template>` to scaffold a new agent from `templates/`. Config via
  `agentctl login` or `AILAB_API` + `AILAB_TOKEN` env.
- **SDKs** — `sdks/python/ailab` (httpx-based, sync, 4 unit tests passing)
  and `sdks/typescript/` (pure fetch, no deps, 4 unit tests passing) ship
  the same surface as `pkg/sdk-go/`. All three power the CLI / UI / external
  consumers; method names match across languages.
- **Builder hardening** — `BUILDER_TRIVY_ENABLED=true` adds a Trivy scan
  Job after Kaniko (`--severity HIGH,CRITICAL --ignore-unfixed --exit-code 1`);
  vuln findings mark the build `blocked`. `BUILDER_COSIGN_ENABLED=true`
  adds a cosign sign Job (key from `BUILDER_COSIGN_SECRET` Secret, optional
  password via `cosign.password` key in the same Secret).
- **Secrets** — `POST/GET/DELETE /v1/secrets` (`agentctl secrets`). Values
  are AES-GCM ciphertext at rest. On run trigger / deploy, api decrypts
  the manifest's named secrets, creates a k8s `Secret` in the tenant
  namespace, and the reconciler `EnvFrom`-mounts it into the pod.
- **Templates** — `python-llm`, `ts-llm`, `mcp-server`, and `container` —
  `agentctl init <template>` scaffolds.
- **CI** — `.github/workflows/ci.yml` runs go build/vet/test, helm lint
  on all three profiles, pytest, node:test, web build, and terraform
  fmt+validate on every PR.
- **Production deploy** — Terraform module (`deploy/terraform/aws`) for
  VPC + EKS + RDS Postgres + S3, plus a release workflow that builds and
  pushes per-service images + the Helm chart to GHCR on SemVer tags.
  Walkthrough in `docs/DEPLOY.md`.
- **Audit log** — `audit_events` append-only table; every state-changing
  write goes through `audit.Log()` (agent create/delete, secret upsert/
  delete, run trigger, deploy/undeploy, build create, trigger create,
  webhook invoke). `GET /v1/audit` returns recent events for the tenant.
- **Loki read-path** — `GET /v1/runs/{id}/logs` falls back to a LogQL
  `query_range` once the pod has been GC'd. Set `LOKI_URL` to enable.
- **OTEL tracing** — api, controller, and builder all initialize a
  TracerProvider via `internal/telemetry`. OTLP gRPC export when
  `OTEL_EXPORTER_OTLP_ENDPOINT` is set (e.g. `tempo.observability:4317`),
  no-op otherwise. The api wraps chi with `otelhttp.NewMiddleware` and
  the run trigger handler emits an explicit `run.trigger` span; the
  span's trace id fills `AGENT_TRACE_ID` so traces in Tempo align with
  the agent's own logs.
- **Billing** — `cmd/billing` ships `usage_events` rows to Orb's ingest
  endpoint in batches, advancing a per-destination checkpoint
  (`usage_shipper_state`) only after successful send. Configure with
  `ORB_API_KEY` (and optional `ORB_API_URL`). Stripe is the same
  Destination interface — wire when needed.
- **Janitor** — `cmd/janitor` periodically GC's completed k8s Jobs older
  than `JANITOR_JOB_TTL`, reaps orphan run-Secrets, fails builds stuck
  pending/running past `JANITOR_BUILD_STUCK_TTL`, and prunes
  `usage_events` older than `JANITOR_USAGE_TTL` once they've been
  shipped past every destination's checkpoint.

See `docs/PLAN.md` for the v1 build order and what's still deferred.
