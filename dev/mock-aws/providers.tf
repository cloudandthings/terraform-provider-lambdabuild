provider "lambdabuild" {}

# Every call goes to the mock (moto server); nothing reaches real AWS.
provider "aws" {
  region     = var.region
  access_key = "test"
  secret_key = "test"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true

  endpoints {
    iam    = var.aws_endpoint
    lambda = var.aws_endpoint
    s3     = var.aws_endpoint
    sts    = var.aws_endpoint
  }
}
