# Changelog

## 0.1.0 (2026-11-21)


### Features

* **ci:** skip the CI matrix on prose-only PRs, add ci-ok gate ([#5](https://github.com/cloudandthings/terraform-provider-lambdabuild/issues/5)) ([d2cf763](https://github.com/cloudandthings/terraform-provider-lambdabuild/commit/d2cf763231b0de072887bf96a460a792ee379b2c))


### Bug Fixes

* **ci:** make the Nightly container smoke job pass ([#4](https://github.com/cloudandthings/terraform-provider-lambdabuild/issues/4)) ([df30486](https://github.com/cloudandthings/terraform-provider-lambdabuild/commit/df30486d6e077018c122356e2560971131a87170))
* make the Windows CI job pass ([#1](https://github.com/cloudandthings/terraform-provider-lambdabuild/issues/1)) ([3345a2f](https://github.com/cloudandthings/terraform-provider-lambdabuild/commit/3345a2f3faf8f71c15519b77aa2580cb8230d1ac))

## Changelog

All notable changes are recorded here, newest first. Versions follow semantic versioning.

## Changelog policy

- **This file is generated.** release-please writes each release's section from the Conventional Commit PR titles merged since the previous release (PRs are squash-merged, so the title is the commit). Don't edit it by hand: fix the wording in the PR title before merging.
- **Packaging-format changes are flagged as one-time redeploys.** Any change that alters the bytes of an Artifact built from unchanged inputs (zip layout, timestamps, file ordering, cleaning rules, SBOM format, the embedded uv version when it changes output) bumps `packager.FormatVersion` (see ADR 0003). Because the Input fingerprint includes the format version, every Artifact gets a new `output_path` on the next plan and Lambda functions and layers redeploy once; no action is needed beyond applying. Such PRs are titled `redeploy(<scope>): …` and appear under **One-time redeploy**.
- Releases with no One-time redeploy entry do not alter any existing Artifact.
- Breaking schema changes (renamed or removed arguments, changed defaults, dropped Python runtimes) are titled with `!` (`feat(provider)!: …`) and appear under **Breaking changes**.
