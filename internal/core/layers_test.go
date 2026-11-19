package core_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// extrasRequest builds the extras-groups fixture: six is a base dependency,
// tabulate sits behind the "tab" extra, packaging in the "tools" group and
// iniconfig in the "dev" group.
func extrasRequest(projectDir, outputDir string) core.Request {
	r := depsRequest(projectDir, outputDir)
	r.Name = "extras-groups"
	return r
}

func buildNames(t *testing.T, req core.Request) (core.BuildResult, []string) {
	t.Helper()
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res, entryNames(zipEntries(t, res.OutputPath))
}

func hasPrefix(names []string, prefix string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, prefix) })
}

func TestLayerArtifactPlacesAllContentUnderPython(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	req := depsRequest(project, filepath.Join(t.TempDir(), "out"))
	req.ArtifactType = "layer"
	_, names := buildNames(t, req)

	for _, want := range []string{"python/handler.py", "python/six.py", "python/tabulate/__init__.py"} {
		if !slices.Contains(names, want) {
			t.Errorf("layer missing %s", want)
		}
	}
	for _, n := range names {
		if !strings.HasPrefix(n, "python/") {
			t.Errorf("layer entry outside python/: %s", n)
		}
	}
}

func TestDependenciesOnlyLayerShipsNoProjectCode(t *testing.T) {
	// The documented deps-only layer: install_project = false and
	// source_include = [] leave only the third-party Packages, so the
	// project's code ships once, in the function Artifact.
	project := copyFixture(t, "packaged") // has [build-system]
	no := false
	req := packagedRequest(project, filepath.Join(t.TempDir(), "out"))
	req.ArtifactType = core.ArtifactTypeLayer
	req.InstallProject = &no
	req.SourceInclude = []string{}
	_, names := buildNames(t, req)
	if !slices.Contains(names, "python/six.py") {
		t.Errorf("dependency six missing: %q", names)
	}
	if hasPrefix(names, "python/demoapp") {
		t.Errorf("project code in a deps-only layer: %q", names)
	}
	// Source edits do not rebuild it.
	before := fingerprintOf(t, req).Fingerprint
	writeFile(t, filepath.Join(project, "demoapp", "__init__.py"), "# edited\n")
	if fingerprintOf(t, req).Fingerprint != before {
		t.Error("a project source edit changed the deps-only layer's fingerprint")
	}
}

func TestExtrasAndGroupsAreInstalledAndDevNever(t *testing.T) {
	project := copyFixture(t, "extras-groups")
	out := filepath.Join(t.TempDir(), "out")

	_, base := buildNames(t, extrasRequest(project, out))
	if !slices.Contains(base, "six.py") {
		t.Error("base dependency six missing")
	}
	for _, absent := range []string{"tabulate/", "packaging/", "iniconfig/"} {
		if hasPrefix(base, absent) {
			t.Errorf("%s shipped without being selected", absent)
		}
	}

	req := extrasRequest(project, out)
	req.Extras = []string{"tab"}
	req.Groups = []string{"tools"}
	_, both := buildNames(t, req)
	for _, want := range []string{"tabulate/", "packaging/"} {
		if !hasPrefix(both, want) {
			t.Errorf("%s missing with extra tab and group tools", want)
		}
	}
	if hasPrefix(both, "iniconfig") {
		t.Error("dev group dependency iniconfig shipped")
	}
}

func TestProjectDefaultGroupsAreNotInstalledUnlessSelected(t *testing.T) {
	// [tool.uv] default-groups makes uv install "tools" by default. Groups
	// are opt-in for Artifacts: only `groups` selects them, so what ships
	// and what the fingerprint traverses in uv.lock always agree.
	project := copyFixture(t, "extras-groups")
	pyproject := filepath.Join(project, "pyproject.toml")
	b, err := os.ReadFile(pyproject)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, pyproject, string(b)+"\n[tool.uv]\ndefault-groups = [\"tools\"]\n")
	out := filepath.Join(t.TempDir(), "out")
	newReq := func() core.Request {
		r := extrasRequest(project, out)
		r.LockMode = core.LockModeFrozen // uv.lock records default-groups; not re-locked here
		return r
	}

	_, names := buildNames(t, newReq())
	if hasPrefix(names, "packaging/") {
		t.Error("default group tools shipped without being selected in groups")
	}
	req := newReq()
	req.Groups = []string{"tools"}
	if _, names := buildNames(t, req); !hasPrefix(names, "packaging/") {
		t.Error("group tools missing when selected")
	}
}

