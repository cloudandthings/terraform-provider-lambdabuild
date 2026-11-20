# lambdabuild documentation

lambdabuild is a Terraform provider that builds deterministic AWS Lambda zips from uv-managed Python projects. Pick the section for what you are doing.

## Using the provider

For anyone writing Terraform with lambdabuild. These pages are published on the Terraform Registry; the Markdown here is generated from `templates/` and `examples/`.

| Page | Read it to |
| --- | --- |
| [Getting started](guides/getting-started.md) | Build and deploy a first function and layer. |
| [Provider configuration](index.md) | Set the cache directory, package indexes, proxies and mirrors. |
| [`lambdabuild_python_package`](resources/python_package.md) | Look up every argument and attribute, with examples. |
| [CI/CD pipelines](guides/ci-cd.md) | Run on ephemeral runners: saved plans, missing Artifacts, caching, drift detection. |
| [Private indexes and restricted networks](guides/private-networks.md) | Use private indexes, credentials, proxies, custom CAs, offline builds. |
| [Reproducible builds](guides/reproducible-builds.md) | Understand the Input fingerprint and one-time redeploys. |
| [Troubleshooting](guides/troubleshooting.md) | Fix a specific error or warning. |

## Understanding the design

For architects and reviewers evaluating how it works and why.

| Page | Covers |
| --- | --- |
| [Architecture](architecture/README.md) | Goals, components, plan and apply flow, fingerprint, build pipeline, packaging rules, isolation, security model, concurrency. |
| [Decision records](adr/README.md) | The significant decisions, with the options that were rejected. |
| [GLOSSARY.md](../GLOSSARY.md) | The domain terms used everywhere: Artifact, Input fingerprint, Packaging-format version, ... |
| [ROADMAP.md](../ROADMAP.md) | Status, the path to the first release, and what comes later. |

## Working on the code

For contributors.

| Page | Covers |
| --- | --- |
| [Developing lambdabuild](development/README.md) | Setup, repository layout, tasks, CI, how to make a change. |
| [Testing](development/testing.md) | Test layers, fixtures, golden hashes, smoke tests, debugging a build. |
| [Releasing and upgrades](development/releasing.md) | The release process, packaging-format bumps, upgrading uv and Go, new Python runtimes. |
| [CHANGELOG.md](../CHANGELOG.md) | What changed in each release, and the changelog policy. |
