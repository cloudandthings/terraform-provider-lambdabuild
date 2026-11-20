# Releasing and upgrades

How versions are cut and published, and the procedures for changes that can move Artifact bytes: packaging changes, uv upgrades, Go upgrades and new Python runtimes.

## Versioning

The version lives in the root [`VERSION`](../../VERSION) file and follows semantic versioning. Tags are `vX.Y.Z`. Nothing is tagged or published by hand.

| Change | Bump |
| --- | --- |
| Breaking schema change (renamed or removed argument, changed default) | major (minor while `0.x`) |
| New argument, resource or feature | minor |
| Fix, new Lambda Python runtime, uv upgrade | patch |

A **One-time redeploy** (a Packaging-format version bump) is not breaking, but it is called out in the changelog so users expect every function to update once.

## Release process

1. **Check coverage.** Every feat and fix since the last tag has a `CHANGELOG.md` entry under Unreleased and matching docs. The `/release-patch`, `/release-minor` and `/release-major` agent commands do this check first.
2. **Prepare.** `mise run release:prepare <patch|minor|major>` requires a clean tree, creates `release/vX.Y.Z`, bumps `VERSION`, moves the Unreleased entries under a dated version heading, commits `chore: release X.Y.Z` and pushes.
3. **Open the PR** to `main`. CI builds a GoReleaser snapshot and checks the archives (`release:snapshot`, `release:verify`) on every PR.
4. **Merge.** The push to `main` that changes `VERSION` runs `.github/workflows/release.yml`: the container smoke tests as a gate, then `mise run release:publish`, which tags `vX.Y.Z` and runs GoReleaser to build, GPG-sign and publish the GitHub Release. The Terraform Registry picks it up from there.

A failed publish can be retried with **Run workflow** on `release.yml`; `release:publish` skips a release that is already published and replaces a leftover draft.

The workflow needs the repository secrets `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE`, with the public key registered on the Registry.

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
3. Add a **One-time redeploy** section to the Unreleased changelog entry: what changed, which projects see different bytes, and that no action is needed beyond applying.

Changes that need this: zip layout, timestamps, ordering, compression, permission rules, the cleaning rules, the SBOM format, and a uv or Go upgrade that changes output.

A change to the fingerprint stream or to an argument's JSON name also changes every fingerprint, and is handled the same way.

## Upgrading uv

The uv version stays out of the Input fingerprint ([ADR 0003](../adr/0003-uv-version-not-in-fingerprint.md)), so golden tests are the gate.

1. Set the new version in `internal/core/internal/uvbin/VERSION` **and** `uv` in `.mise/config.toml`; they must match.
2. `mise run uv:fetch --update-checksums` copies Astral's published archive checksums into `uvbin/checksums.txt` and downloads your host's binary. Commit `checksums.txt`.
3. `mise run test:golden`, and let CI run it on all three operating systems.
   - Hashes unchanged: a patch release with a "Changed: uv X.Y.Z" entry.
   - Hashes changed: follow [Changing Artifact bytes](#changing-artifact-bytes).
4. Run `mise run test:smoke` and `mise run test:network`, or wait for the nightly run.

## Upgrading Go

Go's `compress/flate` produces the zip's compressed bytes, so a Go upgrade can change Artifacts. Update `go` in `.mise/config.toml` and `go.mod`, then run the golden tests as for uv.

## Adding a Lambda Python runtime

When AWS releases a Python runtime:

1. Add it to `SupportedPythonVersions` in `internal/core/plan.go`. Check `defaultPlatformTag` still matches the runtime's Amazon Linux generation.
2. Add golden cases if the runtime needs its own coverage, and run the smoke tests with `--python <version>`.
3. Regenerate the docs (`mise run docs:generate`; the schema description and validator read the list), update the version range in `README.md` and the guides, and add a changelog entry. This ships as a patch release.

When AWS deprecates a runtime, remove it the same way. That is a breaking change for users still on it and goes under **Breaking changes**.
