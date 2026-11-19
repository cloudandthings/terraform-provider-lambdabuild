package provider_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const addr = "lambdabuild_python_package.test"

// Golden values for testdata/fixtures/code-only with name "code-only",
// python 3.12, x86_64 function; the same values pin core's golden test
// (internal/core/golden_sbom_test.go; sbom = true is the default), so the provider must pass the resource
// defaults through to core unchanged.
const (
	goldenFingerprint = "0915df4890f73648dd8fe43ba49d825c35732f39c5edab56a741bbf2b8355282"
	goldenZipSHA256   = "cc5f921b541dbd86ccac391910d012f07babc87b31b0cc785738b6955422c428"
	goldenZipBase64   = "zF+SG1QdvYbMrDkZENAS8HuryHsxsMx4Vzi2lVQixCg="
	goldenZipName     = "code-only-0915df4890f73648.zip"
	goldenSBOMName    = "code-only-0915df4890f73648.cdx.json"
	goldenSBOMSHA256  = "6aec11deeb3e28fcb2a6a020b961aed69d9363c5574d9a257d3ad4654a168504"
)

func TestAccPythonPackage_codeOnlyCreate(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	outputPath := out + "/" + goldenZipName

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: hcl(project, out, `name = "code-only"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("input_fingerprint"), knownvalue.StringExact(goldenFingerprint)),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("output_path"), knownvalue.StringExact(outputPath)),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("sbom_path"), knownvalue.StringExact(strings.TrimSuffix(outputPath, ".zip")+".cdx.json")),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("sbom_sha256")),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("output_sha256")),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("output_base64sha256")),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("output_size")),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("unpacked_size")),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("output_sha256"), knownvalue.StringExact(goldenZipSHA256)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("output_base64sha256"), knownvalue.StringExact(goldenZipBase64)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("sbom_sha256"), knownvalue.StringExact(goldenSBOMSHA256)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("output_size"), knownvalue.Int64Exact(951)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("unpacked_size"), knownvalue.Int64Exact(230)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("artifact_present"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("architecture"), knownvalue.StringExact("x86_64")),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("artifact_type"), knownvalue.StringExact("function")),
				},
				Check: expectZips(t, out, goldenZipName),
			},
		},
	})
}

func TestAccPythonPackage_lifecycle(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := hcl(project, out, `name = "code-only"`)
	cfgTriggers := hcl(project, out, `name = "code-only"`, `triggers = { release = "2" }`)
	paths := []string{out + "/" + goldenZipName}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy:             expectZips(t, out),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				// Unchanged inputs: nothing to do.
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// A source edit is an in-place update with a new fingerprint
				// and path; hashes are unknown until apply and the
				// superseded zip is deleted.
				PreConfig: func() {
					writeFile(t, project, "app/handlers/greet.py", "def greet(name):\n    return f\"Hi {name}\"\n")
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("output_path"), knownvalue.StringRegexp(zipPathIn(out))),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("output_sha256")),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("output_base64sha256")),
					},
				},
				Check: expectNewArtifactOnly(t, out, &paths),
			},
			{
				// A triggers change rebuilds too.
				Config: cfgTriggers,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
						plancheck.ExpectUnknownValue(addr, tfjsonpath.New("output_sha256")),
					},
				},
				Check: expectNewArtifactOnly(t, out, &paths),
			},
		},
	})
}

// sbom = false writes no SBOM and leaves the SBOM attributes null.
func TestAccPythonPackage_sbomDisabled(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{{
			Config: hcl(project, out, `name = "code-only"`, `sbom = false`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("sbom_path"), knownvalue.Null()),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("sbom_sha256"), knownvalue.Null()),
			},
			Check: func(*terraform.State) error {
				if _, err := os.Stat(out); err != nil {
					return err
				}
				sboms, _ := filepath.Glob(filepath.Join(out, "*.cdx.json"))
				if len(sboms) != 0 {
					return fmt.Errorf("unexpected SBOM files %v", sboms)
				}
				return nil
			},
		}},
	})
}

func TestAccPythonPackage_importUnsupported(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config:        hcl(project, out),
				ResourceName:  addr,
				ImportState:   true,
				ImportStateId: "anything",
				ExpectError:   regexp.MustCompile(`Import is not supported`),
			},
		},
	})
}
