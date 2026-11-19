//go:build !unix && !windows

package toolchain

import "os"

// lockFile is a no-op where no file lock is available; extraction still
// writes a temp file and renames it, so readers never see a partial binary.
func lockFile(*os.File) error { return nil }
