variable "aws_endpoint" {
  description = "Mock AWS endpoint. Set by the mock-aws tasks from MOCK_AWS_PORT."
  type        = string
  default     = "http://localhost:5050"
}

variable "region" {
  type    = string
  default = "eu-west-1"
}

variable "architecture" {
  description = "Lambda architecture to build for: x86_64 or arm64."
  type        = string
  default     = "arm64"
}

variable "python_version" {
  type    = string
  default = "3.12"
}

variable "drift_check" {
  description = "Set true to plan like a scheduled drift job on a fresh runner (on_missing_artifact = ignore)."
  type        = bool
  default     = false
}
