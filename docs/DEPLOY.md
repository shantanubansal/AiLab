# Deploying AiLab

End-to-end walkthrough from a clean AWS account to a running platform
serving real agent traffic. Targets a small staging environment by
default; the [production hardening](#production-hardening) section
covers what to change before you start charging customers.

## Prerequisites

- AWS account with admin IAM
- `terraform` ≥ 1.6, `kubectl` ≥ 1.31, `helm` ≥ 3.16, `aws` CLI
- A GitHub repo with the platform code; tags trigger image + chart
  publish to GHCR via `.github/workflows/release.yml`
- A WorkOS tenant (or any OIDC issuer) — required for non-dev auth

## 1. Cut a release

Tag a SemVer commit; CI builds five service images and pushes the Helm
chart to GHCR:

```bash
git tag v0.2.0
git push origin v0.2.0
# Wait for release workflow to finish, then:
#   ghcr.io/<owner>/ailab-{api,controller,builder,gateway,triggers}:v0.2.0
#   oci://ghcr.io/<owner>/charts/agent-platform:0.2.0
```

## 2. Provision AWS infra

```bash
cd deploy/terraform/aws
terraform init
export TF_VAR_postgres_password=$(openssl rand -base64 32)
terraform apply

aws eks update-kubeconfig \
  --name "$(terraform output -raw cluster_name)" --region us-east-1
```

Capture for the next step:

```bash
DB_URL=$(terraform output -raw database_url)
BUCKET=$(terraform output -raw artifacts_bucket)
```

## 3. Bootstrap shared dependencies

```bash
# NATS JetStream (Bitnami chart is fine for v1; replace with NATS-managed
# or your own ops chart in production)
helm repo add nats https://nats-io.github.io/k8s/helm/charts/
helm upgrade --install nats nats/nats --set config.jetstream.enabled=true

# Apply CRDs once (chart-rendered CRDs are skipped on helm upgrade)
kubectl apply -f https://raw.githubusercontent.com/<owner>/AiLab/v0.2.0/deploy/helm/agent-platform/templates/crd-agentrun.yaml
kubectl apply -f https://raw.githubusercontent.com/<owner>/AiLab/v0.2.0/deploy/helm/agent-platform/templates/crd-agentdeployment.yaml

# Run SQL migrations against the new RDS instance
psql "$DB_URL" -f migrations/001_init.sql
psql "$DB_URL" -f migrations/002_triggers_usage_builds.sql
psql "$DB_URL" -f migrations/003_webhook_secret_rename.sql
psql "$DB_URL" -f migrations/004_secrets.sql
```

## 4. Install the platform

```bash
kubectl create namespace ailab
kubectl -n ailab create secret generic platform-secrets \
  --from-literal=postgres-url="$DB_URL" \
  --from-literal=api-secrets-key="$(openssl rand -hex 32)"

helm install ailab oci://ghcr.io/<owner>/charts/agent-platform \
  --version 0.2.0 \
  --namespace ailab \
  -f deploy/helm/agent-platform/values-saas.yaml \
  --set image.registry=ghcr.io/<owner> \
  --set image.tag=v0.2.0 \
  --set postgres.url="$DB_URL" \
  --set auth.workos.clientId=<your-workos-client-id> \
  --set auth.workos.jwksUrl=<your-workos-jwks-url>
```

## 5. Wire DNS

Point a wildcard at the gateway's external load balancer:

```bash
GW_HOST=$(kubectl -n ailab get svc ailab-agent-platform-gateway -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')

# Route 53 (replace with your domain + hosted zone)
aws route53 change-resource-record-sets --hosted-zone-id Z... --change-batch '{
  "Changes": [{
    "Action": "UPSERT",
    "ResourceRecordSet": {
      "Name": "*.run.example.com",
      "Type": "CNAME",
      "TTL": 60,
      "ResourceRecords": [{"Value": "'"$GW_HOST"'"}]
    }
  }]
}'
```

Set `gateway.domain=run.example.com` in your values overlay so the
gateway accepts that host suffix.

## 6. Verify

```bash
kubectl -n ailab get pods                          # api, controller, builder, gateway, triggers all Ready
kubectl -n ailab logs deploy/ailab-agent-platform-api --tail=20

# Drive a manual run from the agentctl CLI:
export AILAB_API=https://api.run.example.com       # whatever you put behind the api Service
export AILAB_TOKEN=<workos-issued-jwt>
agentctl agents create --name smoke --image hello-world
agentctl runs trigger <agentId>
agentctl runs logs <runId>
```

## Production hardening

Before customer traffic hits this, change at minimum:

- Terraform: `multi_az = true` on RDS, dedicated NAT per AZ, larger node
  group, separate node pool with taints for the build namespace (Kaniko
  is privileged-ish), CMK for the artifacts bucket
- Helm: `tenancy.multiTenant=true`, `tenancy.runtimeClassName=gvisor`,
  `tenancy.defaultDenyEgress=true`, `abuse.enabled=true` with an
  outbound allowlist of LLM endpoints
- Auth: real WorkOS Organizations mapped to tenants; rotate the
  `API_SECRETS_KEY` via a sealed-secret / external secrets operator
- Observability: install kube-prometheus-stack, point the api at Loki
  + Tempo, set up the gateway's latency dashboard as oncall's home page
- Backups: enable RDS automated snapshots cross-region, snapshot S3
  artifacts to a vault account

## Self-host (single-tenant)

Same chart, different profile:

```bash
helm install ailab oci://ghcr.io/<owner>/charts/agent-platform \
  --version 0.2.0 \
  -f deploy/helm/agent-platform/values-selfhost.yaml \
  --set image.registry=ghcr.io/<owner> \
  --set image.tag=v0.2.0 \
  --set postgres.url=postgres://...                # BYO database
  --set auth.oidc.issuerUrl=https://login.example.com/
```
