// Package toolchain extracts and verifies the embedded uv, installs the
// uv-managed Python, and runs uv with an environment isolated from user
// and system uv configuration.
//
// Every uv call is a direct exec of the uv binary with an argument list;
// nothing runs through a shell.
package toolchain

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/uvbin"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/filehash"
)

// Config locates the toolchain. Field meanings follow the provider
// arguments of the same name.
type Config struct {
	// CacheDir holds the extracted uv (<CacheDir>/uv/<version>/) and the
	// managed Pythons (<CacheDir>/python). Must allow executing binaries.
	CacheDir string
	// UVCacheDir is uv's package cache; empty means uv's own default.
	UVCacheDir string
	// UVPath uses this external uv instead of extracting the embedded one.
	UVPath string
	// IndexURL replaces the default package index; ExtraIndexURLs add
	// indexes. Both may carry credentials.
	IndexURL       string
	ExtraIndexURLs []string
	// PythonInstallMirror is the managed Python mirror.
	PythonInstallMirror string
	// NativeTLS trusts the OS certificate store.
	NativeTLS bool
	// Offline stops uv from using the network (cached data only).
	Offline bool
}

// Toolchain is a ready-to-run uv.
type Toolchain struct {
	// UV is the absolute path of the uv binary.
	UV string
	// Version is the uv release, e.g. "0.13.6".
	Version string

	cacheDir string
	env      []string
}

// Ensure extracts the embedded uv into cfg.CacheDir if it is missing or
// fails its compiled-in checksum, and returns a Toolchain that runs it.
func Ensure(cfg Config) (*Toolchain, error) {
	if cfg.CacheDir == "" {
		return nil, errors.New("cache_dir is empty")
	}
	cacheDir, err := filepath.Abs(cfg.CacheDir)
	if err != nil {
		return nil, err
	}
	env := environment(os.Environ(), cacheDir, cfg)
	if cfg.UVPath != "" {
		return externalUV(cfg.UVPath, cacheDir, env)
	}
	bin, ok := uvbin.Binary()
	if !ok {
		return nil, errors.New("this provider build has no embedded uv for the host platform")
	}
	dir := filepath.Join(cacheDir, "uv", uvbin.Version)
	path := filepath.Join(dir, bin.Exe)
	if err := extract(bin, dir, path); err != nil {
		return nil, fmt.Errorf("extract uv %s to %s: %w", uvbin.Version, dir, err)
	}
	return &Toolchain{UV: path, Version: uvbin.Version, cacheDir: cacheDir, env: env}, nil
}

// externalUV wraps a user-supplied uv (uv_path). Its version is read from
// `uv --version`; the pinned-version guarantee does not apply to it.
func externalUV(path, cacheDir string, env []string) (*Toolchain, error) {
	abs, err := exec.LookPath(path)
	if err != nil {
		return nil, fmt.Errorf("uv_path %q: %w", path, err)
	}
	if abs, err = filepath.Abs(abs); err != nil {
		return nil, err
	}
	tc := &Toolchain{UV: abs, cacheDir: cacheDir, env: env}
	out, err := tc.Run(context.Background(), "", "--version")
	if err != nil {
		return nil, fmt.Errorf("uv_path %q: %w", path, err)
	}
	// "uv 0.13.6 (abcdef 2025-01-01)"
	if f := strings.Fields(out); len(f) >= 2 {
		tc.Version = f[1]
	}
	return tc, nil
}

