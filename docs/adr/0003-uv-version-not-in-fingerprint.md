# Leave the uv version out of the Input fingerprint

The Input fingerprint does not include the uv version. Including it would change every `output_path` on each uv upgrade (a routine minor release) and redeploy every Lambda even when Artifact bytes are identical, and would give `uv_path` users different fingerprints from embedded-uv users. Instead, golden-hash tests gate every uv upgrade; if output bytes change, the release bumps the Packaging-format version, which is in the fingerprint and triggers exactly one rebuild.

## Consequences

- `uv_version` is an after-build attribute, not known at plan.
- An external `uv_path` binary that produces different bytes surfaces as a non-reproducible build on missing-artifact rebuild, not as a silent redeploy.
