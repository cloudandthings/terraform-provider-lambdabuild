package core_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// testCacheDir is the cache_dir shared by every test that runs the embedded
// uv, so the managed Python is downloaded once per machine, not per test.
// Override with LAMBDABUILD_TEST_CACHE_DIR.
func testCacheDir() string {
	if d := os.Getenv("LAMBDABUILD_TEST_CACHE_DIR"); d != "" {
		return d
	}
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "lambdabuild-test")
}

// depsRequest is the request the provider would send for a function
// Artifact with dependencies of the pure-deps fixture. Packages come from
// the fixture's committed local index, so no network is needed once the
// managed Python is cached.
func depsRequest(projectDir, outputDir string) core.Request {
	return core.Request{
		ProjectDir:          projectDir,
		OutputDir:           outputDir,
		Name:                "pure-deps",
		PythonVersion:       "3.12",
		Architecture:        "x86_64",
		ArtifactType:        "function",
		InstallDependencies: true,
		SBOM:                false,
		Toolchain:           core.ToolchainConfig{CacheDir: testCacheDir()},
	}
}

func zipContent(t *testing.T, path, name string) string {
	t.Helper()
	for _, f := range zipEntries(t, path) {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	t.Fatalf("%s: no entry %s", path, name)
	return ""
}

func TestBuildFunctionWithDependenciesIncludesInstalledPackagesAndSource(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	res, err := core.Build(context.Background(), depsRequest(project, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatal(err)
	}

	names := entryNames(zipEntries(t, res.OutputPath))
	for _, want := range []string{
		"handler.py",
		"six.py",
		"six-1.17.0.dist-info/METADATA",
		"tabulate/__init__.py",
		"tabulate-0.9.0.dist-info/RECORD",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("missing entry %s in %q", want, names)
		}
	}
	if res.UVVersion != "0.13.6" {
		t.Errorf("UVVersion %q, want the embedded 0.13.6", res.UVVersion)
	}
	if !strings.HasPrefix(res.PythonFullVersion, "3.12.") {
		t.Errorf("PythonFullVersion %q, want 3.12.x", res.PythonFullVersion)
	}
}

func TestBuildWithDependenciesDropsHostSpecificFiles(t *testing.T) {
	t.Setenv("UV_COMPILE_BYTECODE", "1") // inherited uv settings must not leak into the build
	project := copyFixture(t, "pure-deps")
	res, err := core.Build(context.Background(), depsRequest(project, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range entryNames(zipEntries(t, res.OutputPath)) {
		base := filepath.Base(name)
		if strings.HasPrefix(name, "bin/") || strings.Contains(name, "__pycache__") ||
			strings.HasSuffix(name, ".pyc") || base == ".lock" || base == "uv_cache.json" {
			t.Errorf("host-specific entry %s in Artifact", name)
		}
	}
	record := zipContent(t, res.OutputPath, "tabulate-0.9.0.dist-info/RECORD")
	for _, gone := range []string{"bin/", "uv_cache.json"} {
		if strings.Contains(record, gone) {
			t.Errorf("RECORD still lists %s:\n%s", gone, record)
		}
	}
}

func TestBuildLockModeLockedFailsOnStaleLockAndFrozenUsesTheLock(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	// Drop tabulate from pyproject.toml; uv.lock still pins it.
	pyproject := filepath.Join(project, "pyproject.toml")
	b, err := os.ReadFile(pyproject)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, pyproject, strings.Replace(string(b), `, "tabulate==0.9.0"`, "", 1))
	req := depsRequest(project, filepath.Join(t.TempDir(), "out"))

	if _, err := core.Build(context.Background(), req); err == nil {
		t.Fatal(`lock_mode "locked" (default) built from a stale uv.lock`)
	}

	req.LockMode = core.LockModeFrozen
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatalf(`lock_mode "frozen": %v`, err)
	}
	if !slices.Contains(entryNames(zipEntries(t, res.OutputPath)), "tabulate/__init__.py") {
		t.Error(`lock_mode "frozen" did not install what uv.lock pins`)
	}
}

func TestBuildRejectsWheelThatDoesNotMatchLockHash(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	// Rewrite six's wheel with the same files plus an archive comment: a
	// valid wheel whose bytes no longer match the hash in uv.lock.
	wheel := filepath.Join(project, "simple", "six", "six-1.17.0-py2.py3-none-any.whl")
	zr, err := zip.OpenReader(wheel)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if err := zw.Copy(f); err != nil {
			t.Fatal(err)
		}
	}
	_ = zr.Close()
	if err := zw.SetComment("tampered"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wheel, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	req := depsRequest(project, filepath.Join(t.TempDir(), "out"))
	req.Toolchain.UVCacheDir = t.TempDir() // the shared uv cache holds the genuine wheel

	_, err = core.Build(context.Background(), req)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "hash mismatch") {
		t.Fatalf("err = %v, want a hash mismatch", err)
	}

	// The project's [tool.uv.pip] settings apply to the install (so its
	// named indexes and their credentials work), but cannot switch hash
	// checking off or allow source builds.
	pyproject := filepath.Join(project, "pyproject.toml")
	b, err := os.ReadFile(pyproject)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, pyproject, string(b)+"\n[tool.uv.pip]\nverify-hashes = false\nrequire-hashes = false\nno-binary = [\":all:\"]\nonly-binary = []\n")
	req.LockMode = core.LockModeFrozen // [tool.uv.pip] is not part of uv.lock, but stay independent of that
	req.Toolchain.UVCacheDir = t.TempDir()
	_, err = core.Build(context.Background(), req)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "hash mismatch") {
		t.Fatalf("with [tool.uv.pip] verify-hashes = false: err = %v, want a hash mismatch", err)
	}
}

