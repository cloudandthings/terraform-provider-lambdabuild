// Package packager writes reproducible zips. Every rule here feeds the
// packaging-format version: change a rule, bump FormatVersion.
package packager

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// FormatVersion is the packaging-format version. It feeds the Input
// fingerprint, so bumping it triggers exactly one rebuild for every user.
// Bump it whenever zip or SBOM rules, the pinned Go version, or output bytes
// after a uv upgrade change.
const FormatVersion = 2

// compressionLevel is the fixed deflate level for every entry.
const compressionLevel = flate.BestCompression

// zipEpoch is the timestamp of every entry.
var zipEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Entry is one file to place in the zip.
type Entry struct {
	// Name is the zip path: relative, "/" separators.
	Name string
	// Path is the file on disk whose bytes become the entry.
	Path string
}

// WriteZip writes entries to w as a deterministic zip and returns the total
// uncompressed size. Entries are sorted by Name in byte order; the input
// order does not matter.
func WriteZip(w io.Writer, entries []Entry) (unpacked int64, err error) {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i, e := range sorted {
		if err := validName(e.Name); err != nil {
			return 0, err
		}
		if i > 0 && sorted[i-1].Name == e.Name {
			return 0, fmt.Errorf("duplicate zip entry %q", e.Name)
		}
	}

	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, compressionLevel)
	})
	for _, e := range sorted {
		n, err := writeEntry(zw, e)
		if err != nil {
			return 0, err
		}
		unpacked += n
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return unpacked, nil
}

func writeEntry(zw *zip.Writer, e Entry) (int64, error) {
	f, err := os.Open(e.Path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	br := bufio.NewReader(f)
	head, _ := br.Peek(4) // short files yield fewer bytes; not an error

	hdr := &zip.FileHeader{Name: e.Name, Method: zip.Deflate, Modified: zipEpoch}
	hdr.SetMode(modeFor(head))
	zf, err := zw.CreateHeader(hdr)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(zf, br)
	if err != nil {
		return 0, fmt.Errorf("zip entry %q: %w", e.Name, err)
	}
	return n, nil
}

// modeFor derives the entry mode from content, never from the filesystem,
// so hosts without an executable bit produce the same bytes.
func modeFor(head []byte) os.FileMode {
	if bytes.HasPrefix(head, []byte("\x7fELF")) || bytes.HasPrefix(head, []byte("#!")) {
		return 0o755
	}
	return 0o644
}

func validName(name string) error {
	switch {
	case name == "",
		strings.HasPrefix(name, "/"),
		strings.HasPrefix(name, "./"),
		strings.HasSuffix(name, "/"),
		strings.Contains(name, `\`),
		name == "..", strings.HasPrefix(name, "../"), strings.Contains(name, "/../"):
		return fmt.Errorf("invalid zip entry name %q", name)
	}
	return nil
}
