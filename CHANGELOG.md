# Changelog

All notable changes are recorded here, newest first. Versions follow semantic versioning.

## Changelog policy

- **Packaging-format changes are flagged as one-time redeploys.** Any change that alters the bytes of an Artifact built from unchanged inputs (zip layout, timestamps, file ordering, cleaning rules, SBOM format, the embedded uv version when it changes output) bumps `packager.FormatVersion` (see ADR 0003). Because the Input fingerprint includes the format version, every Artifact gets a new `output_path` on the next plan and Lambda functions and layers redeploy once. Such releases carry a **"One-time redeploy"** heading in their entry; the entry says what changed and that no action is needed beyond applying.
- Releases that do not change `FormatVersion` do not alter any existing Artifact; their entries need no redeploy note.
- Breaking schema changes (renamed or removed arguments, changed defaults) are listed under **Breaking changes**.
- Entries are written by hand when a change merges, not generated from commits.

## Unreleased

### One-time redeploy

Packaging-format version 2. Every Artifact gets a new `output_path` on the next plan and Lambda functions and layers redeploy once; no action is needed beyond applying. For most projects the zip bytes are unchanged; they change for projects affected by these rules:

- Wheels of the project and its local dependencies are built from a per-build copy of their fingerprinted files (the tree minus `source_exclude` and `.gitignore`d paths), so files outside the Input fingerprint, such as package data in a folder named like a default exclude (`build`, `tests`, ...), no longer ship.
- `[tool.uv] default-groups` no longer installs groups: only the groups listed in `groups` ship.
- The project's `[tool.uv.pip]` settings no longer apply to the dependency install (only its `[[tool.uv.index]]` entries do).
- `.gitignore` files above the project, up to the git repository root, now apply to the fingerprint and to what the wheel build sees.
- `exclude_packages` names are compared PEP 503-normalised in the fingerprint.

### Breaking changes

- `python_version = "3.10"` is no longer accepted: AWS deprecated the python3.10 runtime on 2026-10-31. Supported: 3.11, 3.12, 3.13, 3.14, checked at `terraform validate`.
- `index_url`, `extra_index_urls` and `python_install_mirror` reject credentials in the URL. Name the index (`index_url = "corp=https://..."` or `[[tool.uv.index]] name = "corp"`) and set `UV_INDEX_CORP_USERNAME` / `UV_INDEX_CORP_PASSWORD` in the environment.
- A uv workspace root without a `[project]` table requires `workspace_member`.

### Changed

- Changing `on_missing_artifact` alone never produces a diff, so drift-detection plans with `"ignore"` set through a variable are empty.
- Apply checks the Input fingerprint before building, and the zip and SBOM are written as one unit: a failed apply leaves no stray Artifact files.
- The CRLF warning skips binary files.

### Added

- `lambdabuild_python_package` resource: deterministic Lambda zip Artifacts (function or layer) from uv-managed Python projects, with an embedded, pinned uv.
- Release archives for darwin (amd64, arm64), linux (amd64, arm64) and windows (amd64), including uv's MIT and Apache-2.0 licence texts.
