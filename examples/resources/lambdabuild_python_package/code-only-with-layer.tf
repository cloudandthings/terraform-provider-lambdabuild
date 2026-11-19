# The function Artifact holds only your code (install_dependencies = false);
# the dependencies ship as a layer built from the same project.
resource "lambdabuild_python_package" "code" {
  project_dir          = "${path.module}/app"
  output_dir           = "${path.module}/build"
  name                 = "code"
  python_version       = "3.12"
  install_dependencies = false
}

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
  layer_name       = "example-deps"
  filename         = lambdabuild_python_package.deps.output_path
  source_code_hash = lambdabuild_python_package.deps.output_base64sha256
}

resource "aws_lambda_function" "this" {
  function_name    = "example"
  role             = aws_iam_role.lambda.arn
  runtime          = "python3.12"
  handler          = "app.handler"
  filename         = lambdabuild_python_package.code.output_path
  source_code_hash = lambdabuild_python_package.code.output_base64sha256
  layers           = [aws_lambda_layer_version.deps.arn]
}
