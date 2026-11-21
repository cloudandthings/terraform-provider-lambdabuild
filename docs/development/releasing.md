# Releasing and upgrades

How versions are cut and published, and the procedures for changes that can move Artifact bytes: packaging changes, uv upgrades, Go upgrades and new Python runtimes.

## Versioning

Versions follow semantic versioning; tags are `vX.Y.Z`. [release-please](https://github.com/googleapis/release-please) owns the version (root [`VERSION`](../../VERSION) and `.release-please-manifest.json`) and [`CHANGELOG.md`](../../CHANGELOG.md), both derived from the Conventional Commit PR titles merged to `main` (PRs are squash-merged, so the title is the commit). Nothing is tagged, versioned or published by hand.

| PR title | Changelog section | Bump while `0.x` | Bump from `1.0` |
| --- | --- | --- | --- |
| `feat(scope)!: …` or a `BREAKING CHANGE:` footer (renamed or removed argument, changed default, dropped runtime) | Breaking changes | minor | major |
| `redeploy(scope): …` (Packaging-format version bump) | One-time redeploy | patch | patch |
| `feat(scope): …` | Features | minor | minor |
| `fix(scope): …`, `perf(scope): …` | Bug Fixes, Performance | patch | patch |
| `docs`, `chore`, `ci`, `build`, `test`, `refactor`, `style` | hidden | none on its own | none on its own |

`ci.yml`'s `pr title` job rejects any other type; `ci-ok` requires it. The allowed types and `changelog-sections` in `release-please-config.json` must stay in sync.

A **One-time redeploy** (a Packaging-format version bump) is not breaking, but release-please would only bump the patch for it. Release it as at least a minor: run the release workflow with the higher tag (below).

## Release process

1. **Release PR.** Every push to `main` runs `.github/workflows/release.yml`, where release-please opens or updates the PR `chore: release X.Y.Z` on branch `release-please--branches--main`. It bumps `VERSION` and the manifest and prepends the generated section to `CHANGELOG.md`.
2. **CI on the release PR.** release-please pushes with `GITHUB_TOKEN`, and GitHub does not start `pull_request` runs for that. `release.yml` therefore runs `mise run release:dispatch-ci`, which dispatches `ci.yml` on the release branch whenever its head commit has no CI run yet, so `ci-ok` appears on the PR without anyone touching it.
3. **Check coverage.** The `/release` agent command reviews the open release PR: every feat and fix documented in `docs/` and `examples/`, generated docs current, breaking and redeploy entries correct, and the bump high enough.
4. **Merge.** release-please tags `vX.Y.Z` on the merge commit and creates a **draft** GitHub Release with the changelog section as notes. The container smoke tests run against the tag (`nightly.yml` called with `ref`). Then `mise run release:publish vX.Y.Z` runs GoReleaser, which builds, GPG-signs the checksums, uploads into the draft and publishes it. The Terraform Registry picks up the published release. A failed gate leaves only the unpublished draft and the tag.

### Manual runs

**Run workflow** on `release.yml` (branch `main`) takes an optional `tag`:

- **New version** (`tag` does not exist yet, e.g. `v0.2.0`): `mise run release:force-pr` runs the release-please CLI with `--release-as`, so the release PR proposes exactly that version. Use it for a higher bump than the commits imply, such as a one-time redeploy. Merging the PR releases as usual.
- **Existing tag**: skip release-please, run the smoke gate on that tag and `release:publish`. This retries a failed publish. It does nothing if the release is already published, fills the draft if there is one, and creates the release from the tag's `CHANGELOG.md` section if the draft was deleted.
- **No tag**: same as a push to `main`.

`gh workflow run release.yml -f tag=v0.2.0` does the same from a terminal.

### Setup

- Organisation secrets `GPG_PRIVATE_KEY` and `PASSPHRASE`, imported by `crazy-max/ghaction-import-gpg`. The matching public key must be registered on the Terraform Registry for the `cloudandthings` namespace.
- Repository setting **Actions > General > Allow GitHub Actions to create and approve pull requests** must be on, or release-please cannot open its PR.

### Release archives

GoReleaser (`.goreleaser.yml`) builds `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64` and `windows/amd64` with `CGO_ENABLED=0` and `-trimpath`. Its before-hook runs `mise run uv:fetch --all`, so each binary embeds the uv for its platform. Archives carry `LICENSE` and uv's MIT and Apache-2.0 licences from `licenses/uv/`.

To build and inspect archives locally:

```sh
mise run release:snapshot
mise run release:verify
```

## Changing Artifact bytes

Any change that makes **unchanged inputs** produce **different Artifact bytes** must:

1. Bump `FormatVersion` in `internal/core/internal/packager/packager.go`. It is part of every Input fingerprint, so every user gets a new `output_path` and exactly one redeploy, instead of a silent hash mismatch.
2. Update the golden hashes (`mise run test:golden`), stating in the commit why they moved.
3. Title the PR `redeploy(<scope>): <what changed>` so release-please lists it under **One-time redeploy**. Put which projects see different bytes, and that no action is needed beyond applying, in the PR body. Release it as at least a minor (see [Versioning](#versioning)).

Changes that need this: zip layout, timestamps, ordering, compression, permission rules, the cleaning rules, the SBOM format, and a uv or Go upgrade that changes output.

A change to the fingerprint stream or to an argument's JSON name also changes every fingerprint, and is handled the same way.

## Upgrading uv

The uv version stays out of the Input fingerprint ([ADR 0003](../adr/0003-uv-version-not-in-fingerprint.md)), so golden tests are the gate.

1. Set the new version in `internal/core/internal/uvbin/VERSION` **and** `uv` in `.mise/config.toml`; they must match.
2. `mise run uv:fetch --update-checksums` copies Astral's published archive checksums into `uvbin/checksums.txt` and downloads your host's binary. Commit `checksums.txt`.
3. `mise run test:golden`, and let CI run it on all three operating systems.
   - Hashes unchanged: a `fix(uvbin): upgrade uv to X.Y.Z` PR, released as a patch.
   - Hashes changed: follow [Changing Artifact bytes](#changing-artifact-bytes).
4. Run `mise run test:smoke` and `mise run test:network`, or wait for the nightly run.

## Upgrading Go

Go's `compress/flate` produces the zip's compressed bytes, so a Go upgrade can change Artifacts. Update `go` in `.mise/config.toml` and `go.mod`, then run the golden tests as for uv.

## Adding a Lambda Python runtime

When AWS releases a Python runtime:

1. Add it to `SupportedPythonVersions` in `internal/core/plan.go`. Check `defaultPlatformTag` still matches the runtime's Amazon Linux generation.
2. Add golden cases if the runtime needs its own coverage, and run the smoke tests with `--python <version>`.
3. Regenerate the docs (`mise run docs:generate`; the schema description and validator read the list), and update the version range in `README.md` and the guides. Title the PR `fix(core): …` so it ships as a patch release.

When AWS deprecates a runtime, remove it the same way. That is a breaking change for users still on it: title the PR with `!` (`feat(core)!: …`) so it lands under **Breaking changes**.
