---
page_title: "Private indexes and restricted networks"
subcategory: ""
description: |-
  Build behind a corporate proxy, from private package indexes, with custom certificates, Python mirrors or no network at all.
---

# Private indexes and restricted networks

A build downloads two things: **Packages** from a package index (PyPI by default) and, once per Python version, a **managed Python** build from GitHub. This guide shows how to point both somewhere else, and how credentials stay out of Terraform state.

## The rule for credentials

Provider arguments are stored in state, in plan JSON and in saved plan files. So `index_url`, `extra_index_urls` and `python_install_mirror` **reject** URLs with credentials in them (`https://user:password@...`), and the error never repeats the value.

Instead, give the index a name and put its credentials in the environment of the process that runs Terraform:

```terraform
provider "lambdabuild" {
  index_url = "corp=https://pypi.corp.example/simple"
}
```

```sh
export UV_INDEX_CORP_USERNAME=svc-builder
export UV_INDEX_CORP_PASSWORD=...   # from your CI secret store
```

The variable names are `UV_INDEX_<NAME>_USERNAME` and `UV_INDEX_<NAME>_PASSWORD`, with the index name upper-cased and `-` replaced by `_`. Error messages from uv are redacted before they reach Terraform, so credential values do not appear in logs.

## Package indexes

| Need | Setting |
| --- | --- |
| Replace PyPI for every project | `index_url = "corp=https://pypi.corp.example/simple"` |
| Add indexes next to the default | `extra_index_urls = ["internal=https://pkgs.corp.example/simple"]` |
| An index that belongs to one project | `[[tool.uv.index]]` in that project's `pyproject.toml` |

Indexes declared in `pyproject.toml` work the same way, with the same credential variables:

```toml
[[tool.uv.index]]
name = "corp"
url = "https://pypi.corp.example/simple"
```

Packages are installed with the hashes recorded in `uv.lock`, so an index that serves different bytes for a locked version fails the build instead of shipping them.

## Proxies and certificates

These come from the environment, as for any other tool:

| Variable or argument | Effect |
| --- | --- |
| `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` | Route downloads through a proxy. |
| `SSL_CERT_FILE` | Trust the certificates in this PEM bundle, for a TLS-intercepting proxy or an internal CA. |
| `native_tls = true` (provider argument) | Trust the operating system's certificate store instead of the bundled one. |

## Managed Python mirror

uv downloads Python from the [python-build-standalone](https://github.com/astral-sh/python-build-standalone) releases on GitHub. If GitHub is blocked, mirror those releases internally and set:

```terraform
provider "lambdabuild" {
  python_install_mirror = "https://artifacts.corp.example/python-build-standalone"
}
```

## Offline and air-gapped builds

`offline = true` stops uv from using the network: every Package and Python must already be in the caches.

1. On a machine with network access, build every Artifact once with the same `cache_dir` and `uv_cache_dir` you will use offline.
2. Copy both directories to the offline runner, at the same paths.
3. Set `offline = true` and build.

The Artifacts are byte-identical to the online build: the caches never change the output.

## Other uv settings are ignored on purpose

lambdabuild ignores every other `UV_*` environment variable, your user and system `uv.toml`, and the project's `[tool.uv.pip]` settings. Any of these could change which Packages install or how, and then the same inputs would give different bytes on different machines. Only the per-index credential variables pass through, because they cannot change what gets installed.

## Your own uv binary

`uv_path` runs an external uv instead of the one embedded in the provider, for example a newer release your security team has approved. The embedded version is the one every golden test covers; another uv may produce different bytes, which shows up as a `Non-reproducible build` error when a missing Artifact is rebuilt on a runner with a different uv.