// Paths reach uv as argv entries, never through a shell, so shell
// metacharacters in project and output paths are inert.
func TestBuildPassesPathsWithShellMetacharactersVerbatim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip(`"$" and ";" are not valid in every Windows path`)
	}
	root := filepath.Join(t.TempDir(), "a b; touch pwned $(touch pwned2) `touch pwned3`")
	project := filepath.Join(root, "pure-deps")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(copyFixture(t, "pure-deps"), project); err != nil {
		t.Fatal(err)
	}
	res, err := core.Build(context.Background(), depsRequest(project, filepath.Join(root, "out $HOME")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.OutputPath, "out $HOME") {
		t.Errorf("output path %q was expanded", res.OutputPath)
	}
	for _, f := range []string{"pwned", "pwned2", "pwned3"} {
		for _, dir := range []string{project, root, "."} {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				t.Errorf("a shell ran: %s created in %s", f, dir)
			}
		}
	}
}

// User-level uv config and inherited UV_* settings must not leak into a
// build, while the project's [[tool.uv.index]] is honoured (spike 02).
// Every bogus index points at a closed local port, so using any of them
// fails the build.
func TestBuildIgnoresUserUVConfigAndHonoursProjectIndex(t *testing.T) {
	userConfig := t.TempDir()
	bogus := "http://127.0.0.1:9/simple"
	writeFile(t, filepath.Join(userConfig, "uv", "uv.toml"), ""+
		"index-url = \""+bogus+"\"\n"+
		"extra-index-url = [\""+bogus+"\"]\n"+
		"find-links = [\""+bogus+"\"]\n"+
		"[pip]\n"+ // the dependency install step
		"index-url = \""+bogus+"\"\n"+
		"no-binary = [\":all:\"]\n")
	t.Setenv("XDG_CONFIG_HOME", userConfig) // macOS and Linux
	t.Setenv("APPDATA", userConfig)         // Windows
	t.Setenv("UV_INDEX_URL", bogus)
	t.Setenv("UV_EXTRA_INDEX_URL", bogus)
	t.Setenv("UV_DEFAULT_INDEX", bogus)

	project := copyFixture(t, "pure-deps")
	res, err := core.Build(context.Background(), depsRequest(project, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(entryNames(zipEntries(t, res.OutputPath)), "six.py") {
		t.Error("six not installed from the project index")
	}
}
