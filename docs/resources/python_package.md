---
page_title: "lambdabuild_python_package Resource - lambdabuild"
subcategory: ""
description: |-
  Builds a deterministic Lambda zip Artifact (function or layer) from a uv-managed Python project. Plan computes the Input fingerprint; apply builds.
---

# lambdabuild_python_package (Resource)

Builds a deterministic Lambda zip Artifact (function or layer) from a uv-managed Python project. Plan computes the Input fingerprint; apply builds.

The zip is built during `apply`, not `plan` ([ADR 0001](https://github.com/cloudandthings/terraform-provider-lambdabuild/blob/main/docs/adr/0001-build-at-apply-not-plan.md)).
Plan computes only the input fingerprint, so `output_path` changes whenever the inputs change.

## Behaviour notes

- **What is fingerprinted is what ships.** With `install_project`, the project and every local dependency (sibling workspace members, path dependencies) are built into wheels from a per-build copy of their fingerprinted files: the whole tree minus `source_exclude` and `.gitignore`d paths. `.gitignore` files above `project_dir` up to the git repository root apply, as in git. A file excluded from the fingerprint never reaches the wheel, so package data must not live in a folder matched by `source_exclude` (by default `build`, `dist`, `tests`, ... at any depth). Files outside the tree (for example a `readme = "../README.md"`) are not available to the build backend, and setuptools-scm style versions from git are not available either; use a static version.
- **Dependency groups are opt-in.** Only the groups listed in `groups` are installed; `[tool.uv] default-groups` is ignored and `dev` never ships.
- **Dependency layer without project code.** Set `artifact_type = "layer"`, `install_project = false` and `source_include = []`.
- **Sources changed after `terraform plan -out`.** Applying the saved plan fails with Terraform's own error `Provider produced inconsistent final plan ... .input_fingerprint`, followed by a request to report a provider bug. The apply failed safely and built nothing; run `terraform plan` again. A change in the moment between Terraform's apply-time re-plan and the build fails with "Build inputs changed since plan" instead.
- **Saved no-op plans on another runner.** A saved plan that plans no change builds nothing. Applied on a runner without the zip, the zip stays absent there; resources that read the file itself in the same apply (for example `aws_s3_object` with `source`) fail. Plan on the runner that applies, or keep `on_missing_artifact = "rebuild"` so a missing zip plans its rebuild.
- **`sbom` is part of the Artifact.** Toggling `sbom` changes the Input fingerprint and `output_path`, so dependent Lambda resources update once even though the zip bytes are the same.
- **Index credentials** come only from the environment (`UV_INDEX_<NAME>_USERNAME` / `UV_INDEX_<NAME>_PASSWORD`); see the provider documentation.

## Example Usage

### Function

```terraform
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
```

### Layer

```terraform
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
```

### Code-only function plus dependency layer

```terraform
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
```

### Large Artifacts: upload through S3

```terraform
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
```

### Per-workspace `output_dir`

```terraform
# Workspaces sharing a working directory must not share an output_dir, or one
# workspace's cleanup could delete another's Artifact.
resource "lambdabuild_python_package" "function" {
  project_dir    = "${path.module}/app"
  output_dir     = "${path.module}/build/${terraform.workspace}"
  python_version = "3.12"
}
```

### Drift-detection jobs

```terraform
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
```

### SBOM upload

```terraform
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `output_dir` (String) Directory for the zip and SBOM. Keep it relative so state stays valid across machines.
- `project_dir` (String) Directory containing pyproject.toml and uv.lock. Relative paths resolve against Terraform's working directory.
- `python_version` (String) Lambda Python runtime version, one of Lambda's non-deprecated Python runtimes compiled into this release: "3.11", "3.12", "3.13", "3.14".

### Optional

- `architecture` (String) "x86_64" or "arm64".
- `artifact_type` (String) "function", or "layer" to place content under python/.
- `exclude_packages` (List of String) Packages removed after install, such as boto3. Nothing is stripped by default.
- `extras` (List of String) Optional-dependency extras to install.
- `groups` (List of String) Dependency groups to install (the dev group is never installed unless listed).
- `install_dependencies` (Boolean) Install third-party Packages. false builds a code-only function Artifact.
- `install_project` (Boolean) Install the project as a wheel. Defaults to true when pyproject.toml has a [build-system] table.
- `lock_mode` (String) "locked" fails on a stale uv.lock; "frozen" skips that check.
- `name` (String) Prefix of the zip file name. Defaults to the base name of project_dir.
- `on_missing_artifact` (String) Behaviour when the Artifact is absent on this runner and inputs are unchanged: "rebuild" plans an in-place change to artifact_present only (hashes keep their values) and rebuilds at apply, failing if the hash differs from state; "ignore" plans no change and builds nothing. Changing this argument alone never produces a diff, so a drift-detection job can set "ignore" through a variable against state written with "rebuild" and still get an empty plan; the configured value always decides, and the value in state is the one set at creation.
- `platform_tag` (String) Wheel platform tag. Defaults to manylinux_2_34 for Python 3.12+ and manylinux_2_26 before.
- `sbom` (Boolean) Write a CycloneDX SBOM beside the zip.
- `size_limit_mode` (String) "error" fails a build above Lambda's 250 MB unzipped limit; "warn" builds anyway.
- `source_dir` (String) Root for copied source when install_project is false. Defaults to project_dir, or the workspace_member's directory.
- `source_exclude` (List of String) Globs skipped when copying and fingerprinting. A set value replaces the defaults. .gitignore'd paths are also skipped.
- `source_include` (List of String) Globs of source files to copy when install_project is false.
- `triggers` (Map of String) Arbitrary values that force a rebuild when they change.
- `workspace_member` (String) uv workspace member to build (uv's --package). project_dir is then the workspace root holding uv.lock; the member's pyproject.toml decides install_project, its directory is the default source_dir, and sibling members and path dependencies it uses are fingerprinted and installed as wheels.

### Read-Only

- `artifact_present` (Boolean) Whether the zip (and SBOM) exist on this runner and match the stored hashes. Refreshed by Read.
- `input_fingerprint` (String) SHA-256 over every build input. Known at plan.
- `output_base64sha256` (String) Base64 SHA-256 of the zip, for aws_lambda_function.source_code_hash.
- `output_path` (String) <output_dir>/<name>-<fingerprint prefix>.zip. Known at plan.
- `output_sha256` (String) Hex SHA-256 of the zip.
- `output_size` (Number) Zip size in bytes.
- `python_full_version` (String) Exact managed interpreter version used; null when uv did not run.
- `sbom_path` (String) <output_dir>/<name>-<fingerprint prefix>.cdx.json; null when sbom is false. Known at plan.
- `sbom_sha256` (String) Hex SHA-256 of the SBOM; null when sbom is false.
- `unpacked_size` (Number) Total uncompressed size in bytes.
- `uv_version` (String) uv version used for the build; null when uv did not run.
