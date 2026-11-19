terraform {
  required_version = ">= 1.9"

  required_providers {
    # Resolved to the locally built binary through dev_overrides
    # (see `mise run mock-aws:tf`), never downloaded.
    lambdabuild = {
      source = "cloudandthings/lambdabuild"
    }
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}
