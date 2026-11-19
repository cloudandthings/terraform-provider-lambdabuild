// Package uvbin carries the pinned uv binary for the host platform,
// gzip-compressed, with the SHA-256 of the decompressed binary.
//
// The assets are not committed: `mise run uv:fetch` downloads the release
// named in VERSION, checks it against checksums.txt (Astral's published
// archive checksums), and writes assets/uv-<goos>-<goarch>.gz plus its
// .sha256. One build-tagged file per host platform embeds them; other
// platforms compile without an embedded uv.
package uvbin

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var version string

// Version is the pinned uv release, e.g. "0.13.6".
var Version = strings.TrimSpace(version)

// Embedded is the uv binary for this host platform.
type Embedded struct {
	// Gzip is the gzip-compressed binary.
	Gzip []byte
	// SHA256 is the hex SHA-256 of the decompressed binary.
	SHA256 string
	// Exe is the file name to extract to: "uv" or "uv.exe".
	Exe string
}

// Binary returns the embedded uv, or ok false when this platform has none.
func Binary() (e Embedded, ok bool) {
	if len(compressed) == 0 {
		return Embedded{}, false
	}
	return Embedded{Gzip: compressed, SHA256: strings.TrimSpace(sha256Hex), Exe: exe}, true
}
