// Package python installs a uv project's third-party Packages for Lambda's
// platform into a staging directory and cleans host-specific files out of
// it. Command lines follow spike 03 (notes/03-export-install.md).
package python

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/toolchain"
)

// Options select what to install and for which Lambda platform.
type Options struct {
	// ProjectDir is the uv project (or workspace) root. Commands run there,
	// so the project's [tool.uv] settings and relative index paths apply.
	ProjectDir string
	// Python is the managed interpreter path.
	Python string
	// PythonVersion is the Lambda runtime version, e.g. "3.12".
	PythonVersion string
	// Platform is uv's --python-platform, e.g. "x86_64-manylinux_2_34".
	Platform string
	// Frozen skips the uv.lock staleness check (lock_mode = "frozen").
	Frozen          bool
	WorkspaceMember string
	Extras          []string
	Groups          []string
	// TmpDir receives the requirements file. Staging receives the Packages.
	TmpDir  string
	Staging string
}

// exportArgs are the uv export arguments: pinned requirements with hashes
// and the project's index URLs baked in, local packages left out. Groups
// are opt-in: --no-default-groups ignores [tool.uv] default-groups, so only
// o.Groups install, matching what the fingerprint traverses in uv.lock.
func exportArgs(o Options, out string) []string {
	lock := "--locked"
	if o.Frozen {
		lock = "--frozen"
	}
	args := []string{
		"export", lock, "--no-dev", "--no-default-groups", "--no-emit-project", "--no-emit-local", "--no-editable",
		"--emit-index-url", "--python", o.Python,
	}
	if o.WorkspaceMember != "" {
		args = append(args, "--package", o.WorkspaceMember)
	}
	for _, e := range o.Extras {
		args = append(args, "--extra", e)
	}
	for _, g := range o.Groups {
		args = append(args, "--group", g)
	}
	return append(args, "--format", "requirements-txt", "-o", out)
}

// installArgs are the uv pip install arguments: wheels only, for Lambda's
// platform, verified against the hashes in the requirements file.
//
// Project configuration is not read (--config-file replaces discovery):
// its [tool.uv.pip] table could switch hash checking off or force source
// builds (a configured no-binary even wins over --only-binary). The only
// project settings the install needs are its named indexes, so per-index
// credentials (UV_INDEX_<NAME>_USERNAME/_PASSWORD) bind to them; indexConfig
// writes those to indexes, a uv.toml of their own. User and system config
// are neutralised by the toolchain environment.
func installArgs(o Options, requirements, indexes string) []string {
	return []string{
		"pip", "install", "-r", requirements,
		"--config-file", indexes,
		"--target", o.Staging,
		"--python", o.Python,
		"--python-version", o.PythonVersion,
		"--python-platform", o.Platform,
		"--require-hashes",
		"--only-binary", ":all:",
		"--link-mode", "copy",
		"--no-compile",
	}
}

