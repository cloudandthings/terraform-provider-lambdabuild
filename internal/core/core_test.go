package core_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// copyFixture copies testdata/fixtures/<name> into a fresh temp dir so tests
// can add clutter without touching the committed fixture.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "fixtures", name)
	dst := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// codeOnlyRequest is the request the provider would send for a code-only
// function Artifact with every optional argument left at its default.
func codeOnlyRequest(projectDir, outputDir string) core.Request {
	return core.Request{
		ProjectDir:          projectDir,
		OutputDir:           outputDir,
		Name:                "code-only",
		PythonVersion:       "3.12",
		Architecture:        "x86_64",
		ArtifactType:        "function",
		InstallDependencies: false,
		SBOM:                false,
	}
}

func zipEntries(t *testing.T, path string) []*zip.File {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r.File
}

func entryNames(files []*zip.File) []string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return names
}

func TestBuildCodeOnlyFunctionContainsSourceFilesSortedByPath(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")

	res, err := core.Build(context.Background(), codeOnlyRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"app/__init__.py",
		"app/handlers/__init__.py",
		"app/handlers/greet.py",
		"handler.py",
		"scripts/run.py",
	}
	if got := entryNames(zipEntries(t, res.OutputPath)); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries:\n got %q\nwant %q", got, want)
	}
}

func TestBuildZipEntriesFollowDeterministicPackagingRules(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")

	res, err := core.Build(context.Background(), codeOnlyRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}

	epoch := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range zipEntries(t, res.OutputPath) {
		if strings.HasSuffix(f.Name, "/") || strings.Contains(f.Name, `\`) {
			t.Errorf("%s: directory entry or backslash separator", f.Name)
		}
		if f.Method != zip.Deflate {
			t.Errorf("%s: method %d, want deflate", f.Name, f.Method)
		}
		if !f.Modified.UTC().Equal(epoch) {
			t.Errorf("%s: modified %v, want %v", f.Name, f.Modified, epoch)
		}
		wantMode := fs.FileMode(0o644)
		if f.Name == "scripts/run.py" { // starts with "#!"
			wantMode = 0o755
		}
		if got := f.Mode().Perm(); got != wantMode {
			t.Errorf("%s: mode %o, want %o", f.Name, got, wantMode)
		}
	}
}

func TestBuildMarksELFBinariesExecutableRegardlessOfFilesystemMode(t *testing.T) {
	project := copyFixture(t, "code-only")
	writeFile(t, filepath.Join(project, "bin_tool.py"), "\x7fELF\x02\x01\x01 not really python")
	writeFile(t, filepath.Join(project, "tiny.py"), "#")
	out := filepath.Join(t.TempDir(), "out")

	res, err := core.Build(context.Background(), codeOnlyRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]fs.FileMode{}
	for _, f := range zipEntries(t, res.OutputPath) {
		modes[f.Name] = f.Mode().Perm()
	}
	if modes["bin_tool.py"] != 0o755 {
		t.Errorf("ELF entry mode %o, want 755", modes["bin_tool.py"])
	}
	if modes["tiny.py"] != 0o644 {
		t.Errorf("one-byte entry mode %o, want 644", modes["tiny.py"])
	}
}

func TestBuildWritesZipNamedByFingerprintPrefixAndReportsItsHashes(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")

	res, err := core.Build(context.Background(), codeOnlyRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.ToSlash(out) + "/code-only-" + res.Fingerprint[:16] + ".zip"; filepath.ToSlash(res.OutputPath) != want {
		t.Errorf("output path %q, want %q", res.OutputPath, want)
	}
	b, err := os.ReadFile(res.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if res.OutputSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("OutputSHA256 %s does not match zip bytes", res.OutputSHA256)
	}
	if res.OutputBase64SHA256 != base64.StdEncoding.EncodeToString(sum[:]) {
		t.Errorf("OutputBase64SHA256 %s does not match zip bytes", res.OutputBase64SHA256)
	}
	if res.OutputSize != int64(len(b)) {
		t.Errorf("OutputSize %d, want %d", res.OutputSize, len(b))
	}
	var unpacked int64
	for _, f := range zipEntries(t, res.OutputPath) {
		unpacked += int64(f.UncompressedSize64)
	}
	if res.UnpackedSize != unpacked {
		t.Errorf("UnpackedSize %d, want %d", res.UnpackedSize, unpacked)
	}
	if res.SBOMPath != "" || res.UVVersion != "" {
		t.Errorf("code-only build without SBOM reported SBOMPath %q UVVersion %q", res.SBOMPath, res.UVVersion)
	}
}

func TestBuildLayerPlacesContentUnderPython(t *testing.T) {
	project := copyFixture(t, "code-only")
	req := codeOnlyRequest(project, filepath.Join(t.TempDir(), "out"))
	req.ArtifactType = core.ArtifactTypeLayer

	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entryNames(zipEntries(t, res.OutputPath)) {
		if !strings.HasPrefix(name, "python/") {
			t.Errorf("layer entry %q not under python/", name)
		}
	}
}
