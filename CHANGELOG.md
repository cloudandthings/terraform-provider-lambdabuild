# Changelog

All notable changes are recorded here, newest first. Versions follow semantic versioning.

## Changelog policy

- **This file is generated.** release-please writes each release's section from the Conventional Commit PR titles merged since the previous release (PRs are squash-merged, so the title is the commit). Don't edit it by hand: fix the wording in the PR title before merging.
- **Packaging-format changes are flagged as one-time redeploys.** Any change that alters the bytes of an Artifact built from unchanged inputs (zip layout, timestamps, file ordering, cleaning rules, SBOM format, the embedded uv version when it changes output) bumps `packager.FormatVersion` (see ADR 0003). Because the Input fingerprint includes the format version, every Artifact gets a new `output_path` on the next plan and Lambda functions and layers redeploy once; no action is needed beyond applying. Such PRs are titled `redeploy(<scope>): …` and appear under **One-time redeploy**.
- Releases with no One-time redeploy entry do not alter any existing Artifact.
- Breaking schema changes (renamed or removed arguments, changed defaults, dropped Python runtimes) are titled with `!` (`feat(provider)!: …`) and appear under **Breaking changes**.
