# Third-party licences

Release archives embed a pinned build of [uv](https://github.com/astral-sh/uv)
(see `internal/core/internal/uvbin/VERSION`). uv is dual-licensed under the MIT
and Apache-2.0 licences; both texts are shipped here (`uv/LICENSE-MIT`,
`uv/LICENSE-APACHE`), copied from the uv tag matching `VERSION`. When bumping
uv, refresh them (`mise run uv:licenses`).

The provider itself is licensed under MPL-2.0 (`LICENSE` at the archive root).
