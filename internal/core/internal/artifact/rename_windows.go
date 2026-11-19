//go:build windows

package artifact

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// rename replaces newpath with oldpath. Windows refuses to replace a file
// while another handle has it open or is replacing it, so concurrent
// builds committing the same Artifact name fail with ERROR_ACCESS_DENIED
// or ERROR_SHARING_VIOLATION. Those clear within milliseconds; retry them
// for up to a couple of seconds.
func rename(oldpath, newpath string) error {
	const timeout = 2 * time.Second
	start := time.Now()
	delay := time.Millisecond
	for {
		err := os.Rename(oldpath, newpath)
		if err == nil || !transient(err) || time.Since(start) > timeout {
			return err
		}
		time.Sleep(delay)
		delay = min(2*delay, 100*time.Millisecond)
	}
}

func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
