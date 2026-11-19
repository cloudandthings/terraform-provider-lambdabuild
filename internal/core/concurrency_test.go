package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// TestConcurrentBuildsSharingAFreshCacheAreReproducible is the story 33
// stress test: Terraform's default parallelism runs many builds at once,
// all sharing one cache_dir. The cache_dir here is fresh except for a
// tampered uv binary at the extraction path, so the first builds replace
// it (story 50) and install the managed Python while the others already
// run them. Builds of one fixture also share one project tree, as
// resources pointing at the same project_dir (or at sibling members of
// one workspace) do. Every build must succeed and match its golden hash,
// and the tampered binary must end up replaced by the embedded one.
//
// It owns its cache_dir: tests must never tamper with the shared test
// cache, or concurrently running test binaries execute the tampered uv.
func TestConcurrentBuildsSharingAFreshCacheAreReproducible(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads a managed Python into a fresh cache_dir")
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	uvCache := filepath.Join(t.TempDir(), "uv-cache")

	exe := "uv"
	if runtime.GOOS == "windows" {
		exe = "uv.exe"
	}
	uv := filepath.Join(cacheDir, "uv", embeddedUVVersion(t), exe)
	writeFile(t, uv, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(uv, 0o755); err != nil {
		t.Fatal(err)
	}

	const perFixture = 3
	type job struct {
		gc  goldenCase
		req core.Request
	}
	var jobs []job
	for _, gc := range goldenCases {
		project := copyFixture(t, gc.fixture) // one tree shared by every build of this fixture
		for i := range perFixture {
			req := gc.request(project, filepath.Join(t.TempDir(), fmt.Sprintf("out-%d", i)))
			req.Toolchain.CacheDir = cacheDir
			req.Toolchain.UVCacheDir = uvCache
			jobs = append(jobs, job{gc: gc, req: req})
		}
	}

	results := make([]core.BuildResult, len(jobs))
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = core.Build(context.Background(), j.req)
		}()
	}
	wg.Wait()

	for i, j := range jobs {
		if errs[i] != nil {
			t.Errorf("%s (build %d): %v", j.gc.name, i, errs[i])
			continue
		}
		if results[i].OutputSHA256 != j.gc.zipSHA256 {
			t.Errorf("%s (build %d): zip sha256\n got %s\nwant %s", j.gc.name, i, results[i].OutputSHA256, j.gc.zipSHA256)
		}
	}
	if b, err := os.ReadFile(uv); err != nil || string(b) == "#!/bin/sh\nexit 0\n" {
		t.Errorf("tampered uv at %s was not replaced with the embedded binary (err %v)", uv, err)
	}
}

// embeddedUVVersion is the pinned uv release this provider embeds and
// extracts to <cache_dir>/uv/<version>/.
func embeddedUVVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("internal", "uvbin", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