// indexConfig writes a uv.toml to path holding only the project's
// [[tool.uv.index]] entries (from <ProjectDir>/pyproject.toml, the
// workspace root), as [[index]] tables. Relative local index paths are made
// absolute against ProjectDir, where uv resolves them in pyproject.toml.
func indexConfig(projectDir, path string) error {
	var py struct {
		Tool struct {
			UV struct {
				Index []map[string]any `toml:"index"`
			} `toml:"uv"`
		} `toml:"tool"`
	}
	if _, err := toml.DecodeFile(filepath.Join(projectDir, "pyproject.toml"), &py); err != nil {
		return fmt.Errorf("read [[tool.uv.index]] from pyproject.toml: %w", err)
	}
	for _, idx := range py.Tool.UV.Index {
		if u, ok := idx["url"].(string); ok && !strings.Contains(u, "://") && !filepath.IsAbs(u) {
			idx["url"] = filepath.Join(projectDir, filepath.FromSlash(u))
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(map[string]any{"index": py.Tool.UV.Index}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// InstallDependencies exports the pinned requirements and installs them
// into o.Staging.
func InstallDependencies(ctx context.Context, tc *toolchain.Toolchain, o Options) error {
	req := filepath.Join(o.TmpDir, "requirements.txt")
	if _, err := tc.Run(ctx, o.ProjectDir, exportArgs(o, req)...); err != nil {
		return err
	}
	indexes := filepath.Join(o.TmpDir, "uv-indexes.toml")
	if err := indexConfig(o.ProjectDir, indexes); err != nil {
		return err
	}
	// Run from the project dir: a relative --index-url (a local index)
	// resolves against the working directory.
	_, err := tc.Run(ctx, o.ProjectDir, installArgs(o, req, indexes)...)
	return explainMissingWheel(err, o)
}

// missingWheelPatterns match uv's complaints about a Package that has no
// installable wheel, capturing the Package name.
var missingWheelPatterns = []*regexp.Regexp{
	regexp.MustCompile("Wheels are required for `([^`]+)`"),
	regexp.MustCompile("Because ([A-Za-z0-9._-]+)(?:==|[<>!~=]=?)?[^ ]* has no usable wheels"),
	regexp.MustCompile("Building source distributions is disabled, but attempted to build `([^`]+)`"),
}

// explainMissingWheel turns uv's "no usable wheels" failure into an error
// that names the Package and says what to do. Other errors pass through.
func explainMissingWheel(err error, o Options) error {
	if err == nil {
		return nil
	}
	for _, re := range missingWheelPatterns {
		if m := re.FindStringSubmatch(err.Error()); m != nil {
			return fmt.Errorf("package %q has no wheel for %s (Python %s): Lambda builds install wheels only, so pin a version that ships wheels for this platform: %w",
				m[1], o.Platform, o.PythonVersion, err)
		}
	}
	return err
}

// BuildWheel builds src, the absolute path of a uv project or local
// dependency (a per-build copy of its directory, or an sdist file), into a
// wheel in outDir. The build backend's requirements are resolved by uv build
// itself, using the project's [tool.uv] index settings, so the command runs
// in ProjectDir.
func BuildWheel(ctx context.Context, tc *toolchain.Toolchain, o Options, src, outDir string) error {
	_, err := tc.Run(ctx, o.ProjectDir, "build", "--wheel", "--python", o.Python, "--out-dir", outDir, src)
	return err
}

// InstallWheels installs every wheel in wheelsDir into o.Staging without
// their dependencies (those come from InstallDependencies), for Lambda's
// platform. A directory without wheels is a no-op.
func InstallWheels(ctx context.Context, tc *toolchain.Toolchain, o Options, wheelsDir string) error {
	wheels, err := filepath.Glob(filepath.Join(wheelsDir, "*.whl"))
	if err != nil {
		return err
	}
	if len(wheels) == 0 {
		return nil
	}
	sort.Strings(wheels)
	args := []string{
		"pip", "install", "--no-deps",
		"--target", o.Staging,
		"--python", o.Python,
		"--python-version", o.PythonVersion,
		"--python-platform", o.Platform,
		"--only-binary", ":all:",
		"--link-mode", "copy",
		"--no-config",
	}
	_, err = tc.Run(ctx, o.ProjectDir, append(args, wheels...)...)
	return err
}

// Clean removes host-specific files from staging so the Artifact bytes do
// not depend on the build host:
//   - bin/ (console scripts embed the host interpreter path),
//   - __pycache__ folders and .pyc files,
//   - uv's .lock in the target root and *.dist-info/uv_cache.json (holds
//     a timestamp),
//   - *.dist-info/direct_url.json (records absolute local paths),
//
// and drops the matching lines from every RECORD file, whose hashes would
// otherwise carry the host-specific bytes.
func Clean(staging string) error {
	var removed []string // slash paths relative to staging
	remove := func(p string, d fs.DirEntry) error {
		rel, err := filepath.Rel(staging, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			removed = append(removed, rel+"/")
			if err := os.RemoveAll(p); err != nil {
				return err
			}
			return fs.SkipDir
		}
		removed = append(removed, rel)
		return os.Remove(p)
	}
	err := filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == staging {
			return nil
		}
		name := d.Name()
		parent := filepath.Dir(p)
		inDistInfo := strings.HasSuffix(filepath.Base(parent), ".dist-info")
		switch {
		case d.IsDir() && name == "__pycache__":
			return remove(p, d)
		case d.IsDir() && name == "bin" && parent == staging:
			return remove(p, d)
		case !d.IsDir() && strings.HasSuffix(name, ".pyc"):
			return remove(p, d)
		case !d.IsDir() && name == ".lock" && parent == staging:
			return remove(p, d)
		case !d.IsDir() && inDistInfo && (name == "uv_cache.json" || name == "direct_url.json"):
			return remove(p, d)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return pruneRecords(staging, removed)
}

// pruneRecords rewrites each */*.dist-info/RECORD without lines for removed
// paths. RECORD paths are relative to the install root with "/" separators.
func pruneRecords(staging string, removed []string) error {
	if len(removed) == 0 {
		return nil
	}
	gone := func(path string) bool {
		path = strings.TrimPrefix(path, "./")
		for _, r := range removed {
			if path == r || (strings.HasSuffix(r, "/") && strings.HasPrefix(path, r)) {
				return true
			}
		}
		return false
	}
	records, err := filepath.Glob(filepath.Join(staging, "*.dist-info", "RECORD"))
	if err != nil {
		return err
	}
	for _, rec := range records {
		b, err := os.ReadFile(rec)
		if err != nil {
			return err
		}
		var out strings.Builder
		changed := false
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			path, _, _ := strings.Cut(line, ",")
			if gone(strings.Trim(path, `"`)) {
				changed = true
				continue
			}
			out.WriteString(line)
			out.WriteString("\n")
		}
		if err := sc.Err(); err != nil {
			return err
		}
		if changed {
			if err := os.WriteFile(rec, []byte(out.String()), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
