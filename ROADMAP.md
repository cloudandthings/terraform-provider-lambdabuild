# Roadmap

lambdabuild builds deterministic AWS Lambda zip Artifacts from uv-managed Python projects, with uv embedded in the provider. Phases are sequenced, not dated: each gate is the evidence the next phase depends on. Terms follow [GLOSSARY.md](GLOSSARY.md); key decisions are in [docs/adr/](docs/adr/).

## Where we are

| Phase | Scope | Status |
| --- | --- | --- |
| 0 Spike | Go packager plus uv CLI on fixtures; uv config isolation; export/install with private indexes and local dependencies. | Done, except the Windows leg of the cross-host hash check. |
| 1 Core | Fingerprinter, toolchain manager, Python builder, zip packager with SBOM, artifact store, CLI harness. | Done |
| 2 Provider | `lambdabuild_python_package` resource, lifecycle (missing Artifact, saved plans), acceptance tests. | Done |
| 3 Hardening and release | Corporate and air-gapped networks, docs, GoReleaser, nightly container smoke tests. | Done, apart from publishing (below). |

`VERSION` is `0.0.0`: nothing is released yet.

## Next: first release (v0.1.0)

1. **Cross-host reproducibility (Gate 0).** Same SHA-256 for every fixture's zip on macOS arm64, Linux x86_64 and Windows x86_64. macOS and Linux match; Windows still needs a run, including a check of `MAX_PATH` limits in deep staging directories and of uv's own install output (`RECORD`, `INSTALLER`, `direct_url.json`).
2. **Publish to the Terraform Registry** as `cloudandthings/lambdabuild`.
   - Repository public, with CI running the OS-matrix golden-hash comparison.
   - Container smoke tests running nightly and before each release.
   - GPG signing key created, registered with the Registry, and stored as `GPG_PRIVATE_KEY` / `GPG_PASSPHRASE` secrets.
   - First tag released and installable through `required_providers`.
3. **End-to-end deploy to an AWS sandbox account.** Function and layer deployed and invoked, including numpy on arm64, from a clean container with only Terraform installed; scheduled nightly. Not yet triaged; may follow the first release.

## Later

Roughly in priority order; each is opt-in or additive unless noted.

- **Docker builder** for Python packages that ship only source distributions with native extensions. Needs Docker on the host, so opt-in.
- **Opt-in `build_at_plan`**, so `output_base64sha256` is known at plan time; apply rebuilds a missing zip and requires the same hash ([ADR 0001](docs/adr/0001-build-at-apply-not-plan.md)).
- **SPDX SBOM** output alongside CycloneDX.
- **`python_path` override** for networks with no Python mirror. Deferred because it breaks "every host behaves the same".
- **Shared artifact cache** keyed by Input fingerprint, so ephemeral runners download instead of rebuilding. Calls AWS, so opt-in.
- **Toolchain data source** exposing the uv and Python versions in use.
- **More runtimes** (see below).

## More runtimes

Each runtime is a new builder behind its own resource, such as `lambdabuild_node_package`. Fingerprinting, packaging, hashing and lifecycle logic stay shared; a builder only lists its inputs and stages files.

```go
type Builder interface {
    // Inputs lists the files and settings that feed the fingerprint.
    Inputs(ctx context.Context, cfg Config) (InputSet, error)
    // Stage installs dependencies and source into stagingDir.
    Stage(ctx context.Context, cfg Config, tc Toolchain, stagingDir string) error
}
```

| Runtime | Package manager | Toolchain approach | Main challenge |
| --- | --- | --- | --- |
| Python (v1) | uv | Embedded uv; uv-managed Python | Packages without Linux wheels |
| Node.js | npm, pnpm or Bun | Managed Node runtime, as with Python; or embed Bun, a single binary like uv | Native modules need binaries for the Lambda platform |
| Node.js bundling | esbuild | esbuild is written in Go and can be linked in as a library | Still needs dependencies installed first |
| Go or Rust (custom runtime) | `go build`, cargo | Cross-compilation; large toolchains | Lower priority |
| Any runtime (fallback) | Docker | AWS SAM build images | Needs Docker on the host, so opt-in only |

## Out of scope

Uploading to S3 or calling AWS APIs (except the opt-in cache above), container-image Lambdas, resolving or locking dependencies (`uv.lock` is the source of truth), projects without `uv.lock`, and secrets in HCL.
