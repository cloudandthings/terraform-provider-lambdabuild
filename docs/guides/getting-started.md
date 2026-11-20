---
page_title: "Getting started"
subcategory: ""
description: |-
  Build and deploy your first Lambda function and layer from a uv project.
---

# Getting started

This guide takes a uv project from your laptop to a deployed Lambda function. It assumes you know Terraform and have used [uv](https://docs.astral.sh/uv/) to manage a Python project.

## What you need

- **Terraform 1.0 or later**, or OpenTofu. Nothing else: the provider carries its own uv, and uv downloads its own Python. You do not need Python, pip or Docker on the machine that runs Terraform.
- **A uv project**: a directory with a `pyproject.toml` and a **committed** `uv.lock`. lambdabuild never resolves dependencies; `uv.lock` decides exactly which Package versions ship.
- **A supported host**: macOS (Intel or Apple silicon), Linux (x86_64 or arm64, glibc or musl), or Windows x86_64.
- **Network access** on the first build, to your package index (PyPI by default) and to GitHub, where uv downloads its managed Python builds. The *Private indexes and restricted networks* guide covers proxies, mirrors and offline builds.

## 1. Add the provider

```terraform
terraform {
  required_providers {
    lambdabuild = {
      source = "cloudandthings/lambdabuild"
    }
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "lambdabuild" {}
```

Every provider argument is optional. Run `terraform init`.

## 2. Lay out the project

```text
infra/
├── main.tf
└── app/
    ├── pyproject.toml
    ├── uv.lock
    └── handler.py
```

A minimal `app/pyproject.toml`:

```toml
[project]
name = "app"
version = "0.1.0"
requires-python = ">=3.12"
dependencies = ["requests>=2.32"]
```

Run `uv lock` in `app/` and commit `uv.lock`.

## 3. Build and deploy

```terraform
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
  handler          = "handler.handler"
  filename         = lambdabuild_python_package.function.output_path
  source_code_hash = lambdabuild_python_package.function.output_base64sha256
}
```

Keep `python_version` and `architecture` equal to the function's `runtime` and `architectures`: lambdabuild installs Linux wheels for exactly that target, whatever your own machine is.

Add the build directory to `.gitignore`:

```text
build/
```

## 4. Plan and apply

`terraform plan` hashes your inputs but builds nothing. It shows the **Input fingerprint** and the `output_path` derived from it; the zip hashes show as `(known after apply)`.

`terraform apply` builds the zip into `output_dir`, for example `build/app-3f9c2a1b7d4e5f60.zip`, writes a CycloneDX SBOM beside it, and deploys it.

Run `terraform plan` again without changes: nothing changes. Edit `handler.py` and plan: the fingerprint and `output_path` change, so the function updates. Same sources give the same bytes on every machine, so a teammate on Windows or a Linux CI runner plans no change either.

## How your code gets into the zip

lambdabuild picks one of two ways from your `pyproject.toml`:

| `pyproject.toml` has | What ships | `handler` example |
| --- | --- | --- |
| no `[build-system]` table (an application, like the example above) | Your `.py` files from `project_dir` (`source_include`, default `**/*.py`), minus `tests`, `build`, `dist`, `.venv` and the other `source_exclude` defaults, at the zip root. | `handler.handler` |
| a `[build-system]` table (an installable package) | Your project built as a wheel and installed, exactly as `pip install` would lay it out. | `mypackage.handler.handler` |

Set `install_project` to choose explicitly. Third-party Packages from `uv.lock` are installed beside your code in both cases, unless `install_dependencies = false`.

## Layers

A layer zip puts everything under `python/`, where Lambda adds it to the import path. A common split keeps the dependencies in a layer and your code in a small function zip, so a code change uploads only kilobytes:

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

## uv workspaces

For a uv workspace, point `project_dir` at the workspace root (where `uv.lock` lives) and name the member to build:

```terraform
resource "lambdabuild_python_package" "orders" {
  project_dir      = "${path.module}/services"
  workspace_member = "orders"
  output_dir       = "${path.module}/build"
  python_version   = "3.12"
}
```

Sibling members and path dependencies that `orders` uses are built as wheels and shipped with it.

## Next steps

- **CI/CD pipelines** guide: saved plans, fresh runners, caching and drift detection.
- **Reproducible builds** guide: what the Input fingerprint covers and when Artifacts change.
- **Troubleshooting** guide: common errors and their fixes.
- The `lambdabuild_python_package` resource page lists every argument.
