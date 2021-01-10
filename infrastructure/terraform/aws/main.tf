provider "aws" {
  region = var.region
}

locals {
  cluster_name                        = "test-eks-w7b6"
  k8s_service_account_namespace       = "kube-system"
  k8s_dashboard_service_account_name  = "k8s-dashboard-svc-controller"
  k8s_autoscaler_service_account_name = "cluster-autoscaler-aws-cluster-autoscaler-chart"
  k8s_alb_service_account_name        = "aws-load-balancer-controller"
  k8s_external_dns_account_name       = "external-dns"
  k8s_dashboard_namespace             = "kubernetes-dashboard"
  k8s_w7b6_namespace                  = "webhook-broker"
  k8s_metrics_namespace               = "metrics"
  es_domain                           = "test-es-w7b6"
  vpc_cidr_block                      = "20.10.0.0/16"
  vpn_cidr_block                      = "17.10.0.0/16"
}

# VPC and Client VPN

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "2.64.0"

  name = "webhook-broker-vpc"

  cidr = local.vpc_cidr_block # 10.0.0.0/8 is reserved for EC2-Classic

  azs                 = var.azs
  private_subnets     = ["20.10.1.0/24", "20.10.2.0/24", "20.10.3.0/24"]
  public_subnets      = ["20.10.11.0/24", "20.10.12.0/24", "20.10.13.0/24"]
  database_subnets    = ["20.10.21.0/24", "20.10.22.0/24", "20.10.23.0/24"]

  private_subnet_tags = {"kubernetes.io/role/internal-elb":"1"}
  public_subnet_tags  = {"kubernetes.io/role/elb":"1"}

  create_database_subnet_group = true

  enable_dns_hostnames = true
  enable_dns_support   = true

  enable_classiclink             = true
  enable_classiclink_dns_support = true

  enable_nat_gateway = true
  single_nat_gateway = true

  enable_vpn_gateway = true

  enable_dhcp_options            = true
  dhcp_options_domain_name       = "ec2.internal"

  # Default security group - ingress/egress rules cleared to deny all
  manage_default_security_group  = true
  default_security_group_ingress = [{}]
  default_security_group_egress  = [{from_port = 0, to_port = 0, protocol = "-1", cidr_blocks = "0.0.0.0/0"}]

  tags = {
    Owner       = "user"
    Environment = "staging"
    Name        = "webhook-broker"
  }

  vpc_endpoint_tags = {
    Project  = "Secret"
    Endpoint = "true"
  }
}

module "client_vpn" {
  source = "./modules/client-vpn/"

  vpc_id              = module.vpc.vpc_id
  vpn_cidr            = local.vpn_cidr_block
  private_subnets     = [module.vpc.private_subnets[0], module.vpc.private_subnets[1]]
  vpn_server_cert_arn = var.vpn_server_cert_arn
  vpn_client_cert_arn = var.vpn_client_cert_arn
  region              = var.region
}

# Elasticsearch for log ingestion

resource "aws_security_group" "es" {
  count       = var.create_es ? 1 : 0
  name        = "elasticsearch-${local.es_domain}"
  description = "Managed by Terraform"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port = 443
    to_port   = 443
    protocol  = "tcp"

    cidr_blocks = [
      local.vpc_cidr_block, local.vpn_cidr_block
    ]
  }
}

resource "aws_iam_service_linked_role" "es" {
  count            = var.create_es ? 1 : 0
  aws_service_name = "es.amazonaws.com"
}

data "aws_caller_identity" "current" {}

resource "aws_elasticsearch_domain" "test_w7b6" {
  count                 = var.create_es ? 1 : 0
  domain_name           = local.es_domain
  elasticsearch_version = "7.9"
  cluster_config {
    instance_type          = "t2.medium.elasticsearch"
    instance_count         = 3
    zone_awareness_enabled = true
    zone_awareness_config {
      availability_zone_count = 3
    }
  }
  ebs_options {
    ebs_enabled         = true
    volume_size         = 35
  }
  vpc_options {
    subnet_ids          = module.vpc.private_subnets
    security_group_ids  = [aws_security_group.es[0].id]
  }
  domain_endpoint_options {
    enforce_https       = false
    tls_security_policy = "Policy-Min-TLS-1-2-2019-07"
  }

  access_policies       = <<CONFIG
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Action": "es:*",
            "Principal": "*",
            "Effect": "Allow",
            "Resource": "arn:aws:es:${var.region}:${data.aws_caller_identity.current.account_id}:domain/${local.es_domain}/*"
        }
    ]
}
CONFIG

  tags = {
    Domain = "test-w7b6"
  }
  depends_on = [aws_iam_service_linked_role.es]
}

