# Upload the CycloneDX SBOM next to the Artifact (sbom = true is the default).
resource "lambdabuild_python_package" "function" {
  project_dir    = "${path.module}/app"
  output_dir     = "${path.module}/build"
  python_version = "3.12"
}

resource "aws_s3_object" "sbom" {
  bucket      = aws_s3_bucket.artifacts.id
  key         = "sbom/${basename(lambdabuild_python_package.function.sbom_path)}"
  source      = lambdabuild_python_package.function.sbom_path
  source_hash = lambdabuild_python_package.function.sbom_sha256
}
