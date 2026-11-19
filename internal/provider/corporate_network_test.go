package provider_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// plancheckNoSecret fails when the plan JSON contains secret.
type plancheckNoSecret struct{ secret string }

func (c plancheckNoSecret) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	b, err := json.Marshal(req.Plan)
	if err != nil {
		resp.Error = err
		return
	}
	if strings.Contains(string(b), c.secret) {
		resp.Error = errors.New("plan JSON contains the index password")
	}
}

// Air-gapped acceptance: a private index (basic auth, credentials only in
// the process environment) serves the packages once; afterwards the index is
// gone, every proxy points at a closed port, and the resource still builds
// with offline = true and lock_mode = "frozen" from the primed caches.
// Credentials never reach state or the plan.
func TestAccPythonPackage_airGappedFromPrimedCaches(t *testing.T) {
	const user, pass = "svc-builder", "s3cr3t-pa55"
	root := filepath.Join("..", "..", "testdata", "fixtures", "pure-deps")
	files := http.FileServer(http.Dir(root))
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="corp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer idx.Close()
	t.Setenv("UV_INDEX_CORP_USERNAME", user)
	t.Setenv("UV_INDEX_CORP_PASSWORD", pass)

	project := t.TempDir()
	writeFile(t, project, "handler.py", "def handler(event, context):\n    return 1\n")
	writeFile(t, project, "pyproject.toml", `[project]
name = "corp-deps"
version = "0.1.0"
requires-python = ">=3.12"
dependencies = ["six==1.17.0", "tabulate==0.9.0"]

[[tool.uv.index]]
name = "corp"
url = "`+idx.URL+`/simple"
default = true
`)
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("no uv on PATH (run under mise)")
	}
	lock := exec.Command(uv, "lock")
	lock.Dir = project
	lock.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir(), "UV_NO_SYSTEM_CONFIG=1", "UV_CACHE_DIR="+t.TempDir(),
		"UV_PYTHON_INSTALL_DIR="+filepath.Join(testCacheDir(), "python"), "UV_PYTHON_PREFERENCE=only-managed")
	if out, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("uv lock: %v\n%s", err, out)
	}

	out := filepath.Join(t.TempDir(), "out")
	uvCache := t.TempDir()
	cfg := func(provider, trigger string) string {
		return providerHCL(`cache_dir = `+strconv.Quote(testCacheDir()), `uv_cache_dir = `+strconv.Quote(uvCache), provider) + `
resource "lambdabuild_python_package" "test" {
  project_dir    = ` + strconv.Quote(project) + `
  output_dir     = ` + strconv.Quote(out) + `
  python_version = "3.12"
  triggers       = { run = "` + trigger + `" }
` + extraResourceLines(provider) + `}
`
	}
	noSecrets := func(s *terraform.State) error {
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), pass) {
			return errors.New("state contains the index password")
		}
		return nil
	}
	hasPackages := func(s *terraform.State) error {
		p, err := stateAttr(s, "output_path")
		if err != nil {
			return err
		}
		return expectEntries(p, "six.py", "tabulate/__init__.py", "handler.py")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config:           cfg("", "online"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheckNoSecret{pass}}},
				Check:            resource.ComposeTestCheckFunc(hasPackages, noSecrets),
			},
			{
				PreConfig: func() {
					idx.Close() // the index is unreachable from now on
					for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
						t.Setenv(k, "http://127.0.0.1:1")
					}
				},
				Config:           cfg("offline = true", "air-gapped"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheckNoSecret{pass}}},
				Check:            resource.ComposeTestCheckFunc(hasPackages, noSecrets),
			},
		},
	})
}

// extraResourceLines puts lock_mode = "frozen" on the resource whenever the
// provider is offline, as an air-gapped deployment would.
func extraResourceLines(provider string) string {
	if strings.Contains(provider, "offline") {
		return "  lock_mode      = \"frozen\"\n"
	}
	return ""
}

func expectEntries(zipPath string, names ...string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	seen := map[string]bool{}
	for _, f := range zr.File {
		seen[f.Name] = true
	}
	for _, n := range names {
		if !seen[n] {
			return &missingEntry{n}
		}
	}
	return nil
}