// extract writes the binary to path unless a file with the right checksum
// is already there.
//
// Builds run in parallel, in one provider process and across processes
// sharing a cache_dir, and some of them may already be running the uv at
// path. So extraction never writes into an existing file: macOS kills a
// process whose signed executable changes underneath it, and a half-written
// binary would run as garbage. Writers serialise on a per-version lock
// file, re-check under the lock (another writer may have finished), write
// a temp file in the same folder and rename it over path. The rename swaps
// the directory entry only; running processes keep the old file.
func extract(bin uvbin.Embedded, dir, path string) error {
	if sum, err := filehash.SHA256(path); err == nil && sum == bin.SHA256 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".extract.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }() // releases the lock
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("lock %s: %w", lock.Name(), err)
	}
	if sum, err := filehash.SHA256(path); err == nil && sum == bin.SHA256 {
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".uv-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	zr, err := gzip.NewReader(bytes.NewReader(bin.Gzip))
	if err != nil {
		_ = tmp.Close()
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), zr); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != bin.SHA256 {
		return fmt.Errorf("embedded uv checksum %s does not match compiled-in %s", got, bin.SHA256)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// keepUVVar reports whether an inherited UV_* variable survives the scrub:
// only per-index credentials (UV_INDEX_<NAME>_USERNAME / _PASSWORD), which
// cannot change which Packages are installed.
func keepUVVar(name string) bool {
	return strings.HasPrefix(name, "UV_INDEX_") &&
		(strings.HasSuffix(name, "_USERNAME") || strings.HasSuffix(name, "_PASSWORD"))
}

// environment builds the uv child environment: the process environment
// (HOME, PATH, proxies, certificates, credentials) minus every inherited
// uv setting, plus the toolchain overlay. User config is isolated by
// pointing the user config dirs at an empty folder (spike 02): --no-config
// would also drop the project's [tool.uv] indexes.
func environment(base []string, cacheDir string, cfg Config) []string {
	emptyConfig := emptyConfigDir(cacheDir)
	overlay := map[string]string{
		"XDG_CONFIG_HOME":       emptyConfig, // macOS and Linux user uv.toml
		"APPDATA":               emptyConfig, // Windows user uv.toml
		"UV_NO_SYSTEM_CONFIG":   "1",
		"UV_PYTHON_INSTALL_DIR": filepath.Join(cacheDir, "python"),
		"UV_PYTHON_PREFERENCE":  "only-managed",
		"UV_NO_PROGRESS":        "1",
	}
	if cfg.UVCacheDir != "" {
		overlay["UV_CACHE_DIR"] = cfg.UVCacheDir
	}
	if cfg.IndexURL != "" {
		overlay["UV_DEFAULT_INDEX"] = cfg.IndexURL
	}
	if len(cfg.ExtraIndexURLs) > 0 {
		overlay["UV_INDEX"] = strings.Join(cfg.ExtraIndexURLs, " ")
	}
	if cfg.PythonInstallMirror != "" {
		overlay["UV_PYTHON_INSTALL_MIRROR"] = cfg.PythonInstallMirror
	}
	if cfg.NativeTLS {
		overlay["UV_NATIVE_TLS"] = "1"
	}
	if cfg.Offline {
		overlay["UV_OFFLINE"] = "1"
	}
	env := make([]string, 0, len(base)+len(overlay))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name) // Windows names are case-insensitive
		if _, overridden := overlay[upper]; overridden {
			continue
		}
		if strings.HasPrefix(upper, "UV_") && !keepUVVar(upper) {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range overlay {
		env = append(env, k+"="+v)
	}
	return env
}

// Run executes uv with args in dir (the cache dir when empty) and returns
// its stdout. On failure the error carries uv's stderr but never the
// environment, which may hold credentials.
func (tc *Toolchain) Run(ctx context.Context, dir string, args ...string) (string, error) {
	if err := os.MkdirAll(emptyConfigDir(tc.cacheDir), 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, tc.UV, args...)
	cmd.Dir = dir
	if dir == "" {
		cmd.Dir = tc.cacheDir
	}
	cmd.Env = tc.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		name := "uv"
		if len(args) > 0 {
			name += " " + args[0]
			if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
				name += " " + args[1]
			}
		}
		return "", &redactedError{
			msg: redact(fmt.Sprintf("%s failed: %v\n%s", name, err, strings.TrimSpace(stderr.String())), tc.env),
			err: err,
		}
	}
	return stdout.String(), nil
}

