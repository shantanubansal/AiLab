output "cluster_name" {
  description = "EKS cluster name. Use with: aws eks update-kubeconfig --name <value>."
  value       = module.eks.cluster_name
}

output "cluster_endpoint" {
  description = "EKS API server endpoint."
  value       = module.eks.cluster_endpoint
}

output "cluster_certificate_authority" {
  description = "Base64-encoded EKS CA cert."
  value       = module.eks.cluster_certificate_authority_data
  sensitive   = true
}

output "postgres_address" {
  description = "RDS endpoint hostname."
  value       = aws_db_instance.postgres.address
}

output "postgres_port" {
  description = "RDS port."
  value       = aws_db_instance.postgres.port
}

output "database_url" {
  description = "Postgres connection string. Feed to the Helm chart's postgres.url value."
  value       = "postgres://${var.postgres_username}:${var.postgres_password}@${aws_db_instance.postgres.address}:${aws_db_instance.postgres.port}/${var.postgres_db_name}?sslmode=require"
  sensitive   = true
}

output "artifacts_bucket" {
  description = "S3 bucket for run artifacts and cold logs."
  value       = aws_s3_bucket.artifacts.bucket
}

output "vpc_id" {
  description = "VPC ID."
  value       = module.vpc.vpc_id
}
