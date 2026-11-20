# Testing

lambdabuild's main promise, same inputs and same bytes on every host, is a testing problem. This page explains the test layers, how fixtures and golden hashes work, and how to debug a build.

## Test layers

| Layer | Where | Task | Network | What it proves |
| --- | --- | --- | --- | --- |
| **Seam B: core** | `internal/core/*_test.go` | `test:unit` | No (after the first managed Python download) | Fingerprinting, installing, cleaning, packaging and SBOM produce the right files and bytes. |
| **Golden hashes** | `internal/core/golden_test.go`, `golden_sbom_test.go` | `test:golden` (also part of `test:unit`) | No | Every fixture's fingerprint, zip and SBOM hash is exactly the pinned value, on every OS in CI. |
| **Seam A: resource** | `internal/provider/*_test.go` | `test:acc`, `test:tofu` | No | The Terraform lifecycle: plan, apply, missing Artifacts, saved plans, drift detection, schema wiring. |
| **Network** | `network_test.go` (`network` build tag) | `test:network` | PyPI | Real wheels (numpy) install and hash as pinned. Nightly. |
| **Container smoke** | `testdata/smoke/` | `test:smoke` | PyPI, GitHub, public.ecr.aws | Artifacts built by Terraform in a container with nothing else installed import correctly in the official Lambda Python images, x86_64 and arm64. Nightly and before release. |

Tests reach the internals only through `core` (seam B) or Terraform (seam A). The packages under `internal/core/internal/` have no tests of their own: if something matters, it is visible through the interface.

## Fixtures

`testdata/fixtures/` holds small uv projects, each with a committed `uv.lock` and, where it has dependencies, a local PEP 503 index under `simple/` with fake Packages. Builds install from those indexes, so no test needs PyPI.

| Fixture | Covers |
| --- | --- |
| `code-only` | Copied source without uv, include/exclude rules, `.gitignore`. |
| `pure-deps` | Third-party pure-Python Packages. |
| `compiled-deps` | Platform-specific wheels (x86_64 and aarch64) selected for the target, not the host. |
| `extras-groups` | Optional-dependency extras and dependency groups. |
| `packaged` | An installable project with package data, built as a wheel. |
| `intree-backend` | An in-tree build backend that writes leftovers into the tree it builds, like setuptools: wheels are built from a copy. |
| `workspace` | uv workspace members, sibling members and a path dependency. |
| `sbom-licences` | Licence metadata variants in the SBOM. |
| `sdist-only` | A Package with no wheel: the clear "no wheel" error. |
| `numpy-deps` | Real numpy from PyPI (network and smoke tests only). |

The fake Packages are generated deterministically by `testdata/gen/fake_wheels.py`. After changing the generator, run `mise run fixtures:gen`; with an unchanged generator, `git status` stays clean.

## Golden hashes

`goldenCases` in `internal/core/golden_test.go` pins, per fixture and configuration, the Input fingerprint and the zip and SBOM SHA-256. CI runs the same table on Linux, macOS and Windows, so any host-dependent byte fails there.

Run them after touching fingerprinting, packaging, SBOM, the uv version or the Go version:

```sh
mise run test:golden
```

When a hash moves, decide why before touching it:

- **Unintended** (you changed a refactor, not the output): fix the code.
- **Intended** (a packaging rule changed, a fingerprint argument was added, uv or Go was upgraded and the bytes changed): copy the new values from the failure output, bump `packager.FormatVersion` if Artifact bytes from unchanged inputs changed, and add a changelog entry. See [releasing](releasing.md#changing-artifact-bytes).

Never paste new values only to make the test pass.

## Acceptance tests

`mise run test:acc` sets `TF_ACC=1` and points the plugin test framework at the mise-pinned Terraform. `test:tofu` runs the same tests against OpenTofu. They cover the lifecycle described in the [architecture](../architecture/README.md#plan-and-apply): unchanged plans, rebuild of missing Artifacts with matching hashes, `on_missing_artifact = "ignore"`, saved plans with changed sources, workspaces, and the corporate-network settings.

## Smoke tests

```sh
mise run test:smoke             # Python 3.12
mise run test:smoke --python 3.13 --keep
```

Needs Docker with emulation for the other architecture (Docker Desktop has it; on a bare Linux host register QEMU binfmt first, as `nightly.yml` does). The task cross-compiles the provider for Linux, applies `testdata/smoke/main.tf` in `hashicorp/terraform` with only the provider added, then unpacks each Artifact in `public.ecr.aws/lambda/python:<version>` and runs `testdata/smoke/check.py`.

## Caches and parallel runs

Tests that run uv share one `cache_dir`, by default `<user cache dir>/lambdabuild-test`, so the managed Python downloads once per machine. Set `LAMBDABUILD_TEST_CACHE_DIR` to move it; use a different directory per git worktree when running test suites in parallel worktrees.

## Debugging a build without Terraform

The harness CLI calls `core` directly and prints the result as JSON:

```sh
mise run build:cli
bin/lambdabuild-harness fingerprint -project-dir testdata/fixtures/pure-deps -output-dir /tmp/out -python-version 3.12
bin/lambdabuild-harness build       -project-dir testdata/fixtures/pure-deps -output-dir /tmp/out -python-version 3.12 -architecture arm64
bin/lambdabuild-harness build -h    # every flag; they mirror the resource arguments
```

To compare two builds byte by byte, unzip both and diff the trees, or list entries with `zipinfo -v`.

## Trying the provider against mock AWS

[`dev/mock-aws/`](../../dev/mock-aws/README.md) plans and applies a real configuration (functions, a layer, SBOM uploads) using the provider from your checkout against [moto](https://docs.getmoto.org/) in Docker. No AWS account or credentials are needed.

```sh
mise run mock-aws:up
mise run mock-aws:apply
mise run mock-aws:down
```

The README there lists experiments: code changes, dependency bumps, fresh runners and saved plans.
