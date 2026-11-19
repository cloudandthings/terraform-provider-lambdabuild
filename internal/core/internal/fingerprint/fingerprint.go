// Package fingerprint computes the Input fingerprint: a SHA-256 over a
// canonical, length-prefixed stream of build inputs.
//
// Stream layout (any change alters every fingerprint, so treat it like a
// packaging-format change):
//
//	"lambdabuild-input-fingerprint\n"
//	"format <packaging-format version>\n"
//	"args <len>\n" <canonical JSON of output-affecting arguments> "\n"
//	for each section, in the given order:
//	  "section <name> <file count>\n"
//	  for each file, in the given order (callers sort by path):
//	    "file <len(rel)> <rel> <size>\n" <file bytes>
//
// File bytes are hashed verbatim and file modes are never hashed.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// File is one hashed file.
type File struct {
	Rel string // relative path, "/" separators
	Abs string // path on disk
}

// Section is a named, ordered group of files, such as "project" or "source".
type Section struct {
	Name  string
	Files []File
}

// Input is everything that goes into a fingerprint.
type Input struct {
	FormatVersion int
	// Args must marshal to deterministic JSON: structs with fixed field
	// order, maps (keys are sorted by encoding/json) and non-nil slices.
	Args     any
	Sections []Section
}

// Result is a computed fingerprint.
type Result struct {
	Hex string
	// CRLF lists the Rel paths of files containing "\r\n", in stream order.
	CRLF []string
}

// Compute streams every input into SHA-256.
func Compute(in Input) (Result, error) {
	args, err := json.Marshal(in.Args)
	if err != nil {
		return Result{}, fmt.Errorf("encode fingerprint arguments: %w", err)
	}

	h := sha256.New()
	_, _ = fmt.Fprintf(h, "lambdabuild-input-fingerprint\nformat %d\nargs %d\n", in.FormatVersion, len(args))
	h.Write(args)
	_, _ = io.WriteString(h, "\n")

	var res Result
	for _, s := range in.Sections {
		_, _ = fmt.Fprintf(h, "section %s %d\n", s.Name, len(s.Files))
		for _, f := range s.Files {
			crlf, err := hashFile(h, f)
			if err != nil {
				return Result{}, err
			}
			if crlf {
				res.CRLF = append(res.CRLF, f.Rel)
			}
		}
	}
	res.Hex = hex.EncodeToString(h.Sum(nil))
	return res, nil
}

func hashFile(h io.Writer, f File) (crlf bool, err error) {
	fh, err := os.Open(f.Abs)
	if err != nil {
		return false, err
	}
	defer func() { _ = fh.Close() }()
	info, err := fh.Stat()
	if err != nil {
		return false, err
	}
	_, _ = fmt.Fprintf(h, "file %d %s %d\n", len(f.Rel), f.Rel, info.Size())

	d := &crlfDetector{w: h}
	n, err := io.Copy(d, fh)
	if err != nil {
		return false, fmt.Errorf("fingerprint %s: %w", f.Rel, err)
	}
	if n != info.Size() {
		return false, fmt.Errorf("fingerprint %s: file changed while reading", f.Rel)
	}
	return d.found && !d.binary, nil
}

// crlfDetector passes bytes through and records whether "\r\n" occurs,
// including across write boundaries, and whether the file is binary (holds
// a NUL byte, as git decides). Only text files get a CRLF warning: binary
// files are checked out byte for byte on every host.
type crlfDetector struct {
	w      io.Writer
	lastCR bool
	found  bool
	binary bool
}

func (d *crlfDetector) Write(p []byte) (int, error) {
	if !d.binary {
		for _, b := range p {
			if b == 0 {
				d.binary = true
				break
			}
			if b == '\n' && d.lastCR {
				d.found = true
			}
			d.lastCR = b == '\r'
		}
	}
	return d.w.Write(p)
}
