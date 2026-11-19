// Package uvlock reads the parts of a uv.lock that lambdabuild needs: the
// workspace members and the local source trees (directory, editable, path
// and virtual sources) reachable from the Package being built.
package uvlock

import (
	"errors"
	"fmt"
	"os"
	"path"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Source kinds of local Packages, as uv.lock spells them.
const (
	KindDirectory = "directory"
	KindEditable  = "editable"
	KindVirtual   = "virtual"
	KindPath      = "path"
)

// devGroup is the dependency group that --no-dev excludes. It is never
// followed, even when requested.
const devGroup = "dev"

// Lock is a parsed uv.lock.
type Lock struct {
	Manifest struct {
		Members []string `toml:"members"`
	} `toml:"manifest"`
	Packages []Package `toml:"package"`
}

// Package is one [[package]] entry.
type Package struct {
	Name                 string                  `toml:"name"`
	Version              string                  `toml:"version"`
	Source               map[string]any          `toml:"source"`
	Dependencies         []Dependency            `toml:"dependencies"`
	OptionalDependencies map[string][]Dependency `toml:"optional-dependencies"`
	DevDependencies      map[string][]Dependency `toml:"dev-dependencies"`
	Wheels               []wheelEntry            `toml:"wheels"`
}

type wheelEntry struct {
	URL  string `toml:"url"`
	Path string `toml:"path"`
	Hash string `toml:"hash"`
}

// Wheel is a locked wheel of a registry Package.
type Wheel struct {
	// Filename is the wheel file name, e.g. six-1.17.0-py2.py3-none-any.whl.
	Filename string
	// SHA256 is the lowercase hex digest; empty when uv.lock records none
	// or a different algorithm.
	SHA256 string
}

// Wheels returns the wheels uv.lock pins for the registry Package with the
// given name (any spelling) and version, in lock order. Local, git and URL
// sources have none.
func (l *Lock) Wheels(name, version string) []Wheel {
	want := normalizeName(name)
	var out []Wheel
	for i := range l.Packages {
		p := &l.Packages[i]
		if normalizeName(p.Name) != want || p.Version != version {
			continue
		}
		if _, ok := p.Source["registry"]; !ok {
			continue
		}
		for _, w := range p.Wheels {
			loc := w.URL
			if loc == "" {
				loc = w.Path
			}
			if i := strings.IndexAny(loc, "?#"); i >= 0 {
				loc = loc[:i]
			}
			digest, _ := strings.CutPrefix(w.Hash, "sha256:")
			if digest == w.Hash {
				digest = ""
			}
			out = append(out, Wheel{Filename: path.Base(loc), SHA256: digest})
		}
	}
	return out
}

func normalizeName(name string) string {
	return strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))
}

// Dependency is one edge of the lock graph. Version and Source are set by
// uv only when the name alone is ambiguous. Extra activates extras of the
// target. Markers are not evaluated: an edge is followed whatever its
// marker says, so the result may over-approximate but never misses a tree.
type Dependency struct {
	Name    string         `toml:"name"`
	Version string         `toml:"version"`
	Source  map[string]any `toml:"source"`
	Extra   []string       `toml:"extra"`
}

// Local is a Package whose source is a local file or directory.
type Local struct {
	Name    string
	Version string
	// Kind is one of the Kind* constants.
	Kind string
	// Path is the source path exactly as uv.lock records it: relative to
	// the directory holding uv.lock (or absolute), "/"-separated.
	Path string
	// Member reports a workspace member (built with uv build --package).
	Member bool
}

// Load parses the uv.lock at file.
func Load(file string) (*Lock, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var l Lock
	if _, err := toml.Decode(string(b), &l); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return &l, nil
}

// local returns p as a Local, or false for registry, git and URL sources.
func (l *Lock) local(p *Package) (Local, bool) {
	for _, kind := range []string{KindDirectory, KindEditable, KindVirtual, KindPath} {
		if s, ok := p.Source[kind].(string); ok {
			return Local{
				Name:    p.Name,
				Version: p.Version,
				Kind:    kind,
				Path:    s,
				Member:  slices.Contains(l.Manifest.Members, p.Name),
			}, true
		}
	}
	return Local{}, false
}

