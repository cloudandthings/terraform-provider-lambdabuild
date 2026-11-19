package core_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

func packagedRequest(projectDir, outputDir string) core.Request {
	r := depsRequest(projectDir, outputDir)
	r.Name = "packaged"
	return r
}

func TestBuildInstallsProjectWheelWithPackageData(t *testing.T) {
	project := copyFixture(t, "packaged") // has [build-system]: install_project defaults to true
	res, err := core.Build(context.Background(), packagedRequest(project, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	for _, want := range []string{
		"demoapp/__init__.py",
		"demoapp/templates/hello.txt",
		"demoapp-0.1.0.dist-info/METADATA",
		"six.py",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("missing entry %s in %q", want, names)
		}
	}
	if got := zipContent(t, res.OutputPath, "demoapp/templates/hello.txt"); got != "hello from package data v1\n" {
		t.Errorf("package data = %q", got)
	}
	for _, n := range names {
		if n == "README.md" || strings.HasSuffix(n, "direct_url.json") {
			t.Errorf("unexpected entry %s", n)
		}
	}
}

func TestInstallProjectFingerprintCoversWholeProjectTree(t *testing.T) {
	project := copyFixture(t, "packaged")
	out := filepath.Join(t.TempDir(), "out")
	before, err := core.Fingerprint(context.Background(), packagedRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project, "demoapp", "templates", "hello.txt"), "hello from package data v2\n")
	after, err := core.Fingerprint(context.Background(), packagedRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint == after.Fingerprint {
		t.Fatal("editing a package data file did not change the fingerprint")
	}
	res, err := core.Build(context.Background(), packagedRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}
	if got := zipContent(t, res.OutputPath, "demoapp/templates/hello.txt"); got != "hello from package data v2\n" {
		t.Errorf("package data = %q", got)
	}
}

func TestInstallProjectFingerprintIgnoresExcludedAndGitignoredPaths(t *testing.T) {
	project := copyFixture(t, "packaged")
	writeFile(t, filepath.Join(project, ".gitignore"), "scratch.txt\n")
	out := filepath.Join(t.TempDir(), "out")
	before, err := core.Fingerprint(context.Background(), packagedRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project, "scratch.txt"), "ignored")
	writeFile(t, filepath.Join(project, "tests", "test_x.py"), "excluded")
	after, err := core.Fingerprint(context.Background(), packagedRequest(project, out))
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint != after.Fingerprint {
		t.Error("gitignored or source_exclude'd files changed the fingerprint")
	}
}

func TestInstallProjectFalseCopiesSourceIncludeFilesInstead(t *testing.T) {
	project := copyFixture(t, "packaged")
	no := false
	req := packagedRequest(project, filepath.Join(t.TempDir(), "out"))
	req.InstallProject = &no
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	if !slices.Contains(names, "demoapp/__init__.py") {
		t.Errorf("source file missing: %q", names)
	}
	for _, n := range names {
		if strings.Contains(n, "hello.txt") || strings.HasPrefix(n, "demoapp-0.1.0.dist-info") {
			t.Errorf("project was installed despite install_project = false: %s", n)
		}
	}
}

func TestInstallProjectWithoutDependenciesStillInstallsTheProject(t *testing.T) {
	project := copyFixture(t, "packaged")
	req := packagedRequest(project, filepath.Join(t.TempDir(), "out"))
	req.InstallDependencies = false
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	if !slices.Contains(names, "demoapp/templates/hello.txt") || slices.Contains(names, "six.py") {
		t.Errorf("entries %q", names)
	}
}

// treeListing lists every path under root, relative with "/" separators.
func treeListing(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildingTheProjectWheelNeverWritesIntoTheSourceTree(t *testing.T) {
	// The fixture's in-tree backend leaves build/ and *.egg-info behind in
	// whatever directory it builds, as setuptools does. Resources sharing a
	// project tree build in parallel, so the build must not touch it.
	project := copyFixture(t, "intree-backend")
	before := treeListing(t, project)
	req := depsRequest(project, filepath.Join(t.TempDir(), "out"))
	req.Name = "intree"
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := zipContent(t, res.OutputPath, "intree/__init__.py"); got != "GREETING = \"hello from intree\"\n" {
		t.Errorf("intree/__init__.py = %q", got)
	}
	if after := treeListing(t, project); !slices.Equal(before, after) {
		t.Errorf("build changed the project tree\nbefore %q\n after %q", before, after)
	}
}

func TestPackageDataExcludedFromTheFingerprintDoesNotShip(t *testing.T) {
	// "build" is a default source_exclude, matched at any depth. A data
	// folder of that name is outside the fingerprint, so it must also stay
	// out of the Artifact, or edits to it would ship without a rebuild.
	project := copyFixture(t, "packaged")
	writeFile(t, filepath.Join(project, "demoapp", "templates", "build", "page.txt"), "v1\n")
	res, err := core.Build(context.Background(), packagedRequest(project, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	if slices.Contains(names, "demoapp/templates/build/page.txt") {
		t.Errorf("unfingerprinted file shipped: %q", names)
	}
	if !slices.Contains(names, "demoapp/templates/hello.txt") {
		t.Errorf("package data missing: %q", names)
	}
}
