//go:build !windows

package artifact

import "os"

// rename replaces newpath with oldpath atomically.
func rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
