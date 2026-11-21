# Developing lambdabuild

Everything you need to build, test and change the provider. Read the [architecture overview](../architecture/README.md) first if you are new to the codebase.

- [Testing](testing.md): the test layers, fixtures, golden hashes, smoke tests and debugging.
- [Releasing and upgrades](releasing.md): cutting a release, upgrading uv or Go, adding a Python runtime.

## Setup

All tooling is pinned in [`.mise/config.toml`](../../.mise/config.toml) and run through [mise](https://mise.jdx.dev): Go, Terraform, OpenTofu, GoReleaser, golangci-lint, tfplugindocs, and a host uv used only for fixture maintenance.

```sh
mise install       # install the pinned tools
mise run ci        # lint, unit, acceptance and docs checks, as CI runs them
mise tasks         # list every task
```

The provider embeds uv binaries that are not in git. Tasks that compile Go depend on `uv:fetch`, which downloads the pinned uv for your host into `internal/core/internal/uvbin/assets/` the first time. Plain `go build` or `go test` before that fails to embed; run `mise run uv:fetch` once.

The first test run downloads managed Pythons into a shared test cache. Later runs are offline for everything except the `network` tests.

## Repository layout

| Path | Contents |
| --- | --- |
| `main.go` | Plugin entry point. |
| `internal/provider/` | Provider and `lambdabuild_python_package` resource (test seam A). |
| `internal/core/` | `Fingerprint` and `Build`, the core build interface (test seam B). |
| `internal/core/internal/` | Fingerprinter, packager, toolchain, Python builder, SBOM, artifact store. Reachable only through `core`. |
| `internal/core/internal/uvbin/` | Embedded uv: `VERSION`, `checksums.txt`, per-platform `embed_*.go`. |
| `internal/filehash/` | Shared file hashing. |
| `cmd/lambdabuild-harness/` | CLI driving `core` without Terraform. |
| `testdata/fixtures/` | Hermetic uv projects with fake Packages and local indexes. |
| `testdata/smoke/` | Configuration and checks for the container smoke tests. |
| `dev/mock-aws/` | Plan and apply against a local mock AWS ([README](../../dev/mock-aws/README.md)). |
| `examples/`, `templates/` | Sources of the Registry docs. |
| `docs/` | Generated Registry docs (`index.md`, `resources/`, `guides/`) plus these hand-written docs. |
| `.mise/tasks/` | Every task, as a bash script. |
| `licenses/` | Third-party licences shipped in release archives. |

## Tasks

| Task | What it does |
| --- | --- |
| `ci` | Everything CI runs, with `CI=true`: `lint:go`, `test:unit`, `test:acc`, `docs:validate`. |
| `lint:go` | golangci-lint. |
| `test:unit` | Go unit tests and seam B (`core`) tests. |
| `test:acc` | Terraform acceptance tests (seam A). |
| `test:tofu` | The acceptance tests against OpenTofu. |
| `test:golden` | Golden hashes of every fixture's fingerprint, zip and SBOM. |
| `test:network` | Tests that download real wheels (numpy) from PyPI. |
| `test:smoke` | Builds in a clean Terraform container, imports in the Lambda Python images. Needs Docker. |
| `docs:generate` | Regenerate Registry docs from schema, `templates/`, `examples/`. |
| `docs:validate` | tfplugindocs validation, and fail if generated docs are stale. |
| `fixtures:gen` | Regenerate the fake Packages in `testdata/fixtures/`. |
| `uv:fetch` | Download and verify the pinned uv; `--all` for every platform. |
| `build:cli` | Build `bin/lambdabuild-harness`. |
| `mock-aws:*` | Mock AWS playground: `up`, `plan`, `apply`, `tf`, `destroy`, `down`. |
| `release:*` | `snapshot`, `verify`; CI only: `publish`, `force-pr`, `dispatch-ci`. |

Tasks are bash scripts in `.mise/tasks/<group>/<name>` (run as `mise run <group>:<name>`). Each starts with `#!/usr/bin/env bash`, a `#MISE description="..."` line and `set -euo pipefail`, and is executable. CI workflows call tasks and contain no logic of their own.

## Continuous integration

| Workflow | When | Runs |
| --- | --- | --- |
| `ci.yml` | Every PR and push to `main`; dispatched by `release.yml` on the release PR | PR title lint (PRs only); `mise run ci` on Ubuntu, macOS and Windows; release snapshot build and verify. PRs that touch only prose skip the last two (see below). |
| `nightly.yml` | Daily | `test:smoke` (x86_64 and arm64) and `test:network`. |
| `release.yml` | Every push to `main`, or manual run with an optional `tag` | release-please (release PR, tag and draft release); on a release, smoke gate then `release:publish`. See [releasing](releasing.md). |

The golden tests in `ci` run on all three operating systems against the same pinned hashes. That is how cross-host reproducibility is enforced.

`ci-ok` is the only required status check on `main`. It passes when every `ci.yml` job passed or was skipped, including `pr title`, which checks the PR title is a Conventional Commit. On a PR, the `changes` job skips the matrix and the release snapshot when every changed file is on its denylist of prose paths (`README.md`, `ROADMAP.md`, `GLOSSARY.md`, `CLAUDE.md`, `docs/README.md`, `docs/adr/`, `docs/architecture/`, `docs/development/`, `.agents/`, `.claude/`). Any other path runs everything, and pushes to `main` always run everything. Add a path to the denylist only if no task, test, embed or release archive reads it; generated docs, `templates/`, `examples/`, `CHANGELOG.md` and `licenses/` stay out of it.

## Making a change

1. **Branch** off `main`: `feat/…`, `fix/…`, `docs/…`. Open a PR back to `main`.
2. **Read the ADRs** in [`docs/adr/`](../adr/) that touch your area. If your change contradicts one, say so in the PR and update or supersede the ADR.
3. **Use the glossary.** Code, tests, commits and docs use the terms in [GLOSSARY.md](../../GLOSSARY.md) (Artifact, Input fingerprint, Packaging-format version, uv project, Workspace member, Package), not the synonyms it lists.
4. **Test first for bugs.** Reproduce the failure through `core` or the resource, then fix it. Prefer hermetic fixtures; tests that need the network carry the `network` build tag.
5. **Commit** with Conventional Commits and a scope: `fix(toolchain): …`, `test(provider): …`, `docs(readme): …`. One logical change per commit. The body quotes the exact error you saw and explains why the fix works.
6. **PR title = changelog entry.** PRs are squash-merged and release-please builds [CHANGELOG.md](../../CHANGELOG.md) and the version from the titles, so write the title for users: `feat`, `fix`, `perf`, `redeploy` (Packaging-format version bump) or `!` for breaking. `ci-ok` rejects titles that aren't Conventional Commits. Don't edit `CHANGELOG.md` by hand. See [releasing](releasing.md#versioning).
7. **Docs.** Update schema descriptions, `templates/` and `examples/` in the same branch, then `mise run docs:generate`. Schema descriptions appear on the Registry page, so write them for users.
8. **Done** means `mise run ci` is green.

### Rules that protect reproducibility

- Output must not depend on the host, the clock or the environment. Any change that could is a bug.
- A change that alters Artifact bytes from unchanged inputs bumps `packager.FormatVersion` and ships under a `redeploy(<scope>): …` PR title. See [releasing](releasing.md#changing-artifact-bytes).
- Golden hash changes are decisions, never test fixes. Do not paste new values to make a test pass without understanding why they moved.
- Keep new inputs inside the isolation model: user `UV_*` variables stay ignored, credentials never become provider or resource arguments, uv errors stay redacted.
- Windows: watch path quoting, file locks during rename, `MAX_PATH` and junctions. Put platform code in `_windows.go`, `_unix.go` or `_other.go` files.
- Shared state in `cache_dir` or `output_dir` needs a lock or an atomic rename: concurrent builds share them.

## Documentation

| What | Where to edit |
| --- | --- |
| Registry provider page | `templates/index.md.tmpl`, provider schema descriptions |
| Registry resource page | `templates/resources/python_package.md.tmpl`, resource schema descriptions, `examples/resources/` |
| Registry guides | `templates/guides/*.md.tmpl` |
| Architecture, development docs | `docs/architecture/`, `docs/development/` (hand-written) |
| Decisions | `docs/adr/` (one file per decision, numbered) |

`docs/index.md`, `docs/resources/` and `docs/guides/` are generated: never edit them by hand. Registry pages are rendered on registry.terraform.io, so link from them to repository files with absolute GitHub URLs.