// ErrNotMember is returned by Root for a name that is not a workspace member.
var ErrNotMember = errors.New("not a member of the uv workspace")

// Root returns the uv project that a build selects: the workspace member
// called member, or, when member is empty, the project at the lock's own
// directory. A workspace root without a [project] table has nothing to
// build on its own, so member is then required.
func (l *Lock) Root(member string) (Local, error) {
	for i := range l.Packages {
		p := &l.Packages[i]
		loc, isLocal := l.local(p)
		if !isLocal {
			continue
		}
		if member != "" && loc.Member && p.Name == member {
			return loc, nil
		}
		if member == "" && loc.Kind != KindPath && path.Clean(loc.Path) == "." {
			return loc, nil
		}
	}
	if member != "" {
		return Local{}, fmt.Errorf("workspace_member %q: %w (members: %v)", member, ErrNotMember, l.Manifest.Members)
	}
	if len(l.Manifest.Members) == 0 {
		return Local{}, errors.New("uv.lock has no package for the project in project_dir: run uv lock")
	}
	return Local{}, fmt.Errorf("project_dir is a uv workspace root without a [project] table: set workspace_member to the member to build (members: %v)", l.Manifest.Members)
}

// Reachable returns the local Packages that a build of member (see Root)
// with the given extras and dependency groups installs, sorted by name and
// path. The root itself is left out. Virtual Packages are traversed but
// left out: uv never installs them. Extras apply to the roots and wherever
// an edge activates them; groups apply to the roots only, and the dev
// group is never followed.
func (l *Lock) Reachable(member string, extras, groups []string) ([]Local, error) {
	root, err := l.Root(member)
	if err != nil {
		return nil, err
	}

	type visit struct {
		pkg   int
		extra string // "" = the package's base dependencies
	}
	seen := map[visit]bool{}
	reached := map[int]bool{}
	var queue []visit
	push := func(pkg int, extra string) {
		v := visit{pkg, extra}
		if !seen[v] {
			seen[v] = true
			queue = append(queue, v)
		}
	}
	follow := func(deps []Dependency) error {
		for _, d := range deps {
			i, err := l.resolve(d)
			if err != nil {
				return err
			}
			reached[i] = true
			push(i, "")
			for _, e := range d.Extra {
				push(i, e)
			}
		}
		return nil
	}

	var roots []int
	for i := range l.Packages {
		loc, isLocal := l.local(&l.Packages[i])
		if !isLocal {
			continue
		}
		if loc.Name == root.Name && loc.Path == root.Path {
			roots = append(roots, i)
		}
	}
	for _, r := range roots {
		push(r, "")
		for _, e := range extras {
			push(r, e)
		}
		for _, g := range groups {
			if g == devGroup {
				continue
			}
			if err := follow(l.Packages[r].DevDependencies[g]); err != nil {
				return nil, err
			}
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		p := &l.Packages[v.pkg]
		deps := p.Dependencies
		if v.extra != "" {
			deps = p.OptionalDependencies[v.extra]
		}
		if err := follow(deps); err != nil {
			return nil, err
		}
	}

	var out []Local
	for i := range reached {
		if slices.Contains(roots, i) {
			continue
		}
		loc, isLocal := l.local(&l.Packages[i])
		if !isLocal || loc.Kind == KindVirtual {
			continue
		}
		out = append(out, loc)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Name != out[b].Name {
			return out[a].Name < out[b].Name
		}
		return out[a].Path < out[b].Path
	})
	return out, nil
}

// resolve finds the Package a dependency edge points at.
func (l *Lock) resolve(d Dependency) (int, error) {
	found := -1
	for i := range l.Packages {
		p := &l.Packages[i]
		if p.Name != d.Name {
			continue
		}
		if d.Version != "" && p.Version != d.Version {
			continue
		}
		if d.Source != nil && !reflect.DeepEqual(d.Source, p.Source) {
			continue
		}
		if found >= 0 {
			return 0, fmt.Errorf("uv.lock: dependency %q matches more than one package", d.Name)
		}
		found = i
	}
	if found < 0 {
		return 0, fmt.Errorf("uv.lock: dependency %q has no package entry", d.Name)
	}
	return found, nil
}
