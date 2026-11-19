//go:build windows && amd64

package uvbin

import _ "embed"

//go:embed assets/uv-windows-amd64.gz
var compressed []byte

//go:embed assets/uv-windows-amd64.sha256
var sha256Hex string

const exe = "uv.exe"
