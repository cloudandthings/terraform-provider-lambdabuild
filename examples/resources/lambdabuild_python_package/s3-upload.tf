# Artifacts above 50 MB (zipped) cannot be uploaded directly to Lambda: stage
# them in S3 and deploy from there.
resource "lambdabuild_python_package" "big" {
  project_dir    = "${path.module}/app"
  output_dir     = "${path.module}/build"
  python_version = "3.12"
}

resource "aws_s3_object" "package" {
  bucket      = aws_s3_bucket.artifacts.id
  key         = "lambda/${basename(lambdabuild_python_package.big.output_path)}"
  source      = lambdabuild_python_package.big.output_path
  source_hash = lambdabuild_python_package.big.output_sha256
}

resource "aws_lambda_function" "this" {
  function_name     = "example"
  role              = aws_iam_role.lambda.arn
  runtime           = "python3.12"
  handler           = "app.handler"
  s3_bucket         = aws_s3_object.package.bucket
  s3_key            = aws_s3_object.package.key
  s3_object_version = aws_s3_object.package.version_id
  source_code_hash  = lambdabuild_python_package.big.output_base64sha256
}
