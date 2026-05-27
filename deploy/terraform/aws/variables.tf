variable "region" {
  description = "AWS region."
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Cluster + resource prefix. Keep short; consumed by IAM names that have a 64-char limit."
  type        = string
  default     = "ailab"
}

variable "vpc_cidr" {
  description = "Top-level VPC CIDR. /16 keeps room for three AZs with /20 subnets."
  type        = string
  default     = "10.20.0.0/16"
}

variable "kubernetes_version" {
  description = "EKS minor version. Bump deliberately, in lockstep with controller-runtime."
  type        = string
  default     = "1.31"
}

variable "node_instance_types" {
  description = "EC2 instance types for the managed node group."
  type        = list(string)
  default     = ["t3.medium"]
}

variable "node_min_size" {
  description = "Minimum node count in the managed node group."
  type        = number
  default     = 2
}

variable "node_max_size" {
  description = "Maximum node count in the managed node group."
  type        = number
  default     = 6
}

variable "node_desired_size" {
  description = "Desired node count at create time."
  type        = number
  default     = 3
}

variable "postgres_instance_class" {
  description = "RDS instance class. Single-AZ db.t3.medium is fine for dev; bump to db.m6g.large + multi-AZ for prod."
  type        = string
  default     = "db.t3.medium"
}

variable "postgres_db_name" {
  description = "Database name created on the RDS instance."
  type        = string
  default     = "ailab"
}

variable "postgres_username" {
  description = "RDS master username."
  type        = string
  default     = "ailab"
}

variable "postgres_password" {
  description = "RDS master password. Inject via TF_VAR_postgres_password or your secrets backend."
  type        = string
  sensitive   = true
}

variable "tags" {
  description = "Extra tags applied to all resources."
  type        = map(string)
  default     = {}
}
