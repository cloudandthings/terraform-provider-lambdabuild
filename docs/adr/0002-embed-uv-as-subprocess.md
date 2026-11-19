# Embed uv in the provider binary and run it as a subprocess

The provider embeds a pinned, gzip-compressed uv binary per host platform, extracts it into `cache_dir` on first use, and runs it as a subprocess; uv then fetches a managed Python. We chose this so the host needs nothing but Terraform, every host runs the same toolchain version, and the closest prior art's requirement for Python on the runner disappears. uv is a Rust executable, not a Go library, so embedding the bytes is the only way to ship it inside a Go provider.

## Consequences

- Each provider binary grows by tens of megabytes, mitigated by gzip and Terraform's plugin cache.
- A uv fix requires a provider release; `uv_path` is the escape hatch for approved or newer uv builds.
- Extraction needs a directory that allows executing binaries, so `cache_dir` defaults to the user cache directory, not `/tmp`.
- Release archives must carry uv's MIT and Apache-2.0 licence texts.

## Considered Options

- **Require uv or Python on the host**: rejected; host variance is the core problem lambdabuild exists to solve.
- **Download uv at runtime**: rejected; adds a network dependency and a supply-chain step outside the Registry's signed release.
