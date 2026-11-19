// Package artifact manages output_dir: Artifact file names and atomic
// writes of the Artifact's files (zip and SBOM) as one unit.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// PrefixLen is the number of fingerprint hex characters in file names.
const PrefixLen = 16

// ZipPath returns <outputDir>/<name>-<fingerprint prefix>.zip. The separator
// is always "/" so the value stored in state is identical on every host.
func ZipPath(outputDir, name, fingerprint string) string {
	return join(outputDir, name+"-"+prefix(fingerprint)+".zip")
}

// SBOMPath returns <outputDir>/<name>-<fingerprint prefix>.cdx.json.
func SBOMPath(outputDir, name, fingerprint string) string {
	return join(outputDir, name+"-"+prefix(fingerprint)+".cdx.json")
}

func prefix(fp string) string {
	if len(fp) < PrefixLen {
		return fp
	}
	return fp[:PrefixLen]
}

func join(dir, file string) string {
	dir = strings.TrimRight(dir, `/\`)
	if dir == "" {
		return file
	}
	return dir + "/" + file
}

// Written describes a staged file.
type Written struct {
	SHA256 [32]byte
	Size   int64
}

// HexSHA256 is the lowercase hex digest.
func (w Written) HexSHA256() string { return hex.EncodeToString(w.SHA256[:]) }

// Staged is a file written to a temp file in output_dir, not yet in place.
type Staged struct {
	Written
	tmp   string
	final string
}

// Stage creates outputDir if needed and streams write's output into a
// uniquely named temp file in outputDir while hashing it. Concurrent
// writers never see each other's partial files; the temp file is removed
// on failure. Commit puts it in place, Discard removes it.
func Stage(outputDir, finalPath string, write func(io.Writer) error) (*Staged, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output_dir: %w", err)
	}
	tmp, err := os.CreateTemp(outputDir, ".lambdabuild-*.tmp")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	h := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(tmp, h)}
	if err := write(cw); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return nil, err
	}
	ok = true
	st := &Staged{tmp: tmp.Name(), final: finalPath}
	copy(st.SHA256[:], h.Sum(nil))
	st.Size = cw.n
	return st, nil
}

// Discard removes the staged files that are still temp files. nil entries
// are skipped.
func Discard(files ...*Staged) {
	for _, f := range files {
		if f != nil {
			_ = os.Remove(f.tmp)
		}
	}
}

// Commit renames the staged files into place, in order, as one unit: if a
// rename fails, the files already renamed by this call are removed again
// and every remaining temp file is discarded, so an Artifact is never left
// half written (a zip without its SBOM). nil entries are skipped.
func Commit(files ...*Staged) error {
	for i, f := range files {
		if f == nil {
			continue
		}
		if err := rename(f.tmp, f.final); err != nil {
			for _, done := range files[:i] {
				if done != nil {
					_ = os.Remove(done.final)
				}
			}
			Discard(files[i:]...)
			return err
		}
	}
	return nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
