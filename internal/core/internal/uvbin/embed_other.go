//go:build !((darwin && (arm64 || amd64)) || (linux && (amd64 || arm64)) || (windows && amd64))

package uvbin

// No uv is embedded for this platform; an external uv is required.
var (
	compressed []byte
	sha256Hex  string
)

const exe = "uv"
