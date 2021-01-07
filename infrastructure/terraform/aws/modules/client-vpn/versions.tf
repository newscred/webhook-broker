terraform {
  required_version = ">= 0.14.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = "3.22.0"
    }

    local = {
      version = "~> 2.0.0"
    }

    external = {
      version = "~> 2.0.0"
    }
  }
}
