//go:build darwin && arm64

package uvbin

import _ "embed"

//go:embed assets/uv-darwin-arm64.gz
var compressed []byte

//go:embed assets/uv-darwin-arm64.sha256
var sha256Hex string

const exe = "uv"
