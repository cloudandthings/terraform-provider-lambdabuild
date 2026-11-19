package core_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

func TestFingerprintPrunesExcludedFoldersWithoutReadingThem(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	project := copyFixture(t, "code-only")
	for _, dir := range []string{".venv/locked", "generated/locked"} { // default exclude, .gitignore
		locked := filepath.Join(project, dir)
		writeFile(t, filepath.Join(locked, "x.py"), "x\n")
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	}

	if _, err := core.Fingerprint(context.Background(), codeOnlyRequest(project, "out")); err != nil {
		t.Fatalf("excluded folder was read: %v", err)
	}
}

func TestConcurrentBuildsIntoOneOutputDirDoNotCollide(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")

	const n = 8
	results := make([]core.BuildResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := codeOnlyRequest(project, out)
			if i%2 == 1 {
				req.Name = "other" // a second resource sharing output_dir
			}
			results[i], errs[i] = core.Build(context.Background(), req)
		}()
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("build %d: %v", i, errs[i])
		}
		if results[i].OutputSHA256 != results[0].OutputSHA256 {
			t.Fatalf("build %d produced different bytes", i)
		}
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Fatalf("output_dir holds %q; want exactly the two zips and no temp files", names)
	}
}

func TestBuildRejectsInvalidRequests(t *testing.T) {
	project := copyFixture(t, "code-only")
	cases := map[string]func(*core.Request){
		"python_version":  func(r *core.Request) { r.PythonVersion = "3.8" },
		"deprecated 3.10": func(r *core.Request) { r.PythonVersion = "3.10" }, // AWS deprecated python3.10 on 2026-10-31
		"architecture":    func(r *core.Request) { r.Architecture = "amd64" },
		"artifact_type":   func(r *core.Request) { r.ArtifactType = "image" },
		"project_dir":     func(r *core.Request) { r.ProjectDir = "" },
		"output_dir":      func(r *core.Request) { r.OutputDir = "" },
		"missing uv.lock": func(r *core.Request) { r.ProjectDir = t.TempDir() },
		"lock_mode":       func(r *core.Request) { r.LockMode = "unlocked" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := codeOnlyRequest(project, filepath.Join(t.TempDir(), "out"))
			mutate(&req)
			if _, err := core.Fingerprint(context.Background(), req); err == nil {
				t.Fatal("Fingerprint accepted invalid request")
			}
			if _, err := core.Build(context.Background(), req); err == nil {
				t.Fatal("Build accepted invalid request")
			}
		})
	}
}

func TestBuildDefaultsNameToProjectDirBaseName(t *testing.T) {
	project := copyFixture(t, "code-only")
	req := codeOnlyRequest(project, filepath.Join(t.TempDir(), "out"))
	req.Name = ""
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(res.OutputPath); !strings.HasPrefix(base, "code-only-") {
		t.Fatalf("zip name %q does not start with project_dir base name", base)
	}
}