# EKS

data "aws_availability_zones" "available" {
}

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "13.2.1"
  cluster_name    = local.cluster_name
  cluster_version = "1.18"
  subnets         = module.vpc.public_subnets
  vpc_id          = module.vpc.vpc_id
  enable_irsa     = true

  worker_groups = [
    {
      name                 = "worker-group-1"
      asg_desired_capacity = "1"
      asg_min_size         = "1"
      asg_max_size         = "3"
      instance_type        = "c5.large"
      ami_id               = "ami-0e609024e4dbce4a5"
      tags = [
        {
          "key"                 = "k8s.io/cluster-autoscaler/enabled"
          "propagate_at_launch" = "false"
          "value"               = "true"
        },
        {
          "key"                 = "k8s.io/cluster-autoscaler/${local.cluster_name}"
          "propagate_at_launch" = "false"
          "value"               = "true"
        }
      ]
    },
    {
      name                 = "worker-spot-group-1"
      asg_desired_capacity = "2"
      asg_max_size         = "100"
      kubelet_extra_args   = "--node-labels=node.kubernetes.io/lifecycle=spot"
      instance_type        = "c5.large"
      ami_id               = "ami-0e609024e4dbce4a5"
      spot_instance_pools  = 2
      spot_allocation_strategy      = "lowest-price" # Valid options are 'lowest-price' and 'capacity-optimized'. If 'lowest-price', the Auto Scaling group launches instances using the Spot pools with the lowest price, and evenly allocates your instances across the number of Spot pools. If 'capacity-optimized', the Auto Scaling group launches instances using Spot pools that are optimally chosen based on the available Spot capacity.
      spot_price                    = "0.068"
      tags = [
        {
          "key"                 = "k8s.io/cluster-autoscaler/enabled"
          "propagate_at_launch" = "false"
          "value"               = "true"
        },
        {
          "key"                 = "k8s.io/cluster-autoscaler/${local.cluster_name}"
          "propagate_at_launch" = "false"
          "value"               = "true"
        }
      ]
    }
  ]
}

# Kubernetes and Helm Setup

data "aws_eks_cluster" "cluster" {
  name = module.eks.cluster_id
}

data "aws_eks_cluster_auth" "cluster" {
  name = module.eks.cluster_id
}

provider "kubernetes" {
  host                   = data.aws_eks_cluster.cluster.endpoint
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.cluster.certificate_authority.0.data)
  token                  = data.aws_eks_cluster_auth.cluster.token
  load_config_file       = false
}

provider "helm" {
  kubernetes {
    host                   = data.aws_eks_cluster.cluster.endpoint
    cluster_ca_certificate = base64decode(data.aws_eks_cluster.cluster.certificate_authority.0.data)
    token                  = data.aws_eks_cluster_auth.cluster.token
  }
}

# Cluster Auto Scaler

data "aws_iam_policy_document" "cluster_autoscaler" {
  statement {
    sid    = "clusterAutoscalerAll"
    effect = "Allow"

    actions = [
      "autoscaling:DescribeAutoScalingGroups",
      "autoscaling:DescribeAutoScalingInstances",
      "autoscaling:DescribeLaunchConfigurations",
      "autoscaling:DescribeTags",
      "ec2:DescribeLaunchTemplateVersions",
    ]

    resources = ["*"]
  }

  statement {
    sid    = "clusterAutoscalerOwn"
    effect = "Allow"

    actions = [
      "autoscaling:SetDesiredCapacity",
      "autoscaling:TerminateInstanceInAutoScalingGroup",
      "autoscaling:UpdateAutoScalingGroup",
    ]

    resources = ["*"]

    condition {
      test     = "StringEquals"
      variable = "autoscaling:ResourceTag/kubernetes.io/cluster/${module.eks.cluster_id}"
      values   = ["owned"]
    }

    condition {
      test     = "StringEquals"
      variable = "autoscaling:ResourceTag/k8s.io/cluster-autoscaler/enabled"
      values   = ["true"]
    }
  }
}

resource "aws_iam_policy" "cluster_autoscaler" {
  name_prefix = "cluster-autoscaler"
  description = "EKS cluster-autoscaler policy for cluster ${module.eks.cluster_id}"
  policy      = data.aws_iam_policy_document.cluster_autoscaler.json
}

