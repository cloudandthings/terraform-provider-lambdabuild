# Workspaces sharing a working directory must not share an output_dir, or one
# workspace's cleanup could delete another's Artifact.
resource "lambdabuild_python_package" "function" {
  project_dir    = "${path.module}/app"
  output_dir     = "${path.module}/build/${terraform.workspace}"
  python_version = "3.12"
}
