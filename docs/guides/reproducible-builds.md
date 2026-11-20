---
page_title: "Reproducible builds"
subcategory: ""
description: |-
  What lambdabuild promises about Artifact bytes, what the Input fingerprint covers, and when an upgrade redeploys every function once.
---

# Reproducible builds

lambdabuild's promise: **the same inputs give the same bytes, on every host.** A zip built on a Mac, on a Linux CI runner and on a Windows laptop has the same SHA-256, so `source_code_hash` changes only when your code or lockfile does, and Lambda never redeploys for nothing.

## What the Input fingerprint covers

The Input fingerprint is a SHA-256 over everything that decides the Artifact's bytes. Equal fingerprints promise byte-identical Artifacts. Its first 16 hex characters name the files: `<name>-<fingerprint prefix>.zip`.

**In the fingerprint:**

- `pyproject.toml` and `uv.lock` of `project_dir`.
- Your source files: the whole project tree when the project is installed as a wheel, or the `source_include` files of `source_dir` otherwise. Paths matched by `source_exclude` or a `.gitignore` (up to the git repository root) are left out.
- Every local dependency the build installs (sibling workspace members, path dependencies), with the same exclusions.
- These arguments: `python_version`, `architecture`, `artifact_type`, `platform_tag`, `workspace_member`, `extras`, `groups`, `exclude_packages`, `install_dependencies`, `install_project`, `source_include`, `source_exclude`, `sbom` and `triggers`. List order does not matter.
- The **Packaging-format version** of the provider release (see below).

**Not in the fingerprint**, because it cannot change the bytes:

- Where things are: `project_dir`, `output_dir` and the host's paths. Moving a checkout does not rebuild.
- `name` (it changes the file name only), `lock_mode`, `size_limit_mode`, `on_missing_artifact`.
- Every provider argument: caches, indexes, proxies, `offline`, `uv_path`.
- The uv and Python patch versions in use. uv is pinned per provider release and tested against golden hashes; the [design decision](https://github.com/cloudandthings/terraform-provider-lambdabuild/blob/main/docs/adr/0003-uv-version-not-in-fingerprint.md) explains why it stays out.
- File timestamps and permissions.

Use `triggers` to force a rebuild from something lambdabuild cannot see:

```terraform
triggers = {
  build_number = var.build_number
}
```

## How the bytes are made identical

- Every zip entry has the timestamp 1980-01-01 and is compressed at one fixed level.
- Entries are sorted by path. Paths always use `/`.
- Permissions come from file content, not the file system: ELF binaries and `#!` scripts are `0755`, everything else `0644`. Windows, which has no executable bit, produces the same zip.
- Files that differ per host are removed after install: `bin/` console scripts (they embed the interpreter path), `__pycache__` and `.pyc` files, `direct_url.json` (absolute paths) and uv's cache markers. The matching lines are dropped from each Package's `RECORD`.
- Packages are installed as Linux wheels for the target platform, never built from source on the host.
- The SBOM has no timestamp, sorted components and a serial number derived from the zip hash.

Two things on your side keep this promise:

- **Line endings.** A CRLF checkout of a text file is different bytes. Commit `* text=auto eol=lf` in `.gitattributes`; the plan warns `crlf_line_endings` when it sees CRLF.
- **Static project versions.** The wheel is built from a copy of the fingerprinted files, without the `.git` directory, so versions computed from git tags (setuptools-scm and similar) are not available. Use a static `version` in `pyproject.toml`.

## One-time redeploys

Sometimes a provider release must change how Artifacts are made, for example to fix a packaging rule. Such a release bumps the **Packaging-format version**. Because it is part of every Input fingerprint, every `output_path` changes on the next plan and every function and layer redeploys **once**. Nothing else is needed: apply the plan.

These releases are marked **One-time redeploy** in the [changelog](https://github.com/cloudandthings/terraform-provider-lambdabuild/blob/main/CHANGELOG.md). Releases without that heading never change an existing Artifact.

## Checking it yourself

Build the same commit on two machines and compare `output_sha256`, for example through an output:

```terraform
output "function_sha256" {
  value = lambdabuild_python_package.function.output_sha256
}
```

When a missing Artifact is rebuilt (see the *CI/CD pipelines* guide), lambdabuild does this check for you and fails with `Non-reproducible build` if the bytes differ from state.