// redactedError carries a scrubbed message and the original cause for
// errors.Is/As (the cause is an exec error and holds no environment).
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

var userinfoURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)

// Redacted replaces secrets in redact's output.
const Redacted = "***"

// redact scrubs credentials from text that may reach Terraform diagnostics
// or logs: userinfo in URLs, and the value of every uv index credential or
// secret-looking variable in env (UV_INDEX_*_USERNAME/_PASSWORD, and names
// containing PASSWORD, TOKEN or SECRET). Short values are skipped so that
// "1" or "true" cannot blank out unrelated text.
func redact(s string, env []string) string {
	s = userinfoURL.ReplaceAllString(s, "${1}"+Redacted+"@")
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		secret := keepUVVar(upper) ||
			strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET")
		if secret && len(value) >= 4 {
			s = strings.ReplaceAll(s, value, Redacted)
		}
		if upper == "UV_DEFAULT_INDEX" || upper == "UV_INDEX" {
			for _, raw := range strings.Fields(value) {
				s = redactUserinfo(s, raw)
			}
		}
	}
	return s
}

// redactUserinfo blanks the user name and password of a URL (also in their
// percent-decoded form) wherever they appear in s.
func redactUserinfo(s, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return s
	}
	pw, _ := u.User.Password()
	for _, v := range []string{u.User.Username(), pw} {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, Redacted)
		}
	}
	return s
}

// emptyConfigDir stands in for the user config dir. It stays empty.
func emptyConfigDir(cacheDir string) string {
	return filepath.Join(cacheDir, "uv-empty-config")
}

// Python is a managed interpreter.
type Python struct {
	Path        string
	FullVersion string
}

// Python installs (once) and locates the managed Python for version, e.g.
// "3.12". System Pythons are never used.
//
// Builds sharing a cache_dir serialise on a lock file in the install dir.
// uv python install (re)creates the minor-version link
// (cpython-3.12-<platform>, a junction on Windows) without a lock of its
// own, so a concurrent uv python find through it sees a half-built
// interpreter, and two installs fail on the link with a sharing violation.
//
// The lock only covers install and find; builds then run the interpreter
// for minutes. So the returned Path never goes through the minor-version
// link: it is found by exact patch version, which resolves to that
// installation's own folder, and checked to be in it. --no-bin and
// --no-registry keep the install out of the user's bin dir and the
// Windows registry, which are shared beyond cache_dir.
func (tc *Toolchain) Python(ctx context.Context, version string) (Python, error) {
	dir := filepath.Join(tc.cacheDir, "python")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Python{}, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".install.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Python{}, err
	}
	defer func() { _ = lock.Close() }() // releases the lock
	if err := lockFile(lock); err != nil {
		return Python{}, fmt.Errorf("lock %s: %w", lock.Name(), err)
	}
	if _, err := tc.Run(ctx, "", "python", "install", "--no-bin", "--no-registry", version); err != nil {
		return Python{}, err
	}
	out, err := tc.Run(ctx, "", "python", "find", "--show-version", version)
	if err != nil {
		return Python{}, err
	}
	full := strings.TrimSpace(out)
	out, err = tc.Run(ctx, "", "python", "find", full)
	if err != nil {
		return Python{}, err
	}
	path := strings.TrimSpace(out)
	if !inPatchInstall(path, full) {
		return Python{}, fmt.Errorf("uv python find %s returned %s, not an interpreter in a cpython-%s-* folder", full, path, full)
	}
	return Python{Path: path, FullVersion: full}, nil
}

// inPatchInstall reports whether path is inside a cpython-<full>-<platform>
// install folder, not reached through a minor-version link such as
// cpython-3.12-<platform>. It matches path components rather than comparing
// with the install dir, which Windows may spell with short (8.3) names.
func inPatchInstall(path, full string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, "cpython-"+full+"-") {
			return true
		}
	}
	return false
}
