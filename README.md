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

What's still stubbed: the `gateway` service (MCP hosting / `AgentDeployment`
reconciler is wired but no ingress yet); the Next.js UI. See `docs/PLAN.md`
for the build order.
