# lambdabuild

A Terraform provider that builds reproducible AWS Lambda deployment zips from source projects, so Terraform can deploy them without host tooling.

## Artifacts

**Artifact**:
The zip file a lambdabuild resource produces, ready for Lambda to consume, together with its SBOM when one is enabled.
_Avoid_: package, bundle, archive, build output

**SBOM**:
The machine-readable inventory of every Package shipped inside an Artifact, stored beside its zip.
_Avoid_: manifest, bill of materials file

**Artifact type**:
Whether an Artifact is a `function` zip (content at the root) or a `layer` zip (content under `python/`).
_Avoid_: package type, kind

**Input fingerprint**:
A hash over everything that determines an Artifact's bytes; equal fingerprints promise byte-identical Artifacts.
_Avoid_: build hash, input hash, cache key

**Packaging-format version**:
A number that changes whenever the rules turning staged files into Artifact bytes change; it feeds the Input fingerprint.
_Avoid_: zip version, format revision

## Python projects

**uv project**:
A directory with a `pyproject.toml` and a committed `uv.lock`; the only source lambdabuild builds Python Artifacts from.
_Avoid_: requirements project, app

**Workspace member**:
One project inside a uv workspace, chosen as the thing to build.
_Avoid_: package, sub-project

**Package**:
A third-party Python distribution installed into an Artifact, such as `boto3`.
_Avoid_: library, dependency (when meaning one specific distribution)
