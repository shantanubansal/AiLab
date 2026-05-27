# AWS Terraform module

Brings up the AWS infra the AiLab platform needs: VPC, EKS, RDS Postgres,
S3 artifacts bucket. Helm install runs on top — see
[`docs/DEPLOY.md`](../../../docs/DEPLOY.md).

## Quickstart

```bash
cd deploy/terraform/aws
terraform init
TF_VAR_postgres_password='change-me' terraform plan
TF_VAR_postgres_password='change-me' terraform apply
```

After apply, capture outputs you'll need for the Helm install:

```bash
aws eks update-kubeconfig --name "$(terraform output -raw cluster_name)" --region us-east-1
terraform output -raw database_url        # → postgres.url in values
terraform output    artifacts_bucket
```

## Defaults

Tuned for dev / staging cost. For production:

- Set `multi_az = true` on `aws_db_instance.postgres`, bump
  `postgres_instance_class` to `db.m6g.large`+.
- Flip `single_nat_gateway = false` so each AZ has its own NAT.
- Increase node group sizes; consider a separate node group with taints
  for the gateway/MCP-server workloads vs batch runs.
- Add a CMK + restrict the artifacts bucket via bucket policy.

## What's not in scope

Stays out of the module:

- NATS deployment (use the bundled Helm subchart or run NATS-managed)
- Container registry credentials (use ECR or push to GHCR from CI)
- DNS / Route 53 — the gateway's `<agent>.<tenant>.<domain>` wildcard
  CNAME is environment-specific
- Observability stack (Loki/Tempo/Grafana) — add it as a separate Helm
  install or use Amazon Managed Grafana
