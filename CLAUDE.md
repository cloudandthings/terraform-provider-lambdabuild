## Agent skills

### Issue tracker

Issues and specs are local markdown files under `.scratch/<feature>/`. `.scratch/` is gitignored: it lives only on the developer's machine, never in the repo. Planned work for others to see goes in `ROADMAP.md`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary, recorded as a `Status:` line in each issue file. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## mise

mise is the single entry point for tooling, tasks and env vars, so a task runs the same locally and in CI.

- **Tasks are bash scripts** in `.mise/tasks/`, not inline TOML. Start each with `#!/usr/bin/env bash`, a `#MISE description="..."` line and `set -euo pipefail`; `chmod +x`.
- **Organise with folders**: `.mise/tasks/test/unit` is `mise run test:unit`.
- **Workflows call tasks** (`mise run ci`), they don't inline logic. CI installs tools with `jdx/mise-action`. If a workflow step is more than a one-liner, make it a task.
- **Env vars** go in `.mise/config.toml` `[env]` (or `#MISE env=` on one task) when they apply to the project.
- Everything CI runs, with `CI=true`: `mise run ci` (`lint:go`, `test:unit`, `test:acc`, `docs:validate`). Done = `mise run ci` green.
- Slow checks (`test:smoke`, `test:network`) run in `.github/workflows/nightly.yml` and gate releases, not in `ci`.

## Releasing

- The version lives in the root `VERSION` file. `/release-patch`, `/release-minor` and `/release-major` first check `CHANGELOG.md` and the docs cover every feat/fix since the last tag, then run `mise run release:prepare <bump>` (branch `release/vX.Y.Z`, bump `VERSION`, move the changelog's Unreleased entries under the version, push) and open the PR.
- Merging that PR to `main` runs `.github/workflows/release.yml`: smoke gate, then `mise run release:publish` (CI-only: tags `vX.Y.Z`, GoReleaser builds, GPG-signs and publishes). CI needs secrets `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE`.
- `mise run release:snapshot` + `release:verify` build and check the archives locally; CI runs them on every PR.
