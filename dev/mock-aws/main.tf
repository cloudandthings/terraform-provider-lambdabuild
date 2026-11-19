# Two ways to ship the same uv project in ./app:
# - "this": the function Artifact holds only app code, dependencies ship as a layer.
# - "bundled": one function Artifact holding app code and dependencies, no layer.

locals {
  runtime = "python${var.python_version}"
}

resource "lambdabuild_python_package" "code" {
  project_dir          = "${path.module}/app"
  output_dir           = "${path.module}/build"
  name                 = "code"
  python_version       = var.python_version
  architecture         = var.architecture
  install_dependencies = false
  on_missing_artifact  = var.drift_check ? "ignore" : "rebuild"
}

resource "lambdabuild_python_package" "deps" {
  project_dir         = "${path.module}/app"
  output_dir          = "${path.module}/build"
  name                = "deps"
  python_version      = var.python_version
  architecture        = var.architecture
  artifact_type       = "layer"
  install_project     = false
  source_include      = []
  on_missing_artifact = var.drift_check ? "ignore" : "rebuild"
}

resource "lambdabuild_python_package" "bundled" {
  project_dir         = "${path.module}/app"
  output_dir          = "${path.module}/build"
  name                = "bundled"
  python_version      = var.python_version
  architecture        = var.architecture
  on_missing_artifact = var.drift_check ? "ignore" : "rebuild"
}

resource "aws_iam_role" "lambda" {
  name = "lambdabuild-mock"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_lambda_layer_version" "deps" {
  layer_name               = "lambdabuild-mock-deps"
  filename                 = lambdabuild_python_package.deps.output_path
  source_code_hash         = lambdabuild_python_package.deps.output_base64sha256
  compatible_runtimes      = [local.runtime]
  compatible_architectures = [var.architecture]
}

resource "aws_lambda_function" "this" {
  function_name    = "lambdabuild-mock"
  role             = aws_iam_role.lambda.arn
  runtime          = local.runtime
  architectures    = [var.architecture]
  handler          = "handler.handler"
  filename         = lambdabuild_python_package.code.output_path
  source_code_hash = lambdabuild_python_package.code.output_base64sha256
  layers           = [aws_lambda_layer_version.deps.arn]
}

resource "aws_lambda_function" "bundled" {
  function_name    = "lambdabuild-mock-bundled"
  role             = aws_iam_role.lambda.arn
  runtime          = local.runtime
  architectures    = [var.architecture]
  handler          = "handler.handler"
  filename         = lambdabuild_python_package.bundled.output_path
  source_code_hash = lambdabuild_python_package.bundled.output_base64sha256
}

# SBOMs next to the Artifacts, as a release pipeline would keep them.
resource "aws_s3_bucket" "sbom" {
  bucket = "lambdabuild-mock-sbom"
}

resource "aws_s3_object" "sbom" {
  for_each = {
    code    = lambdabuild_python_package.code
    deps    = lambdabuild_python_package.deps
    bundled = lambdabuild_python_package.bundled
  }

  bucket      = aws_s3_bucket.sbom.id
  key         = "sbom/${basename(each.value.sbom_path)}"
  source      = each.value.sbom_path
  source_hash = each.value.sbom_sha256
}
