package core_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// workspaceRequest builds the wsapp member of the workspace fixture, whose
// project_dir is the workspace root (ws/) holding uv.lock. The fixture also
// has vendor/pathdep, a path dependency outside the workspace root.
func workspaceRequest(fixtureDir, outputDir string) core.Request {
	r := depsRequest(filepath.Join(fixtureDir, "ws"), outputDir)
	r.Name = "wsapp"
	r.WorkspaceMember = "wsapp"
	return r
}

func workspaceFingerprint(t *testing.T, req core.Request) string {
	t.Helper()
	res, err := core.Fingerprint(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res.Fingerprint
}

func hasEntryWithPrefix(names []string, prefix string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, prefix) })
}

func TestBuildWorkspaceMemberInstallsSiblingAndPathDependencyAsWheels(t *testing.T) {
	fixture := copyFixture(t, "workspace")
	res, err := core.Build(context.Background(), workspaceRequest(fixture, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	for _, want := range []string{
		"wsapp/__init__.py",
		"wsapp-0.1.0.dist-info/METADATA",
		"wslib/__init__.py",
		"wslib-0.2.0.dist-info/METADATA",
		"pathdep/__init__.py",
		"pathdep-1.0.0.dist-info/METADATA",
		"six.py",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("missing entry %s in %q", want, names)
		}
	}
	for _, unwanted := range []string{"wsdev", "wsother", "wsextra", "tabulate", "packages/", "pyproject.toml"} {
		if hasEntryWithPrefix(names, unwanted) {
			t.Errorf("unexpected entry with prefix %s in %q", unwanted, names)
		}
	}
	for _, n := range names {
		// Installed as wheels, not editable: no .pth pointing at the host tree.
		if strings.HasSuffix(n, ".pth") || strings.HasSuffix(n, "direct_url.json") {
			t.Errorf("unexpected entry %s", n)
		}
	}
}

func TestWorkspaceMemberFingerprintCoversReachableLocalTreesOnly(t *testing.T) {
	cases := []struct {
		name    string
		edit    string // relative to the fixture root
		changes bool
	}{
		{"sibling member source", "ws/packages/wslib/wslib/__init__.py", true},
		{"sibling member new data file", "ws/packages/wslib/wslib/data.txt", true},
		{"path dependency outside workspace", "vendor/pathdep/pathdep/__init__.py", true},
		{"member itself", "ws/packages/wsapp/wsapp/__init__.py", true},
		{"unrelated member", "ws/packages/wsother/wsother/__init__.py", false},
		{"dev-group-only member", "ws/packages/wsdev/wsdev/__init__.py", false},
		{"extra-only member, extra not selected", "ws/packages/wsextra/wsextra/__init__.py", false},
		{"excluded dir in sibling", "ws/packages/wslib/tests/test_x.py", false},
		{"gitignored file in sibling", "ws/packages/wslib/scratch.log", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := copyFixture(t, "workspace")
			writeFile(t, filepath.Join(fixture, "ws", "packages", "wslib", ".gitignore"), "*.log\n")
			req := workspaceRequest(fixture, filepath.Join(t.TempDir(), "out"))
			before := workspaceFingerprint(t, req)
			writeFile(t, filepath.Join(fixture, filepath.FromSlash(tc.edit)), "# edited\n")
			after := workspaceFingerprint(t, req)
			if changed := before != after; changed != tc.changes {
				t.Errorf("editing %s: fingerprint changed = %v, want %v", tc.edit, changed, tc.changes)
			}
		})
	}
}

func TestWorkspaceMemberFingerprintCoversExtraOnlyMemberWhenExtraSelected(t *testing.T) {
	fixture := copyFixture(t, "workspace")
	req := workspaceRequest(fixture, filepath.Join(t.TempDir(), "out"))
	req.Extras = []string{"fancy"}
	before := workspaceFingerprint(t, req)
	writeFile(t, filepath.Join(fixture, "ws", "packages", "wsextra", "wsextra", "__init__.py"), "# edited\n")
	if workspaceFingerprint(t, req) == before {
		t.Error("editing a member reached through a selected extra did not change the fingerprint")
	}
}

