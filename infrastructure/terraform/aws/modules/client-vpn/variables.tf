variable "region" {
  default = "us-east-1"
}

variable "private_subnets" {
}

variable "vpc_id" {
}

variable "vpn_cidr" {
  default = "17.10.0.0/16"
}

variable "vpn_server_cert_arn" {
  default = "arn:aws:acm:eu-west-2:xxxxxxxx:certificate/xxxxx"
}

variable "vpn_client_cert_arn" {
  default = "arn:aws:acm:eu-west-2:xxxxxxxxx:certificate/xxxxx"
}

variable "opvn_filepath" {
  default = "config.ovpn"
}
