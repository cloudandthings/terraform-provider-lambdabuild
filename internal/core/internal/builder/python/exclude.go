package python

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Excluded is an installed Package that ExcludePackages removed. Name is the
// normalized (PEP 503) name.
type Excluded struct {
	Name    string
	Version string
}

var nonNameChars = regexp.MustCompile(`[-_.]+`)

// NormalizeName is the PEP 503 normalization of a Package name.
func NormalizeName(name string) string {
	return nonNameChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}

// ExcludePackages removes each named Package from staging: every file its
// dist-info RECORD lists, then the dist-info folder itself. Names match
// after PEP 503 normalization. A name that is not installed is ignored
// (for example boto3 when nothing depends on it). It returns the Packages
// actually removed, sorted by name. Run it after Clean.
func ExcludePackages(staging string, names []string) ([]Excluded, error) {
	if len(names) == 0 {
		return nil, nil
	}
	want := map[string]bool{}
	for _, n := range names {
		want[NormalizeName(n)] = true
	}
	infos, err := filepath.Glob(filepath.Join(staging, "*.dist-info"))
	if err != nil {
		return nil, err
	}
	var removed []Excluded
	for _, info := range infos {
		name, version, err := distInfoIdentity(info)
		if err != nil {
			return nil, err
		}
		if !want[name] {
			continue
		}
		if err := removeRecordedFiles(staging, info); err != nil {
			return nil, fmt.Errorf("excluding %s: %w", name, err)
		}
		if err := os.RemoveAll(info); err != nil {
			return nil, err
		}
		removed = append(removed, Excluded{Name: name, Version: version})
	}
	slices.SortFunc(removed, func(a, b Excluded) int { return strings.Compare(a.Name, b.Name) })
	return removed, nil
}

// distInfoIdentity reads the Package name and version from METADATA, falling
// back to the folder name "<name>-<version>.dist-info".
func distInfoIdentity(info string) (name, version string, err error) {
	f, err := os.Open(filepath.Join(info, "METADATA"))
	if err == nil {
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				break // end of headers
			}
			if v, ok := strings.CutPrefix(line, "Name:"); ok && name == "" {
				name = NormalizeName(v)
			} else if v, ok := strings.CutPrefix(line, "Version:"); ok && version == "" {
				version = strings.TrimSpace(v)
			}
		}
		if err := sc.Err(); err != nil {
			return "", "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", "", err
	}
	if name == "" {
		base := strings.TrimSuffix(filepath.Base(info), ".dist-info")
		n, v, _ := strings.Cut(base, "-")
		name, version = NormalizeName(n), v
	}
	return name, version, nil
}

// removeRecordedFiles deletes the files listed in info/RECORD and any
// directories that become empty. Paths escaping staging are ignored.
func removeRecordedFiles(staging, info string) error {
	b, err := os.ReadFile(filepath.Join(info, "RECORD"))
	if err != nil {
		return fmt.Errorf("reading RECORD: %w", err)
	}
	root := filepath.Clean(staging) + string(filepath.Separator)
	var dirs []string
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		path, _, _ := strings.Cut(sc.Text(), ",")
		path = strings.Trim(path, `"`)
		if path == "" {
			continue
		}
		abs := filepath.Join(staging, filepath.FromSlash(path))
		if !strings.HasPrefix(abs, root) {
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		dirs = append(dirs, filepath.Dir(abs))
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Deepest first so nested empty folders collapse; os.Remove fails on
	// non-empty directories, which is what we want.
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) })
	for _, d := range slices.Compact(dirs) {
		for d != filepath.Clean(staging) && strings.HasPrefix(d, root) {
			if os.Remove(d) != nil {
				break
			}
			d = filepath.Dir(d)
		}
	}
	return nil
}
