package python

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Installed is a Package found in staging through its *.dist-info folder.
type Installed struct {
	// Name is the Name header of METADATA as written by the Package;
	// NormalizedName is its PEP 503 form.
	Name           string
	NormalizedName string
	Version        string
	// LicenseExpression is the License-Expression header (SPDX); License
	// the first line of the legacy License header. Both may be empty.
	LicenseExpression string
	License           string
	// Tags are the Tag headers of the dist-info WHEEL file.
	Tags []string
}

// ListInstalled reads the identity, licence and wheel tags of every
// Package in staging, sorted by normalized name then version.
func ListInstalled(staging string) ([]Installed, error) {
	infos, err := filepath.Glob(filepath.Join(staging, "*.dist-info"))
	if err != nil {
		return nil, err
	}
	var out []Installed
	for _, info := range infos {
		name, version, err := distInfoIdentity(info)
		if err != nil {
			return nil, err
		}
		in := Installed{Name: name, NormalizedName: name, Version: version}
		h, err := readHeaders(filepath.Join(info, "METADATA"))
		if err != nil {
			return nil, err
		}
		if v := h["name"]; len(v) > 0 && strings.TrimSpace(v[0]) != "" {
			in.Name = strings.TrimSpace(v[0])
		} else {
			in.Name = strings.TrimSuffix(strings.SplitN(filepath.Base(info), "-", 2)[0], ".dist-info")
		}
		if v := h["license-expression"]; len(v) > 0 {
			in.LicenseExpression = strings.TrimSpace(v[0])
		}
		if v := h["license"]; len(v) > 0 {
			in.License = strings.TrimSpace(v[0])
		}
		w, err := readHeaders(filepath.Join(info, "WHEEL"))
		if err != nil {
			return nil, err
		}
		in.Tags = w["tag"]
		out = append(out, in)
	}
	slices.SortFunc(out, func(a, b Installed) int {
		if c := strings.Compare(a.NormalizedName, b.NormalizedName); c != 0 {
			return c
		}
		return strings.Compare(a.Version, b.Version)
	})
	return out, nil
}

// readHeaders parses the RFC 822-style header block of a METADATA or WHEEL
// file into lower-cased keys. Continuation lines (leading whitespace) are
// ignored; a missing file yields no headers.
func readHeaders(file string) (map[string][]string, error) {
	f, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := map[string][]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			break // body (the long description) follows
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		h[k] = append(h[k], strings.TrimSpace(v))
	}
	return h, sc.Err()
}