module "iam_assumable_role_admin" {
  source                        = "terraform-aws-modules/iam/aws//modules/iam-assumable-role-with-oidc"
  version                       = "3.6.0"
  create_role                   = true
  role_name                     = "cluster-autoscaler"
  provider_url                  = replace(module.eks.cluster_oidc_issuer_url, "https://", "")
  role_policy_arns              = [aws_iam_policy.cluster_autoscaler.arn]
  oidc_fully_qualified_subjects = ["system:serviceaccount:${local.k8s_service_account_namespace}:${local.k8s_autoscaler_service_account_name}"]
}

# The following configuration are to represent - https://raw.githubusercontent.com/hashicorp/learn-terraform-provision-eks-cluster/master/kubernetes-dashboard-admin.rbac.yaml
# From - https://learn.hashicorp.com/tutorials/terraform/eks
resource "kubernetes_cluster_role_binding" "cluster-admin-binding" {
  metadata {
    name = "cluster-admin-bindings"
  }
  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "ClusterRole"
    name      = "cluster-admin"
  }
  subject {
    kind      = "ServiceAccount"
    name      = local.k8s_dashboard_service_account_name
    namespace = local.k8s_dashboard_namespace
  }
}

resource "helm_release" "aws-spot-termination-handler" {
  name       = "aws-node-termination-handler"
  namespace  = local.k8s_service_account_namespace

  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-node-termination-handler"
}

resource "helm_release" "cluster-autoscaler" {
  name       = "cluster-autoscaler"
  namespace  = local.k8s_service_account_namespace

  repository = "https://kubernetes.github.io/autoscaler"
  chart      = "cluster-autoscaler-chart"

  depends_on = [module.iam_assumable_role_admin]

  values = [
    templatefile("conf/cluster-autoscaler-chart-values.yml", {role_arn = module.iam_assumable_role_admin.this_iam_role_arn})
  ]
}

# Kubernetes Dashboard

resource "kubernetes_namespace" "k8s-dashboard-namespace" {
  metadata {
    name = local.k8s_dashboard_namespace
  }
}

resource "helm_release" "kubernetes-dashboard" {
  name       = "kubernetes-dashboard"
  namespace  = local.k8s_dashboard_namespace

  repository = "https://kubernetes.github.io/dashboard/"
  chart      = "kubernetes-dashboard"
  depends_on = [kubernetes_namespace.k8s-dashboard-namespace]

  set {
      name = "serviceAccount.name"
      value = local.k8s_dashboard_service_account_name
  }
}

# Metrics Server required for HPA

# TODO: This chart has been deprecated, we will need to move to the new chart once official
# https://github.com/kubernetes-sigs/metrics-server/issues/572
resource "helm_release" "metrics-server" {
  name       = "metrics-server"
  namespace  = local.k8s_service_account_namespace

  repository = "https://charts.helm.sh/stable"
  chart      = "metrics-server"
  version    = "2.11.4"

  depends_on = [module.eks]

  set {
      name = "image.repository"
      value = "k8s.gcr.io/metrics-server/metrics-server"
  }

  set {
      name = "image.tag"
      value = "v0.4.1"
  }
}

# AWS ALB Ingression Controller

# This file is from - view-source:https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/main/docs/install/iam_policy.json
# Following the documentation in https://github.com/aws/eks-charts/tree/master/stable/aws-load-balancer-controller
resource "aws_iam_policy" "alb_ingress_controller" {
  name_prefix = "alb-ingress"
  description = "EKS ALB Ingress policy for cluster ${module.eks.cluster_id}"
  policy      = file("conf/aws-alb-ingress-policy.json")
}

module "iam_assumable_role_ingress" {
  source                        = "terraform-aws-modules/iam/aws//modules/iam-assumable-role-with-oidc"
  version                       = "3.6.0"
  create_role                   = true
  role_name                     = "alb-ingress"
  provider_url                  = replace(module.eks.cluster_oidc_issuer_url, "https://", "")
  role_policy_arns              = [aws_iam_policy.alb_ingress_controller.arn]
  oidc_fully_qualified_subjects = ["system:serviceaccount:${local.k8s_service_account_namespace}:${local.k8s_alb_service_account_name}"]
}

resource "helm_release" "alb-ingress-controller" {
  name       = "aws-load-balancer-controller"
  namespace  = local.k8s_service_account_namespace

  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"

  depends_on = [module.iam_assumable_role_ingress]

  values = [templatefile("conf/alb-ingress-chart-values.yml", {role_arn = module.iam_assumable_role_ingress.this_iam_role_arn, svc_acc_name = local.k8s_alb_service_account_name, cluster_name = local.cluster_name, region = var.region, vpc_id = module.vpc.vpc_id})]
}

