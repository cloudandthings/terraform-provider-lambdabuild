# terraform-provider-lambdabuild

[![CI](https://github.com/cloudandthings/terraform-provider-lambdabuild/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/cloudandthings/terraform-provider-lambdabuild/actions/workflows/ci.yml)
[![Nightly](https://github.com/cloudandthings/terraform-provider-lambdabuild/actions/workflows/nightly.yml/badge.svg?branch=main)](https://github.com/cloudandthings/terraform-provider-lambdabuild/actions/workflows/nightly.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/cloudandthings/terraform-provider-lambdabuild)](go.mod)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL_2.0-brightgreen.svg)](LICENSE)
[![Terraform](https://img.shields.io/badge/Terraform-%E2%89%A51.0-7B42BC?logo=terraform)](https://developer.hashicorp.com/terraform)
[![OpenTofu](https://img.shields.io/badge/OpenTofu-supported-FFDA18?logo=opentofu)](https://opentofu.org)
[![uv](https://img.shields.io/badge/built%20with-uv-DE5FE9)](https://github.com/astral-sh/uv)

A Terraform provider that builds **deterministic AWS Lambda zips** (functions and layers) from [uv](https://github.com/astral-sh/uv)-managed Python projects.

uv is embedded in the provider binary and fetches its own managed Python, so the machine running Terraform needs **nothing but Terraform**: no Python, no pip, no Docker. The same sources produce byte-identical zips on macOS, Linux and Windows, so `source_code_hash` only changes when your code or lockfile does.

> **Status:** pre-release. Nothing is published to the Terraform Registry yet; see [ROADMAP.md](ROADMAP.md).

## Features

- **Reproducible Artifacts.** Fixed timestamps, file order and permissions; golden-hash tests pin the output of every fixture, and CI runs them on Linux, macOS and Windows.
- **Functions and layers.** Bundle code and dependencies together, or ship code-only functions with a separate dependency layer.
- **Built for Lambda.** `x86_64` or `arm64`, Python 3.11–3.14, manylinux wheels resolved for the target, not the host. Fails early when a zip exceeds Lambda's 250 MB unzipped limit.
- **Works from `uv.lock`.** Workspaces, extras, dependency groups, path dependencies and private indexes all come from your existing uv project.
- **CycloneDX SBOM** written beside every zip by default.
- **Plan-safe.** The Input fingerprint and `output_path` are known at plan; the zip is built at apply ([ADR 0001](docs/adr/0001-build-at-apply-not-plan.md)), so saved plans work on ephemeral CI runners.
- **Corporate networks.** Named private indexes with credentials from the environment (never in state), proxies, custom CAs, Python mirrors and offline mode.

## Usage

```terraform
terraform {
  required_providers {
    lambdabuild = {
      source = "cloudandthings/lambdabuild"
    }
  }
}

provider "lambdabuild" {}

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

More examples (layers, code-only functions, S3 upload, SBOM upload, drift-detection jobs) are in [`examples/`](examples/resources/lambdabuild_python_package).

## Documentation

- [Provider configuration](docs/index.md): cache directory, private indexes and credentials, proxies and mirrors.
- [`lambdabuild_python_package`](docs/resources/python_package.md): every argument and attribute, plus behaviour notes.
- [GLOSSARY.md](GLOSSARY.md): the terms used throughout (Artifact, Input fingerprint, ...).
- [Architecture decisions](docs/adr/) and [CHANGELOG.md](CHANGELOG.md).

## Development

All tooling is pinned and run through [mise](https://mise.jdx.dev):

```sh
mise install            # Go, Terraform, OpenTofu, GoReleaser, golangci-lint, ...
mise run ci             # lint, unit, acceptance and docs checks, as CI runs them
mise tasks              # everything else (golden hashes, smoke tests, mock AWS, ...)
```

`mise run mock-aws:up` then `mise run mock-aws:apply` plans and applies the provider from this checkout against a local mock AWS; see [dev/mock-aws](dev/mock-aws/README.md).

Registry docs under `docs/` are generated: edit schema descriptions, `templates/` or `examples/`, then run `mise run docs:generate`.

## License

[MPL-2.0](LICENSE). Release archives also carry the licences of the embedded uv binary in [`licenses/`](licenses/).
