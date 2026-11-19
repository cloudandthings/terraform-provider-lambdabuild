package core_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(b)+content)
}

func fingerprintOf(t *testing.T, req core.Request) core.FingerprintResult {
	t.Helper()
	res, err := core.Fingerprint(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFingerprintMatchesBuildAndPredictsOutputPath(t *testing.T) {
	project := copyFixture(t, "code-only")
	req := codeOnlyRequest(project, filepath.Join(t.TempDir(), "out"))

	fp := fingerprintOf(t, req)
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if fp.Fingerprint != res.Fingerprint || fp.OutputPath != res.OutputPath {
		t.Fatalf("Fingerprint %s %s, Build %s %s", fp.Fingerprint, fp.OutputPath, res.Fingerprint, res.OutputPath)
	}
	if len(fp.Fingerprint) != 64 {
		t.Fatalf("fingerprint %q is not hex SHA-256", fp.Fingerprint)
	}
}

func TestFingerprintIgnoresExcludedGitignoredAndUnselectedFiles(t *testing.T) {
	project := copyFixture(t, "code-only")
	req := codeOnlyRequest(project, filepath.Join(t.TempDir(), "out"))
	before := fingerprintOf(t, req)

	for _, p := range []string{
		".venv/lib/python3.12/site-packages/x.py", // default source_exclude
		"app/__pycache__/greet.cpython-312.py",
		".git/hooks/pre-commit.py",
		"dist/old.py",
		"pkg.egg-info/x.py",
		"tests/test_more.py",
		".lambdabuild/stale.py",
		"scratch_notes.py",      // .gitignore: scratch_*.py at any depth
		"app/scratch_x.py",      //
		"local_settings.py",     // .gitignore: /local_settings.py (anchored)
		"generated/out.py",      // .gitignore: generated/
		"generated/keep.py",     // negation cannot re-include under an ignored dir
		"app/templates.html",    // not matched by source_include
		"app/sub/.gitignore",    // nested .gitignore itself is not a .py
		"app/sub/ignored_me.py", // ignored by the nested .gitignore below
	} {
		writeFile(t, filepath.Join(project, p), "clutter\n")
	}
	writeFile(t, filepath.Join(project, "app/sub/.gitignore"), "ignored_me.py\n")

	after := fingerprintOf(t, req)
	if before.Fingerprint != after.Fingerprint {
		t.Fatal("fingerprint changed after adding excluded or ignored files")
	}

	// A file under app/ that is *not* anchored-ignored is selected.
	writeFile(t, filepath.Join(project, "app/local_settings.py"), "x = 1\n")
	if fingerprintOf(t, req).Fingerprint == before.Fingerprint {
		t.Fatal("/local_settings.py is anchored to the root; app/local_settings.py must be fingerprinted")
	}
}

func TestBuildSkipsExcludedAndGitignoredFiles(t *testing.T) {
	project := copyFixture(t, "code-only")
	writeFile(t, filepath.Join(project, "scratch_a.py"), "x\n")
	writeFile(t, filepath.Join(project, "generated/keep.py"), "x\n")
	req := codeOnlyRequest(project, filepath.Join(t.TempDir(), "out"))
	req.SourceExclude = []string{"scripts"}

	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"app/__init__.py",
		"app/handlers/__init__.py",
		"app/handlers/greet.py",
		"handler.py",
		"tests/test_handler.py", // custom source_exclude replaces the defaults
	}
	got := entryNames(zipEntries(t, res.OutputPath))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries:\n got %q\nwant %q", got, want)
	}
}