# External DNS

resource "aws_iam_policy" "external_dns" {
  name_prefix = "external-dns"
  description = "External DNS policy for cluster ${module.eks.cluster_id}"
  policy      = file("conf/external-dns-policy.json")
}

module "iam_assumable_role_external_dns" {
  source                        = "terraform-aws-modules/iam/aws//modules/iam-assumable-role-with-oidc"
  version                       = "3.6.0"
  create_role                   = true
  role_name                     = "external-dns"
  provider_url                  = replace(module.eks.cluster_oidc_issuer_url, "https://", "")
  role_policy_arns              = [aws_iam_policy.external_dns.arn]
  oidc_fully_qualified_subjects = ["system:serviceaccount:${local.k8s_service_account_namespace}:${local.k8s_external_dns_account_name}"]
}

resource "helm_release" "external_dns" {
  name       = "external-dns"
  namespace  = local.k8s_service_account_namespace

  repository = "https://charts.bitnami.com/bitnami"
  chart      = "external-dns"

  depends_on = [module.iam_assumable_role_external_dns]

  values = [templatefile("conf/external-dns-chart-values.yml", {role_arn = module.iam_assumable_role_external_dns.this_iam_role_arn, svc_acc_name = local.k8s_external_dns_account_name, region = var.region})]
}


# Webhook Broker

# RDS

module "sg_mysql" {
  source  = "terraform-aws-modules/security-group/aws//modules/mysql"
  version = "3.17.0"
  name    = "security-group-mysql-${module.vpc.vpc_id}"
  vpc_id  = module.vpc.vpc_id

  create  = var.create_rds

  ingress_cidr_blocks = [
    local.vpc_cidr_block, local.vpn_cidr_block
  ]
}

module "rds" {
  source               = "terraform-aws-modules/rds/aws"
  version              = "2.20.0"

  create_db_instance   = var.create_rds

  identifier        = "w7b6"
  engine            = "mysql"
  engine_version    = "8.0.21"
  instance_class    = "db.r5.large"
  allocated_storage = 5
  storage_encrypted = false

  name     = "webhook_broker"
  username = "webhook_broker"
  password = var.db_password
  port     = "3306"

  vpc_security_group_ids = [module.vpc.default_security_group_id, module.sg_mysql.this_security_group_id]

  maintenance_window = "Sun:00:00-Sun:03:00"
  backup_window      = "04:00-07:00"

  multi_az = true

  # disable backups to create DB faster
  backup_retention_period = 10

  tags = {
    Owner       = "user"
    Environment = "dev"
  }

  enabled_cloudwatch_logs_exports = ["error", "slowquery"]

  # DB subnet group
  subnet_ids = module.vpc.database_subnets

  # DB parameter group
  family = "mysql8.0"

  # DB option group
  major_engine_version = "8.0"

  # Snapshot name upon DB deletion
  final_snapshot_identifier = "w7b6snap"

  # Database Deletion Protection
  deletion_protection = false

  parameters = [
    {
      name  = "character_set_client"
      value = "utf8"
    },
    {
      name  = "character_set_server"
      value = "utf8"
    }
  ]

}

resource "kubernetes_namespace" "webhook_broker_namespace" {
  metadata {
    name = local.k8s_w7b6_namespace
  }
}


resource "helm_release" "webhook-broker" {
  name       = "webhook-broker"
  namespace  = local.k8s_w7b6_namespace

  repository = "https://helm.imytech.net/"
  chart      = "webhook-broker-chart"
  version    = "0.1.0-dev"

  depends_on = [module.rds, kubernetes_namespace.webhook_broker_namespace, helm_release.external_dns]

  values = [
    templatefile("conf/webhook-broker-values.yml", {https_cert_arn=var.webhook_broker_https_cert_arn, db_url="${module.rds.this_db_instance_username}:${var.db_password}@tcp(${module.rds.this_db_instance_endpoint})/${module.rds.this_db_instance_name}?charset=utf8&parseTime=true&multiStatements=true", access_log_s3_bucket=var.webhook_broker_access_log_bucket, access_log_s3_path_prefix=var.webhook_broker_access_log_path, subnets=join(", ", module.vpc.private_subnets), hostname=var.webhook_broker_hostname})
  ]
}
