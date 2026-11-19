output "function_artifact" {
  value = {
    path   = lambdabuild_python_package.code.output_path
    sha256 = lambdabuild_python_package.code.output_sha256
  }
}

output "layer_artifact" {
  value = {
    path   = lambdabuild_python_package.deps.output_path
    sha256 = lambdabuild_python_package.deps.output_sha256
  }
}

output "bundled_artifact" {
  value = {
    path   = lambdabuild_python_package.bundled.output_path
    sha256 = lambdabuild_python_package.bundled.output_sha256
  }
}

output "function_code_sha256" {
  description = "As stored by the mock Lambda; matches function_artifact.sha256 (base64)."
  value       = aws_lambda_function.this.code_sha256
}

output "layer_arn" {
  value = aws_lambda_layer_version.deps.arn
}

output "bundled_function_code_sha256" {
  description = "As stored by the mock Lambda; matches bundled_artifact.sha256 (base64)."
  value       = aws_lambda_function.bundled.code_sha256
}
