# Third-party dependencies only, as a layer (content under python/).
resource "lambdabuild_python_package" "deps" {
  project_dir    = "${path.module}/app"
  output_dir     = "${path.module}/build"
  name           = "deps"
  python_version = "3.12"
  artifact_type  = "layer"
  # Dependencies only: no project wheel and no copied source files, so
  # code edits rebuild only the function Artifact.
  install_project = false
  source_include  = []
}

resource "aws_lambda_layer_version" "deps" {
  layer_name          = "example-deps"
  filename            = lambdabuild_python_package.deps.output_path
  source_code_hash    = lambdabuild_python_package.deps.output_base64sha256
  compatible_runtimes = ["python3.12"]
}
