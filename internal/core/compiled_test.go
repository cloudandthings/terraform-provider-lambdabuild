package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

func compiledRequest(projectDir, outputDir string) core.Request {
	r := depsRequest(projectDir, outputDir)
	r.Name = "compiled-deps"
	return r
}

func TestBuildSelectsWheelsForTheRequestedArchitectureFromAnyHost(t *testing.T) {
	for arch, want := range map[string]string{"x86_64": "x86_64", "arm64": "aarch64"} {
		t.Run(arch, func(t *testing.T) {
			project := copyFixture(t, "compiled-deps")
			req := compiledRequest(project, filepath.Join(t.TempDir(), "out"))
			req.Architecture = arch
			res, err := core.Build(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			got := zipContent(t, res.OutputPath, "fakeext/__init__.py")
			if got != "ARCH = '"+want+"'\n" {
				t.Errorf("fakeext/__init__.py = %q, want arch %s", got, want)
			}
		})
	}
}

func TestBuildExplicitPlatformTagOverridesTheDerivedOne(t *testing.T) {
	// The fixture wheels are tagged manylinux_2_28: installable with the
	// derived default (manylinux_2_34) but not with an older explicit tag.
	project := copyFixture(t, "compiled-deps")
	req := compiledRequest(project, filepath.Join(t.TempDir(), "out"))
	if _, err := core.Build(context.Background(), req); err != nil {
		t.Fatalf("default tag: %v", err)
	}
	req.PlatformTag = "manylinux_2_17"
	_, err := core.Build(context.Background(), req)
	if err == nil {
		t.Fatal("manylinux_2_17 cannot install a manylinux_2_28 wheel, but the build succeeded")
	}
	if !strings.Contains(err.Error(), "fakeext") {
		t.Errorf("error should name the Package: %v", err)
	}
}

func TestBuildPlatformTagDefaultsFeedTheFingerprint(t *testing.T) {
	project := copyFixture(t, "compiled-deps")
	base := compiledRequest(project, t.TempDir())
	fp := func(mutate func(*core.Request)) string {
		r := base
		mutate(&r)
		res, err := core.Fingerprint(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return res.Fingerprint
	}
	derived := fp(func(r *core.Request) {})
	if explicit := fp(func(r *core.Request) { r.PlatformTag = "manylinux_2_34" }); explicit != derived {
		t.Error("explicit manylinux_2_34 must equal the derived default for 3.12")
	}
	if fp(func(r *core.Request) { r.PlatformTag = "manylinux_2_28" }) == derived {
		t.Error("a different platform_tag must change the fingerprint")
	}
	if fp(func(r *core.Request) { r.PythonVersion = "3.11" }) == fp(func(r *core.Request) { r.PythonVersion = "3.11"; r.PlatformTag = "manylinux_2_34" }) {
		t.Error("python 3.11 derives manylinux_2_26, which must differ from an explicit manylinux_2_34")
	}
}

func TestBuildFailsFastForPackageWithOnlyASourceDistribution(t *testing.T) {
	project := copyFixture(t, "sdist-only")
	req := depsRequest(project, filepath.Join(t.TempDir(), "out"))
	req.Name = "sdist-only"
	_, err := core.Build(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error for a Package without a wheel")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fakesdist") {
		t.Errorf("error must name the Package: %v", err)
	}
	if !strings.Contains(strings.ToLower(msg), "pin a version that ships wheels") {
		t.Errorf("error must suggest pinning a version that ships wheels: %v", err)
	}
}
