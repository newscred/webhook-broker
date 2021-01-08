variable "region" {
  default = "us-east-1"
}

variable "azs" {
  default = ["us-east-1a", "us-east-1b", "us-east-1c"]
}

variable "vpn_server_cert_arn" {
  default = "arn:aws:acm:us-east-1:aws:certificate/cert-id"
}

variable "vpn_client_cert_arn" {
  default = "arn:aws:acm:us-east-1:aws:certificate/cert-id"
}

variable "create_rds" {
  default = true
}

variable "create_es" {
  default = true
}