func TestBuildWorkspaceMemberWithExtraInstallsExtraMemberAndNeverDevGroup(t *testing.T) {
	fixture := copyFixture(t, "workspace")
	req := workspaceRequest(fixture, filepath.Join(t.TempDir(), "out"))
	req.Extras = []string{"fancy"}
	req.Groups = []string{"dev"} // dev dependencies are always excluded
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	for _, want := range []string{"wsextra/__init__.py", "wsextra-0.3.0.dist-info/METADATA", "tabulate/__init__.py", "wslib/__init__.py"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing entry %s in %q", want, names)
		}
	}
	if hasEntryWithPrefix(names, "wsdev") {
		t.Errorf("dev group member shipped: %q", names)
	}
}

func TestBuildWorkspaceMemberShipsEditedSibling(t *testing.T) {
	fixture := copyFixture(t, "workspace")
	writeFile(t, filepath.Join(fixture, "ws", "packages", "wslib", "wslib", "__init__.py"), "VERSION = 'v2'\n")
	res, err := core.Build(context.Background(), workspaceRequest(fixture, filepath.Join(t.TempDir(), "out")))
	if err != nil {
		t.Fatal(err)
	}
	if got := zipContent(t, res.OutputPath, "wslib/__init__.py"); got != "VERSION = 'v2'\n" {
		t.Errorf("wslib/__init__.py = %q", got)
	}
}

func TestWorkspaceMemberSelectsWhatIsBuilt(t *testing.T) {
	fixture := copyFixture(t, "workspace")
	app := workspaceRequest(fixture, filepath.Join(t.TempDir(), "out"))
	other := app
	other.WorkspaceMember = "wsother"
	if workspaceFingerprint(t, app) == workspaceFingerprint(t, other) {
		t.Fatal("workspace_member did not change the fingerprint")
	}
	res, err := core.Build(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	if !slices.Contains(names, "wsother/__init__.py") || !slices.Contains(names, "wslib/__init__.py") || hasEntryWithPrefix(names, "wsapp") || hasEntryWithPrefix(names, "pathdep") {
		t.Errorf("entries = %q, want wsother and wslib only", names)
	}
}

func TestUnknownWorkspaceMemberIsAnError(t *testing.T) {
	fixture := copyFixture(t, "workspace")
	req := workspaceRequest(fixture, filepath.Join(t.TempDir(), "out"))
	req.WorkspaceMember = "nope"
	_, err := core.Fingerprint(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), `workspace_member "nope"`) {
		t.Fatalf("err = %v, want an error naming the workspace_member", err)
	}
}

func TestVirtualWorkspaceRootRequiresWorkspaceMember(t *testing.T) {
	// The fixture's workspace root has no [project]: there is nothing to
	// build without choosing a member, so plan fails naming the members.
	fixture := copyFixture(t, "workspace")
	req := workspaceRequest(fixture, filepath.Join(t.TempDir(), "out"))
	req.WorkspaceMember = ""
	for op, run := range map[string]func() error{
		"Fingerprint": func() error { _, err := core.Fingerprint(context.Background(), req); return err },
		"Build":       func() error { _, err := core.Build(context.Background(), req); return err },
	} {
		err := run()
		if err == nil || !strings.Contains(err.Error(), "workspace_member") || !strings.Contains(err.Error(), "wsapp") {
			t.Errorf("%s: err = %v, want an error asking for workspace_member and listing the members", op, err)
		}
	}
}

func TestWorkspaceMemberWithoutDependenciesIgnoresLocalTrees(t *testing.T) {
	fixture := copyFixture(t, "workspace")
	req := workspaceRequest(fixture, filepath.Join(t.TempDir(), "out"))
	req.InstallDependencies = false
	before := workspaceFingerprint(t, req)
	writeFile(t, filepath.Join(fixture, "ws", "packages", "wslib", "wslib", "__init__.py"), "# edited\n")
	if workspaceFingerprint(t, req) != before {
		t.Error("a local dependency changed the fingerprint although install_dependencies = false does not ship it")
	}
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(zipEntries(t, res.OutputPath))
	if !slices.Contains(names, "wsapp/__init__.py") || hasEntryWithPrefix(names, "wslib") {
		t.Errorf("entries = %q, want only the wsapp wheel", names)
	}
}
