# Scheduled `terraform plan -detailed-exitcode` jobs run on fresh runners
# where the zip does not exist. "ignore" keeps those plans empty instead of
# planning a rebuild. Changing on_missing_artifact alone never produces a
# diff, so the job may set it against state written by the deploy pipeline
# with the default "rebuild".
variable "drift_check" {
  type    = bool
  default = false
}

resource "lambdabuild_python_package" "function" {
  project_dir         = "${path.module}/app"
  output_dir          = "${path.module}/build"
  python_version      = "3.12"
  on_missing_artifact = var.drift_check ? "ignore" : "rebuild"
}
