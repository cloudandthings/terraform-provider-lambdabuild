---
page_title: "Troubleshooting"
subcategory: ""
description: |-
  Common lambdabuild errors and warnings, what they mean and how to fix them.
---

# Troubleshooting

Find the message you see, then follow the fix. Messages are quoted as lambdabuild prints them; `...` stands for details that vary.

## Plan and apply lifecycle

### `Provider produced inconsistent final plan ... .input_fingerprint: was "...", but now "..."`

The sources changed between `terraform plan -out` and `terraform apply`. Terraform stops before anything is built. The note asking you to report a provider bug comes from Terraform and does not apply here. Run `terraform plan` again.

### `Build inputs changed since plan`

The same situation, caught during the build itself: a file changed while apply was running. No Artifact is left behind. Plan again, and make sure nothing writes into the project while Terraform runs.

### `Non-reproducible build: rebuilding ... from unchanged inputs produced sha256 ..., but state records ...`

A missing Artifact was rebuilt from the same inputs, and the bytes differ from those recorded in state. Common causes:

- Runners use different `uv_path` binaries, or one uses `uv_path` and another the embedded uv.
- A build backend writes something host-specific into the project wheel, such as a timestamp.

To move on, replace the resource so it is built fresh: `terraform apply -replace=lambdabuild_python_package.<name>`. If the inputs really are identical and you use the embedded uv, please [open an issue](https://github.com/cloudandthings/terraform-provider-lambdabuild/issues) with both `output_sha256` values: it is a bug.

### A plan on a fresh runner shows `artifact_present` changing to `true`

Expected. The zip is not on this runner, so it will be rebuilt; the hashes do not change, so the Lambda function is not updated. For read-only plans (drift detection), set `on_missing_artifact = "ignore"`. See the *CI/CD pipelines* guide.

### `aws_s3_object` or `filebase64sha256()` cannot find the zip at apply

A saved plan that planned no change to the Artifact was applied on a runner that does not have it. Plan on the runner that applies.

## Project and lockfile

### `project_dir ...: uv.lock not found`

lambdabuild only builds uv projects with a lockfile. Run `uv lock` in `project_dir` and commit `uv.lock`.

### uv reports that the lockfile needs to be updated but `--locked` was provided

`uv.lock` is out of date with `pyproject.toml`. Run `uv lock` and commit. To build from the lockfile as it stands, set `lock_mode = "frozen"`.

### `project_dir is a uv workspace root without a [project] table: set workspace_member ...`

`project_dir` points at a workspace root. Set `workspace_member` to the member to build; the message lists them.

### `workspace_member "...": not a member of the uv workspace (members: ...)`

Check the spelling against the listed members, which come from `uv.lock`.

### `package "..." has no wheel for ... (Python ...): Lambda builds install wheels only, ...`

lambdabuild installs only pre-built wheels for the Lambda platform, never building Packages from source. Pin a version of that Package that publishes a `manylinux` wheel for your `python_version` and `architecture`: for Python 3.12 and later, a wheel built for glibc 2.34 or older (Amazon Linux 2023); for 3.11, glibc 2.26 or older (Amazon Linux 2). Building such Packages in Docker is on the roadmap.

### The project's package data is missing from the zip

The project wheel is built only from files in the Input fingerprint. A file inside a folder matched by `source_exclude` (by default `build`, `dist`, `tests`, `.venv`, `__pycache__`, `*.egg-info`, ...) or by `.gitignore` does not ship. Move the data, or set `source_exclude` (a set value replaces the defaults). Files outside the project tree, such as `readme = "../README.md"`, are not available to the build either.

### The plan warns `lambdabuild: crlf_line_endings`

Some source files have Windows line endings, so this checkout fingerprints differently from an LF checkout of the same commit. Add `* text=auto eol=lf` to `.gitattributes` and re-checkout.

## Lambda limits

### `Artifact is ... MB, above Lambda's 250 MB unzipped limit. Largest Packages by unpacked size: ...`

Lambda would reject this function. Options, in order:

1. Exclude Packages the runtime already has: `exclude_packages = ["boto3", "botocore", "s3transfer"]`.
2. Check `groups` and `extras` do not pull in development tools.
3. `size_limit_mode = "warn"` builds it anyway. Moving dependencies to a layer does not help: Lambda's limit covers the function and all its layers together.

### Warning `Artifact is ... MB, above Lambda's 50 MB direct-upload limit`

Lambda cannot take the zip directly. Upload it with `aws_s3_object` and deploy from S3; the resource page has an example.

## Toolchain and network

### `... must not contain credentials (user:password@) ...`

Credentials in provider URLs would land in state. Name the index and set `UV_INDEX_<NAME>_USERNAME` and `UV_INDEX_<NAME>_PASSWORD`. See the *Private indexes and restricted networks* guide.

### `python_version "..." is not a supported Lambda Python runtime; supported: ...`

Only Lambda's non-deprecated Python runtimes are accepted. A new runtime arrives in a provider patch release.

### `this provider build has no embedded uv for the host platform`

The host is not one of the release platforms (macOS amd64/arm64, Linux amd64/arm64, Windows amd64). Set `uv_path` to a uv binary for this host.

### uv fails with `permission denied` when it starts, or the managed Python cannot run

`cache_dir` is on a file system that does not allow running programs, such as a `noexec` `/tmp`. Point `cache_dir` at a directory that does.

### Downloads fail with certificate errors

A proxy or internal index uses a certificate your runner's bundle does not trust. Set `SSL_CERT_FILE` to your CA bundle, or `native_tls = true` to use the operating system's store.

### The function fails at import with `invalid ELF header` or a missing shared library

The Artifact was built for another target than the function runs on. Make `architecture` match the function's `architectures` and `python_version` match its `runtime`. A custom `platform_tag` newer than the runtime's Amazon Linux can also cause this.

## Getting more detail

Build errors include uv's own error output, with credentials redacted. To debug a build without Terraform, see the [development documentation](https://github.com/cloudandthings/terraform-provider-lambdabuild/blob/main/docs/development/testing.md#debugging-a-build-without-terraform).