func TestFingerprintChangesWithSelectedContentPathsAndProjectFiles(t *testing.T) {
	project := copyFixture(t, "code-only")
	req := codeOnlyRequest(project, filepath.Join(t.TempDir(), "out"))
	base := fingerprintOf(t, req).Fingerprint

	edits := map[string]func(){
		"source content": func() { writeFile(t, filepath.Join(project, "handler.py"), "print(1)\n") },
		"source rename": func() {
			if err := os.Rename(filepath.Join(project, "handler.py"), filepath.Join(project, "main.py")); err != nil {
				t.Fatal(err)
			}
		},
		"new source": func() { writeFile(t, filepath.Join(project, "app/new.py"), "") },
		"pyproject":  func() { writeFile(t, filepath.Join(project, "pyproject.toml"), "[project]\nname = \"x\"\n") },
		"uv.lock":    func() { appendFile(t, filepath.Join(project, "uv.lock"), "# edited\n") },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			project = copyFixture(t, "code-only")
			req.ProjectDir = project
			if got := fingerprintOf(t, req).Fingerprint; got != base {
				t.Fatalf("fresh fixture copy changed fingerprint: %s vs %s", got, base)
			}
			edit()
			if fingerprintOf(t, req).Fingerprint == base {
				t.Fatal("fingerprint did not change")
			}
		})
	}
}

func TestFingerprintDoesNotDependOnLocationOrExecutableBit(t *testing.T) {
	a := copyFixture(t, "code-only")
	b := copyFixture(t, "code-only")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(b, "handler.py"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fa := fingerprintOf(t, codeOnlyRequest(a, "out-a"))
	fb := fingerprintOf(t, codeOnlyRequest(b, "out-b"))
	if fa.Fingerprint != fb.Fingerprint {
		t.Fatal("fingerprint depends on project location, output_dir or executable bit")
	}
}

func TestFingerprintChangesWithOutputAffectingArguments(t *testing.T) {
	project := copyFixture(t, "code-only")
	base := codeOnlyRequest(project, "out")
	baseFP := fingerprintOf(t, base).Fingerprint

	variants := map[string]func(*core.Request){
		"python_version":       func(r *core.Request) { r.PythonVersion = "3.13" },
		"architecture":         func(r *core.Request) { r.Architecture = core.ArchitectureARM64 },
		"artifact_type":        func(r *core.Request) { r.ArtifactType = core.ArtifactTypeLayer },
		"platform_tag":         func(r *core.Request) { r.PlatformTag = "manylinux_2_28" },
		"extras":               func(r *core.Request) { r.Extras = []string{"fast"} },
		"groups":               func(r *core.Request) { r.Groups = []string{"lambda"} },
		"exclude_packages":     func(r *core.Request) { r.ExcludePackages = []string{"boto3"} },
		"install_dependencies": func(r *core.Request) { r.InstallDependencies = true },
		"sbom":                 func(r *core.Request) { r.SBOM = true },
		"source_include":       func(r *core.Request) { r.SourceInclude = []string{"**/*.py", "**/*.txt"} },
		"source_exclude":       func(r *core.Request) { r.SourceExclude = []string{".venv"} },
		"triggers":             func(r *core.Request) { r.Triggers = map[string]string{"k": "v"} },
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			req := base
			mutate(&req)
			if fingerprintOf(t, req).Fingerprint == baseFP {
				t.Fatal("fingerprint did not change")
			}
		})
	}

	t.Run("name and output_dir do not change it", func(t *testing.T) {
		req := base
		req.Name = "other"
		req.OutputDir = "elsewhere"
		if fingerprintOf(t, req).Fingerprint != baseFP {
			t.Fatal("fingerprint changed")
		}
	})
	t.Run("defaults spelled out do not change it", func(t *testing.T) {
		req := base
		req.Architecture = ""
		req.ArtifactType = ""
		req.PlatformTag = "manylinux_2_34"
		req.SourceInclude = core.DefaultSourceInclude
		req.Triggers = map[string]string{}
		if fingerprintOf(t, req).Fingerprint != baseFP {
			t.Fatal("fingerprint changed")
		}
	})
	t.Run("list order does not change it", func(t *testing.T) {
		a, b := base, base
		a.Extras = []string{"x", "y"}
		b.Extras = []string{"y", "x"}
		if fingerprintOf(t, a).Fingerprint != fingerprintOf(t, b).Fingerprint {
			t.Fatal("extras order changed fingerprint")
		}
	})
}

