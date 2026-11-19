package provider_test

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/provider"
)

// Acceptance test helpers (test seam A: the Terraform CLI drives the
// provider in-process through terraform-plugin-testing). Acceptance tests
// only run with TF_ACC=1 (`mise run test:acc`).

// protoV6Factories serves the provider in-process; terraform-plugin-testing
// uses the Terraform binary from TF_ACC_TERRAFORM_PATH (set by
// `mise run test:acc`).
var protoV6Factories = map[string]func() (tfprotov6.ProviderServer, error){
	"lambdabuild": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// copyFixture copies testdata/fixtures/<name> to a fresh temp dir and
// returns its absolute path. Configs must use absolute paths: the
// in-process provider's working directory is the Go package directory, not
// Terraform's working directory.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "fixtures", name)
	dst := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// writeFile overwrites dir/rel with content, for source-change steps.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// zipsIn lists the *.zip base names in dir, sorted.
func zipsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".zip") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// sbomsIn lists the *.cdx.json base names in dir, sorted.
func sbomsIn(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Base(f))
	}
	sort.Strings(out)
	return out
}

// expectZips checks that dir holds exactly the named zips and, because
// sbom defaults to true and an SBOM is deleted with its zip, exactly their
// SBOMs. Configs that set sbom = false must not use it.
func expectZips(t *testing.T, dir string, names ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got := zipsIn(t, dir)
		if !slices.Equal(got, names) {
			return fmt.Errorf("zips in %s: got %v, want %v", dir, got, names)
		}
		want := make([]string, 0, len(names))
		for _, n := range names {
			want = append(want, strings.TrimSuffix(n, ".zip")+".cdx.json")
		}
		if gotSBOM := sbomsIn(t, dir); !slices.Equal(gotSBOM, want) {
			return fmt.Errorf("SBOMs in %s: got %v, want %v", dir, gotSBOM, want)
		}
		return nil
	}
}

// zipPathIn matches an output_path in dir: <dir>/<name>-<16 hex>.zip.
func zipPathIn(dir string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(dir) + `/[^/]+-[0-9a-f]{16}\.zip$`)
}

// stateAttr reads one flatmapped attribute of the resource under test.
func stateAttr(s *terraform.State, name string) (string, error) {
	rs, ok := s.RootModule().Resources["lambdabuild_python_package.test"]
	if !ok {
		return "", fmt.Errorf("resource not in state")
	}
	return rs.Primary.Attributes[name], nil
}

// expectNewArtifactOnly checks that output_path differs from every path in
// *seen, that it is the only zip in dir (superseded zips are deleted) and
// that the recorded hash matches the file. It appends the path to *seen.
func expectNewArtifactOnly(t *testing.T, dir string, seen *[]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		p, err := stateAttr(s, "output_path")
		if err != nil {
			return err
		}
		if slices.Contains(*seen, p) {
			return fmt.Errorf("output_path %s was not expected to repeat; seen %v", p, *seen)
		}
		*seen = append(*seen, p)
		if err := expectZips(t, dir, filepath.Base(p))(s); err != nil {
			return err
		}
		want, err := stateAttr(s, "output_sha256")
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != want {
			return fmt.Errorf("sha256 of %s is %s, state records %s", p, got, want)
		}
		sbomPath, err := stateAttr(s, "sbom_path")
		if err != nil {
			return err
		}
		wantSBOM, err := stateAttr(s, "sbom_sha256")
		if err != nil {
			return err
		}
		sb, err := os.ReadFile(sbomPath)
		if err != nil {
			return err
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(sb)); got != wantSBOM {
			return fmt.Errorf("sha256 of %s is %s, state records %s", sbomPath, got, wantSBOM)
		}
		return nil
	}
}

// hcl renders a lambdabuild_python_package "test" block from attribute
// lines such as `name = "x"`.
func hcl(projectDir, outputDir string, extra ...string) string {
	return fmt.Sprintf(`
resource "lambdabuild_python_package" "test" {
  project_dir          = %q
  output_dir           = %q
  python_version       = "3.12"
  install_dependencies = false
%s
}
`, projectDir, outputDir, strings.Join(extra, "\n"))
}
