# lambdabuild

Terraform provider (`cloudandthings/lambdabuild`) that builds deterministic AWS Lambda zip Artifacts from uv projects. uv is embedded in the provider binary, so the runner needs only Terraform. Product promise: **same inputs, same bytes, on every host** (macOS, Linux, Windows). Treat any change that could make output depend on the host, the clock, or the environment as a bug.

Status and what's next: `ROADMAP.md`. Nothing released yet (`VERSION` is `0.0.0`).

## Layout

- `internal/provider/`: Terraform plugin-framework provider and the `lambdabuild_python_package` resource (test seam A).
- `internal/core/`: the core build interface (test seam B): `Fingerprint` (plan time, writes nothing) and `Build` (apply time). The fingerprinter, packager, toolchain, artifact store, etc. live under `internal/core/internal/` and are tested only through `core`.
- `internal/core/internal/uvbin/`: embedded uv. Pinned in its `VERSION` (must match `uv` in `.mise/config.toml`); binaries fetched by `mise run uv:fetch` into gitignored `assets/`.
- `cmd/lambdabuild-harness/`: CLI that drives `core` directly, for debugging builds without Terraform.
- `testdata/fixtures/`: hermetic uv projects with fake Packages (regenerate with `mise run fixtures:gen`). `testdata/smoke/`: container smoke test config.
- `dev/mock-aws/`: plan/apply the local provider against moto (`mise run mock-aws:*`).
- `docs/index.md`, `docs/resources/` and `docs/guides/` are **generated** by tfplugindocs from schema descriptions, `templates/` (including `templates/guides/`) and `examples/`. Edit those, then `mise run docs:generate`; `docs:validate` fails on drift.
- `docs/README.md` (index by audience), `docs/architecture/`, `docs/development/` and `docs/adr/` are hand-written human docs. Keep them current when behaviour, tasks or layout change.
- `.agents/`: configuration the agent skills read (issue tracker, triage labels, domain docs). Not user documentation.

## Domain and decisions

- Use `GLOSSARY.md` terms in code, tests, commits and docs (Artifact, Input fingerprint, Packaging-format version, uv project, Workspace member, Package). Avoid the synonyms it lists.
- Read the ADRs in `docs/adr/` touching your area before changing it. Key ones: build at apply, not plan (0001); uv runs as an embedded subprocess (0002); uv version stays out of the fingerprint (0003). If a change contradicts an ADR, say so explicitly.

## Reproducibility rules

- **Golden hashes** in `internal/core/golden_test.go` / `golden_sbom_test.go` pin every fixture's fingerprint, zip and SBOM SHA-256. Run `mise run test:golden` after touching fingerprinting, packaging, SBOM, uv or Go versions. A hash change is a decision, never a test to "fix" by pasting new values.
- Any change to Artifact bytes from unchanged inputs bumps `packager.FormatVersion` and ships with a `redeploy(<scope>): …` PR title, so the generated changelog lists it under **One-time redeploy** (policy at the top of `CHANGELOG.md`).
- Isolation is deliberate: user `UV_*` variables are ignored, credentials never go in provider arguments (they land in state), uv errors are redacted. Keep new inputs inside that model.
- Windows is a first-class target (CI matrix runs ubuntu, macos, windows). Watch path quoting, file locks/renames, `MAX_PATH`, and junctions; platform code goes in `_windows.go` / `_unix.go` / `_other.go` files.
- Concurrent builds may share one `cache_dir` and `output_dir`; shared state needs a lock or atomic rename. Use a worktree-unique `LAMBDABUILD_TEST_CACHE_DIR` when running tests in parallel worktrees.

## Way of work

- Branch off `main` (`feat/…`, `fix/…`, `docs/…`); PR back to `main`. Never tag or publish by hand.
- Commits: Conventional Commits with a scope (`fix(toolchain): …`, `test(provider): …`, `docs(changelog): …`). One logical change per commit. The body explains the observed failure (quote the exact error) and why the fix works.
- Write tests first for bugs: reproduce red through `core` or the resource, then fix. Prefer hermetic fixtures over network; network-only tests carry the `network` build tag.
- PRs are squash-merged; the PR title becomes the commit on `main` and release-please generates `CHANGELOG.md` and the version from it. Titles are Conventional Commits (CI's `pr title` job enforces it): `feat`/`fix`/`perf` for user-visible changes, `redeploy` for a FormatVersion bump, `!` for breaking; write them for users. Never edit `CHANGELOG.md` by hand. Every user-visible feat/fix updates the docs (schema descriptions, `templates/`, `examples/`) in the same branch.
- Schema descriptions are user docs: write them for the Registry page.

## Agent skills

### Issue tracker

Issues and specs are local markdown files under `.scratch/<feature>/`. `.scratch/` is gitignored: it lives only on the developer's machine, never in the repo. Planned work for others to see goes in `ROADMAP.md`. See `.agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary, recorded as a `Status:` line in each issue file. See `.agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `.agents/domain.md`.

## mise

mise is the single entry point for tooling, tasks and env vars, so a task runs the same locally and in CI. `mise tasks` lists them all.

- **Tasks are bash scripts** in `.mise/tasks/`, not inline TOML. Start each with `#!/usr/bin/env bash`, a `#MISE description="..."` line and `set -euo pipefail`; `chmod +x`.
- **Organise with folders**: `.mise/tasks/test/unit` is `mise run test:unit`.
- **Workflows call tasks** (`mise run ci`), they don't inline logic. CI installs tools with `jdx/mise-action`. If a workflow step is more than a one-liner, make it a task.
- **Env vars** go in `.mise/config.toml` `[env]` (or `#MISE env=` on one task) when they apply to the project.
- Everything CI runs, with `CI=true`: `mise run ci` (`lint:go`, `test:unit`, `test:acc`, `docs:validate`). Done = `mise run ci` green.
- Slow checks (`test:smoke`, `test:network`) run in `.github/workflows/nightly.yml` and gate releases, not in `ci`.

## Releasing

- release-please (`.github/workflows/release.yml`, `release-please-config.json`) keeps a `chore: release X.Y.Z` PR open on `main`, bumping `VERSION` and prepending `CHANGELOG.md`. CI on that PR is dispatched by `release.yml` (`release:dispatch-ci`), since GITHUB_TOKEN pushes don't trigger `pull_request` runs. `/release` reviews the open release PR (docs coverage, bump level).
- Merging it tags `vX.Y.Z` and creates a draft release; the smoke tests gate `mise run release:publish` (GoReleaser builds, GPG-signs, fills and publishes the draft). Signing uses org secrets `GPG_PRIVATE_KEY` and `PASSPHRASE`.
- Manual "Run workflow" with `tag`: a new version forces the release PR to that version (`release:force-pr`); an existing tag re-runs smoke + publish for it. Never tag or publish by hand.
- `mise run release:snapshot` + `release:verify` build and check the archives locally; CI runs them on every PR.
