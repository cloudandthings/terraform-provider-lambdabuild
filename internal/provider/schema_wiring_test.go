package provider_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// testCacheDir mirrors core's: one cache_dir per machine so the managed
// Python downloads once. Override with LAMBDABUILD_TEST_CACHE_DIR.
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

func providerHCL(lines ...string) string {
	return "provider \"lambdabuild\" {\n" + strings.Join(lines, "\n") + "\n}\n"
}

// Every default of the spec is visible in state.
func TestAccPythonPackage_defaults(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{{
			Config: hcl(project, out),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("lock_mode"), knownvalue.StringExact("locked")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("on_missing_artifact"), knownvalue.StringExact("rebuild")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("size_limit_mode"), knownvalue.StringExact("error")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("exclude_packages"), knownvalue.ListSizeExact(0)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("extras"), knownvalue.ListSizeExact(0)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("groups"), knownvalue.ListSizeExact(0)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("workspace_member"), knownvalue.Null()),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("uv_version"), knownvalue.Null()),
			},
		}},
	})
}

// Arguments that feed the Input fingerprint change the plan; lock_mode,
// on_missing_artifact and size_limit_mode never change the Artifact bytes.
func TestAccPythonPackage_fingerprintedAndNot(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: hcl(project, out, `name = "code-only"`)},
			{
				Config: hcl(project, out, `name = "code-only"`, `lock_mode = "frozen"`, `size_limit_mode = "warn"`, `on_missing_artifact = "ignore"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("input_fingerprint"), knownvalue.StringExact(goldenFingerprint)),
					},
				},
			},
			{
				Config: hcl(project, out, `name = "code-only"`, `extras = ["a"]`, `groups = ["g"]`, `exclude_packages = ["boto3"]`,
					`platform_tag = "manylinux_2_28"`, `architecture = "arm64"`, `artifact_type = "layer"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("output_sha256")),
					},
				},
			},
		},
	})
}

// Invalid values are rejected at validate/plan with a message naming the
// attribute.
func TestAccPythonPackage_invalidArguments(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cases := map[string]struct {
		line string
		want string
	}{
		"architecture":        {`architecture = "riscv"`, `architecture`},
		"artifact_type":       {`artifact_type = "image"`, `artifact_type`},
		"lock_mode":           {`lock_mode = "loose"`, `lock_mode`},
		"on_missing_artifact": {`on_missing_artifact = "panic"`, `on_missing_artifact`},
		"size_limit_mode":     {`size_limit_mode = "never"`, `size_limit_mode`},
		"python_version":      {`python_version = "2.7"`, `python_version`},
		"platform_tag":        {`platform_tag = "win32"`, `platform_tag`},
		"name":                {`name = "a/b"`, `name`},
		"empty extra":         {`extras = [""]`, `extras`},
		"empty group":         {`groups = [""]`, `groups`},
		"empty exclude":       {`exclude_packages = [""]`, `exclude_packages`},
		"workspace_member":    {`workspace_member = "m"`, `workspace_member "m": not a member`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := hcl(project, out, c.line)
			if name == "python_version" {
				cfg = strings.Replace(hcl(project, out), `"3.12"`, `"2.7"`, 1)
			}
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{{
					Config:      cfg,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.want),
				}},
			})
		})
	}
}

// python_version is checked at validate time, before any plan: a resource
// with count = 0 is never planned, but its configuration is validated.
func TestAccPythonPackage_pythonVersionValidatedAtValidateTime(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	for _, v := range []string{"3.10", "3.9", "2.7"} {
		t.Run(v, func(t *testing.T) {
			cfg := strings.Replace(hcl(project, out, "count = 0"), `"3.12"`, `"`+v+`"`, 1)
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{{
					Config:      cfg,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`python_version(.|\n)*3\.11, 3\.12, 3\.13, 3\.14`),
				}},
			})
		})
	}
}

// Provider arguments are accepted; there is no env argument.
func TestAccProvider_arguments(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerHCL(
					`cache_dir = `+strconv.Quote(t.TempDir()),
					`uv_path = "/nonexistent/uv"`,
					`uv_cache_dir = `+strconv.Quote(t.TempDir()),
					`index_url = "corp=https://pypi.example.internal/simple"`,
					`extra_index_urls = ["https://extra.example.internal/simple", "extra=https://two.example.internal/simple"]`,
					`python_install_mirror = "https://mirror.example.internal/pbs"`,
					`native_tls = true`,
					`offline = true`,
				) + hcl(project, out),
			},
		},
	})
}

func TestAccProvider_invalidArguments(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cases := map[string]struct{ line, want string }{
		"env":              {`env = { A = "b" }`, `env`},
		"index_url":        {`index_url = "not a url"`, `index_url`},
		"extra_index_urls": {`extra_index_urls = [""]`, `extra_index_urls`},
		"cache_dir":        {`cache_dir = ""`, `cache_dir`},
		// Credentials would be persisted in state and saved plans.
		"index_url credentials":             {`index_url = "https://svc:s3cr3t-pa55@pypi.example.internal/simple"`, `index_url must not contain\s+credentials(.|\n)*UV_INDEX_<NAME>_PASSWORD`},
		"named index_url credentials":       {`index_url = "corp=https://svc:s3cr3t-pa55@pypi.example.internal/simple"`, `index_url must not contain\s+credentials`},
		"extra_index_urls credentials":      {`extra_index_urls = ["https://svc:s3cr3t-pa55@x.example.internal/simple"]`, `extra_index_urls\[0\] must not\s+contain\s+credentials`},
		"python_install_mirror credentials": {`python_install_mirror = "https://svc:s3cr3t-pa55@m.example.internal/"`, `python_install_mirror must\s+not\s+contain\s+credentials`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{{
					Config:      providerHCL(c.line) + hcl(project, out),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.want),
				}},
			})
		})
	}
}

// Dependencies install through the resource and the toolchain versions are
// reported.
func TestAccPythonPackage_withDependencies(t *testing.T) {
	project := copyFixture(t, "pure-deps")
	out := filepath.Join(t.TempDir(), "out")
	cfg := providerHCL(`cache_dir = `+strconv.Quote(testCacheDir())) + `
resource "lambdabuild_python_package" "test" {
  project_dir    = ` + strconv.Quote(project) + `
  output_dir     = ` + strconv.Quote(out) + `
  python_version = "3.12"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{{
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectUnknownValue(addr, tfjsonpath.New("uv_version")),
					plancheck.ExpectUnknownValue(addr, tfjsonpath.New("python_full_version")),
				},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("uv_version"), knownvalue.StringRegexp(regexp.MustCompile(`^\d+\.\d+\.\d+$`))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("python_full_version"), knownvalue.StringRegexp(regexp.MustCompile(`^3\.12\.\d+$`))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("sbom_sha256"), knownvalue.StringExact("5ada0aba3322ff059711cbc50e3c51a9bfdf43d2cb1a243e5c7698ba58394368")),
			},
			Check: func(s *terraform.State) error {
				p, err := stateAttr(s, "output_path")
				if err != nil {
					return err
				}
				zr, err := zip.OpenReader(p)
				if err != nil {
					return err
				}
				defer func() { _ = zr.Close() }()
				seen := map[string]bool{}
				for _, f := range zr.File {
					seen[f.Name] = true
				}
				for _, want := range []string{"six.py", "tabulate/__init__.py", "handler.py"} {
					if !seen[want] {
						return &missingEntry{want}
					}
				}
				return nil
			},
		}},
	})
}

type missingEntry struct{ name string }

func (e *missingEntry) Error() string { return "zip lacks " + e.name }