func TestExtrasAndGroupsChangeFingerprint(t *testing.T) {
	project := copyFixture(t, "extras-groups")
	out := filepath.Join(t.TempDir(), "out")
	fp := func(mut func(*core.Request)) string {
		req := extrasRequest(project, out)
		mut(&req)
		res, err := core.Fingerprint(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return res.Fingerprint
	}
	base := fp(func(*core.Request) {})
	variants := map[string]func(*core.Request){
		"extras":               func(r *core.Request) { r.Extras = []string{"tab"} },
		"groups":               func(r *core.Request) { r.Groups = []string{"tools"} },
		"exclude_packages":     func(r *core.Request) { r.ExcludePackages = []string{"six"} },
		"artifact_type":        func(r *core.Request) { r.ArtifactType = "layer" },
		"install_dependencies": func(r *core.Request) { r.InstallDependencies = false },
	}
	seen := map[string]string{base: "base"}
	for name, mut := range variants {
		got := fp(mut)
		if prev, dup := seen[got]; dup {
			t.Errorf("%s has the same fingerprint as %s", name, prev)
		}
		seen[got] = name
	}
	dup := fp(func(r *core.Request) { r.Extras = []string{"tab", "tab"} })
	one := fp(func(r *core.Request) { r.Extras = []string{"tab"} })
	if dup != one {
		t.Error("duplicate extras changed the fingerprint")
	}
}

func TestExcludePackagesRemovesPackageAndDistInfoViaRecord(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	req := depsRequest(project, filepath.Join(t.TempDir(), "out"))
	req.ExcludePackages = []string{"Six", "not_installed"}
	res, names := buildNames(t, req)

	if slices.Contains(names, "six.py") || hasPrefix(names, "six-1.17.0.dist-info/") {
		t.Errorf("six not removed: %v", names)
	}
	if !hasPrefix(names, "tabulate/") || !hasPrefix(names, "tabulate-0.9.0.dist-info/") {
		t.Error("tabulate must remain")
	}
	if !slices.Contains(names, "handler.py") {
		t.Error("source must remain")
	}
	want := []core.ExcludedPackage{{Name: "six", Version: "1.17.0"}}
	if !slices.Equal(res.ExcludedPackages, want) {
		t.Errorf("ExcludedPackages = %v, want %v", res.ExcludedPackages, want)
	}
}

func TestNothingIsStrippedByDefault(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	res, names := buildNames(t, depsRequest(project, filepath.Join(t.TempDir(), "out")))
	if !slices.Contains(names, "six.py") || len(res.ExcludedPackages) != 0 {
		t.Errorf("default build stripped something: %v %v", names, res.ExcludedPackages)
	}
}

func TestInstallDependenciesFalseShipsOnlySourceWithoutRunningUV(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	req := depsRequest(project, filepath.Join(t.TempDir(), "out"))
	req.InstallDependencies = false
	// A cache dir that cannot exist proves uv is never extracted.
	req.Toolchain.CacheDir = filepath.Join(project, "handler.py", "no-such-dir")
	res, names := buildNames(t, req)
	if !slices.Equal(names, []string{"handler.py"}) {
		t.Errorf("entries = %v, want only handler.py", names)
	}
	if res.UVVersion != "" || res.PythonFullVersion != "" {
		t.Errorf("uv ran: %q %q", res.UVVersion, res.PythonFullVersion)
	}
}
