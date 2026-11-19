# A Lambda function Artifact built from a uv project (pyproject.toml + uv.lock).
resource "lambdabuild_python_package" "function" {
  project_dir    = "${path.module}/app"
  output_dir     = "${path.module}/build"
  python_version = "3.12"
  architecture   = "arm64"
}

resource "aws_lambda_function" "this" {
  function_name    = "example"
  role             = aws_iam_role.lambda.arn
  runtime          = "python3.12"
  architectures    = ["arm64"]
  handler          = "app.handler"
  filename         = lambdabuild_python_package.function.output_path
  source_code_hash = lambdabuild_python_package.function.output_base64sha256
}
