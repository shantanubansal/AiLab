// AWS infra for the AiLab platform: VPC + EKS + RDS Postgres + S3.
//
// Opinionated and minimal. Use this as a starting point, not a fortress.
// Defaults skew toward dev/staging cost; flip `multi_az`, instance sizes,
// and node counts up for production. Helm install runs after this brings
// up the cluster — see docs/DEPLOY.md.

provider "aws" {
  region = var.region

  default_tags {
    tags = merge(var.tags, {
      "ailab.uipath.com/managed-by" = "terraform"
      "ailab.uipath.com/stack"      = var.name
    })
  }
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 3)
}

// ---- Network ----

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.13.0"

  name = "${var.name}-vpc"
  cidr = var.vpc_cidr

  azs             = local.azs
  public_subnets  = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 4, i)]
  private_subnets = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 4, i + 8)]

  enable_nat_gateway   = true
  single_nat_gateway   = true // dev default; flip for prod
  enable_dns_hostnames = true

  public_subnet_tags = {
    "kubernetes.io/role/elb" = 1
  }
  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = 1
  }
}

// ---- EKS ----

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.24.0"

  cluster_name    = "${var.name}-eks"
  cluster_version = var.kubernetes_version

  cluster_endpoint_public_access = true

  enable_cluster_creator_admin_permissions = true

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  cluster_addons = {
    coredns                = { most_recent = true }
    kube-proxy             = { most_recent = true }
    vpc-cni                = { most_recent = true }
    eks-pod-identity-agent = { most_recent = true }
  }

  eks_managed_node_groups = {
    workers = {
      instance_types = var.node_instance_types
      min_size       = var.node_min_size
      max_size       = var.node_max_size
      desired_size   = var.node_desired_size
    }
  }
}

// ---- RDS Postgres ----

resource "aws_security_group" "rds" {
  name        = "${var.name}-rds"
  description = "Postgres access from inside the VPC."
  vpc_id      = module.vpc.vpc_id

  ingress {
    description = "Postgres from VPC CIDR"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [module.vpc.vpc_cidr_block]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_subnet_group" "rds" {
  name       = "${var.name}-rds"
  subnet_ids = module.vpc.private_subnets
}

resource "aws_db_instance" "postgres" {
  identifier              = "${var.name}-postgres"
  engine                  = "postgres"
  engine_version          = "16"
  instance_class          = var.postgres_instance_class
  allocated_storage       = 50
  max_allocated_storage   = 200
  db_name                 = var.postgres_db_name
  username                = var.postgres_username
  password                = var.postgres_password
  db_subnet_group_name    = aws_db_subnet_group.rds.name
  vpc_security_group_ids  = [aws_security_group.rds.id]
  skip_final_snapshot     = true
  backup_retention_period = 7
  storage_encrypted       = true
  apply_immediately       = true
  // Flip these for prod:
  multi_az            = false
  deletion_protection = false
  publicly_accessible = false
}

// ---- S3 (artifacts + cold logs) ----

resource "random_id" "bucket_suffix" {
  byte_length = 4
}

resource "aws_s3_bucket" "artifacts" {
  bucket = "${var.name}-artifacts-${random_id.bucket_suffix.hex}"
}

resource "aws_s3_bucket_versioning" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
