package core_test

import (
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// writeBlob writes n bytes of random (incompressible) or zero (highly
// compressible) data.
func writeBlob(t *testing.T, path string, n int64, random bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var src io.Reader = zeroReader{}
	if random {
		src = rand.Reader
	}
	if _, err := io.CopyN(f, src, n); err != nil {
		t.Fatal(err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func sizeRequest(t *testing.T, mode string) core.Request {
	t.Helper()
	dir := copyFixture(t, "code-only")
	req := codeOnlyRequest(dir, t.TempDir())
	req.SourceInclude = []string{"**/*.py", "**/*.bin"}
	req.SizeLimitMode = mode
	return req
}

func warningCodes(r core.BuildResult) []string {
	var out []string
	for _, w := range r.Warnings {
		if w.Code != core.WarningCRLF { // random blobs may contain CR LF
			out = append(out, w.Code)
		}
	}
	return out
}

func TestBuildWarnsWhenZipExceedsDirectUploadLimitAndListsLargestPackages(t *testing.T) {
	req := sizeRequest(t, "")
	const mb = 1 << 20
	writeBlob(t, filepath.Join(req.ProjectDir, "bigpkg", "a.bin"), 51*mb, true)
	for i, n := range []string{"p2", "p3", "p4", "p5", "p6"} {
		writeBlob(t, filepath.Join(req.ProjectDir, n, "x.bin"), int64(i+1)*1024, false)
	}

	res, err := core.Build(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := warningCodes(res); len(got) != 1 || got[0] != core.WarningZipSize {
		t.Fatalf("warnings = %v, want [%s]", got, core.WarningZipSize)
	}
	msg := res.Warnings[len(res.Warnings)-1].Message
	for _, want := range []string{"bigpkg", "p6", "p5", "p4", "p3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "p2 ") {
		t.Errorf("message lists more than five Packages: %q", msg)
	}
	if strings.Index(msg, "bigpkg") > strings.Index(msg, "p6") {
		t.Errorf("largest not first: %q", msg)
	}
	if _, err := os.Stat(res.OutputPath); err != nil {
		t.Errorf("Artifact not written: %v", err)
	}
}

func TestBuildFailsAboveUnzippedLimitByDefaultWithoutWritingArtifact(t *testing.T) {
	req := sizeRequest(t, "")
	writeBlob(t, filepath.Join(req.ProjectDir, "hugepkg", "a.bin"), 251<<20, false)

	_, err := core.Build(t.Context(), req)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "hugepkg") || !strings.Contains(err.Error(), "size_limit_mode") {
		t.Errorf("error should name the biggest Package and the escape hatch: %v", err)
	}
	if entries, _ := os.ReadDir(req.OutputDir); len(entries) != 0 {
		t.Errorf("output dir should be empty, has %d entries", len(entries))
	}
}

func TestBuildWarnModeWritesArtifactAboveUnzippedLimit(t *testing.T) {
	req := sizeRequest(t, core.SizeLimitModeWarn)
	writeBlob(t, filepath.Join(req.ProjectDir, "hugepkg", "a.bin"), 251<<20, false)

	res, err := core.Build(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := warningCodes(res); len(got) != 1 || got[0] != core.WarningUnpackedSize {
		t.Fatalf("warnings = %v, want [%s]", got, core.WarningUnpackedSize)
	}
	if m := res.Warnings[len(res.Warnings)-1].Message; !strings.Contains(m, "hugepkg") {
		t.Errorf("message lacks biggest Package: %s", m)
	}
	if _, err := os.Stat(res.OutputPath); err != nil {
		t.Errorf("Artifact not written: %v", err)
	}
}

func TestBuildRejectsUnknownSizeLimitMode(t *testing.T) {
	req := sizeRequest(t, "loud")
	if _, err := core.Build(t.Context(), req); err == nil || !strings.Contains(err.Error(), "size_limit_mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildSmallArtifactHasNoSizeWarnings(t *testing.T) {
	req := sizeRequest(t, "")
	res, err := core.Build(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(warningCodes(res)) != 0 {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}