func TestFingerprintWarnsAboutCRLFSourceFiles(t *testing.T) {
	project := copyFixture(t, "code-only")
	req := codeOnlyRequest(project, "out")
	if w := fingerprintOf(t, req).Warnings; len(w) != 0 {
		t.Fatalf("unexpected warnings for LF fixture: %+v", w)
	}

	writeFile(t, filepath.Join(project, "app/handlers/greet.py"), "def greet(name):\r\n    return name\r\n")
	writeFile(t, filepath.Join(project, "lone_cr.py"), "a = 1\rb = 2\n") // bare CR is not CRLF
	res := fingerprintOf(t, req)
	if len(res.Warnings) != 1 {
		t.Fatalf("want one warning, got %+v", res.Warnings)
	}
	w := res.Warnings[0]
	if w.Code != core.WarningCRLF || !reflect.DeepEqual(w.Paths, []string{"app/handlers/greet.py"}) {
		t.Fatalf("warning %+v", w)
	}
}

func TestFingerprintDoesNotWarnAboutCRLFInBinaryFiles(t *testing.T) {
	// Package data such as images may contain the bytes \r\n; only text
	// files differ between CRLF and LF checkouts.
	project := copyFixture(t, "packaged")
	req := packagedRequest(project, "out")
	writeFile(t, filepath.Join(project, "demoapp", "templates", "logo.png"), "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if w := fingerprintOf(t, req).Warnings; len(w) != 0 {
		t.Fatalf("CRLF warning for a binary file: %+v", w)
	}
}

func TestFingerprintIgnoresExcludePackagesSpelling(t *testing.T) {
	// Package names compare PEP 503-normalised: "Six", "six" and "SIX"
	// exclude the same Package and build the same Artifact.
	project := copyFixture(t, "pure-deps")
	req := depsRequest(project, "out")
	req.ExcludePackages = []string{"six", "Typing_Extensions"}
	want := fingerprintOf(t, req).Fingerprint
	req.ExcludePackages = []string{"typing-extensions", "Six", "SIX"}
	if got := fingerprintOf(t, req).Fingerprint; got != want {
		t.Error("exclude_packages spelling changed the fingerprint")
	}
}

// inGitRepo places fixture name in <repo>/services/<name>, where <repo>
// holds .git and the given root .gitignore, and returns the project dir.
func inGitRepo(t *testing.T, name, gitignore string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".gitignore"), gitignore)
	project := filepath.Join(repo, "services", name)
	if err := os.MkdirAll(filepath.Dir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(copyFixture(t, name), project); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestFingerprintHonoursGitignoreFilesAboveTheProject(t *testing.T) {
	// The repository root's .gitignore applies to a project in a
	// subfolder, as in git; unanchored and anchored rules alike.
	for _, tc := range []struct {
		fixture string
		req     func(project, out string) core.Request
	}{
		{"code-only", codeOnlyRequest}, // source_include files
		{"packaged", packagedRequest},  // whole tree (install_project)
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			project := inGitRepo(t, tc.fixture, "*_local.py\n/services/"+tc.fixture+"/generated/\n")
			req := tc.req(project, filepath.Join(t.TempDir(), "out"))
			before := fingerprintOf(t, req).Fingerprint
			writeFile(t, filepath.Join(project, "settings_local.py"), "DEBUG = True\n")
			writeFile(t, filepath.Join(project, "generated", "stub.py"), "x = 1\n")
			if after := fingerprintOf(t, req).Fingerprint; after != before {
				t.Error("files ignored by the repository's root .gitignore changed the fingerprint")
			}
			writeFile(t, filepath.Join(project, "kept.py"), "y = 2\n")
			if fingerprintOf(t, req).Fingerprint == before {
				t.Error("a file not ignored did not change the fingerprint")
			}
		})
	}
}
