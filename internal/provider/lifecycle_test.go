package provider_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// Missing-Artifact and saved-plan lifecycle (spec "Behaviour by situation").

// lambdaAddr is a stand-in for aws_lambda_function: a built-in
// terraform_data resource that consumes the Artifact's path and hash, so a
// plan that would change the Lambda's code shows up as a diff on it.
const lambdaAddr = "terraform_data.lambda"

const lambdaHCL = `
resource "terraform_data" "lambda" {
  input = {
    filename         = lambdabuild_python_package.test.output_path
    source_code_hash = lambdabuild_python_package.test.output_base64sha256
  }
}
`

// withLambda appends the stand-in Lambda to a resource config.
func withLambda(cfg string) string { return cfg + lambdaHCL }

// expectOnlyChanged checks that the planned change to addr touches exactly
// the named attributes; every other attribute is known and equal to state.
func expectOnlyChanged(addr string, attrs ...string) plancheck.PlanCheck {
	return onlyChanged{addr: addr, attrs: attrs}
}

type onlyChanged struct {
	addr  string
	attrs []string
}

func (c onlyChanged) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	for _, rc := range req.Plan.ResourceChanges {
		if rc.Address != c.addr {
			continue
		}
		before, _ := rc.Change.Before.(map[string]any)
		after, _ := rc.Change.After.(map[string]any)
		unknown, _ := rc.Change.AfterUnknown.(map[string]any)
		keys := map[string]bool{}
		for k := range before {
			keys[k] = true
		}
		for k := range after {
			keys[k] = true
		}
		var changed []string
		for k := range keys {
			if u, _ := unknown[k].(bool); u || !reflect.DeepEqual(before[k], after[k]) {
				changed = append(changed, k)
			}
		}
		sort.Strings(changed)
		want := slices.Clone(c.attrs)
		sort.Strings(want)
		if !slices.Equal(changed, want) {
			resp.Error = fmt.Errorf("%s: planned changes to %v, want only %v", c.addr, changed, want)
		}
		return
	}
	resp.Error = fmt.Errorf("%s: no planned change", c.addr)
}

// expectArtifactMatchesState checks that the zip and SBOM recorded in state
// exist and hash to the recorded values.
func expectArtifactMatchesState(t *testing.T) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for _, pair := range [][2]string{{"output_path", "output_sha256"}, {"sbom_path", "sbom_sha256"}} {
			p, err := stateAttr(s, pair[0])
			if err != nil {
				return err
			}
			want, err := stateAttr(s, pair[1])
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if got := sha256Hex(b); got != want {
				return fmt.Errorf("sha256 of %s is %s, state records %s", p, got, want)
			}
		}
		return nil
	}
}

// remove deletes files, failing the test on error.
func remove(t *testing.T, paths ...string) func() {
	return func() {
		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// With the default on_missing_artifact = "rebuild", a missing zip plans an
// in-place change to artifact_present only: the hashes keep their state
// values, so the Lambda sees no diff. Apply rebuilds the same bytes.
func TestAccPythonPackage_missingZipRebuilds(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := withLambda(hcl(project, out, `name = "code-only"`))
	missingRebuildSteps(t, cfg, out, remove(t, filepath.Join(out, goldenZipName)))
}

func missingRebuildSteps(t *testing.T, cfg, out string, makeMissing func()) {
	t.Helper()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy:             expectZips(t, out),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				// Read keeps the resource and records the absence.
				PreConfig:          makeMissing,
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				Check:              resource.TestCheckResourceAttr(addr, "artifact_present", "false"),
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
						expectOnlyChanged(addr, "artifact_present"),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("artifact_present"), knownvalue.Bool(true)),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("output_sha256"), knownvalue.StringExact(goldenZipSHA256)),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("output_base64sha256"), knownvalue.StringExact(goldenZipBase64)),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("sbom_sha256"), knownvalue.StringExact(goldenSBOMSHA256)),
						plancheck.ExpectResourceAction(lambdaAddr, plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					expectZips(t, out, goldenZipName),
					expectArtifactMatchesState(t),
					resource.TestCheckResourceAttr(addr, "artifact_present", "true"),
				),
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func sha256Hex(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// betweenPlanAndApply runs fn after the step's plan has been saved and
// before Terraform applies that saved plan. terraform-plugin-testing
// always applies the saved plan file, and PreApply plan checks run in
// between, so this is where a test changes the runner.
func betweenPlanAndApply(fn func()) plancheck.PlanCheck { return hook(fn) }

type hook func()

func (h hook) CheckPlan(context.Context, plancheck.CheckPlanRequest, *plancheck.CheckPlanResponse) {
	h()
}

// freshRunner removes the output directory, as on a CI runner that applies
// a saved plan made elsewhere: none of the plan runner's Artifacts exist.
func freshRunner(t *testing.T, out string) func() {
	return func() {
		if err := os.RemoveAll(out); err != nil {
			t.Fatal(err)
		}
	}
}

// A saved plan made on one runner applies on a fresh one: the new
// Artifact is built there, and the superseded one, absent on that runner,
// does not fail the apply.
func TestAccPythonPackage_savedPlanAppliedOnFreshRunner(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := withLambda(hcl(project, out, `name = "code-only"`))
	paths := []string{filepath.Join(out, goldenZipName)}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy:             expectZips(t, out),
		Steps: []resource.TestStep{
			{
				// First apply on a runner that never saw the plan.
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{betweenPlanAndApply(freshRunner(t, out))},
				},
				Check: expectZips(t, out, goldenZipName),
			},
			{
				// Source change planned where the old Artifact exists,
				// applied where it does not.
				PreConfig: func() {
					writeFile(t, project, "app/handlers/greet.py", "def greet(name):\n    return f\"Hi {name}\"\n")
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction(lambdaAddr, plancheck.ResourceActionUpdate),
						betweenPlanAndApply(freshRunner(t, out)),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					expectNewArtifactOnly(t, out, &paths),
					resource.TestCheckResourceAttr(addr, "artifact_present", "true"),
				),
			},
			{
				// A missing-Artifact rebuild planned on one runner also
				// applies on another.
				PreConfig: freshRunner(t, out),
				Config:    cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						expectOnlyChanged(addr, "artifact_present"),
						plancheck.ExpectResourceAction(lambdaAddr, plancheck.ResourceActionNoop),
						betweenPlanAndApply(freshRunner(t, out)),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					expectArtifactMatchesState(t),
					resource.TestCheckResourceAttr(addr, "artifact_present", "true"),
				),
			},
		},
	})
}

// Apply recomputes the Input fingerprint before building and refuses
// sources that changed after the saved plan was made, leaving no Artifact
// for the unplanned sources behind (CheckDestroy: nothing left after
// destroy).
//
// Terraform re-plans every resource at apply and compares the result with
// the saved plan, and the provider cannot see the saved plan there, so the
// failure Terraform reports is its own consistency error naming the
// changed input_fingerprint; the provider's "Build inputs changed since
// plan" check only catches a change after that re-plan.
func TestAccPythonPackage_sourcesChangedSincePlan(t *testing.T) {
	changedSincePlan := regexp.MustCompile(`invalid new value\s+for \.input_fingerprint: was`)
	edit := func(t *testing.T, project, body string) func() {
		return func() { writeFile(t, project, "app/handlers/greet.py", body) }
	}

	t.Run("create", func(t *testing.T) {
		project := copyFixture(t, "code-only")
		out := filepath.Join(t.TempDir(), "out")
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6Factories,
			CheckDestroy:             expectZips(t, out),
			Steps: []resource.TestStep{{
				Config: withLambda(hcl(project, out, `name = "code-only"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{betweenPlanAndApply(edit(t, project, "def greet(name):\n    return 1\n"))},
				},
				ExpectError: changedSincePlan,
			}},
		})
	})

	t.Run("update", func(t *testing.T) {
		project := copyFixture(t, "code-only")
		out := filepath.Join(t.TempDir(), "out")
		cfg := withLambda(hcl(project, out, `name = "code-only"`))
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6Factories,
			CheckDestroy:             expectZips(t, out),
			Steps: []resource.TestStep{
				{Config: cfg},
				{
					PreConfig: edit(t, project, "def greet(name):\n    return 2\n"),
					Config:    cfg,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
							betweenPlanAndApply(edit(t, project, "def greet(name):\n    return 3\n")),
						},
					},
					ExpectError: changedSincePlan,
				},
			},
		})
	})

	t.Run("missing artifact rebuild", func(t *testing.T) {
		project := copyFixture(t, "code-only")
		out := filepath.Join(t.TempDir(), "out")
		cfg := withLambda(hcl(project, out, `name = "code-only"`))
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6Factories,
			CheckDestroy:             expectZips(t, out),
			Steps: []resource.TestStep{
				{Config: cfg},
				{
					PreConfig: freshRunner(t, out),
					Config:    cfg,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							expectOnlyChanged(addr, "artifact_present"),
							betweenPlanAndApply(edit(t, project, "def greet(name):\n    return 4\n")),
						},
					},
					ExpectError: changedSincePlan,
				},
			},
		})
	})
}

// editBeforeApply serves the provider but runs edit just before every
// ApplyResourceChange, after Terraform's apply-time re-plan: the window in
// which only the provider's own check can notice changed sources.
func editBeforeApply(edit func()) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"lambdabuild": func() (tfprotov6.ProviderServer, error) {
			s, err := protoV6Factories["lambdabuild"]()
			return editingServer{ProviderServer: s, edit: edit}, err
		},
	}
}

type editingServer struct {
	tfprotov6.ProviderServer
	edit func()
}

func (s editingServer) ApplyResourceChange(ctx context.Context, req *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error) {
	s.edit()
	return s.ProviderServer.ApplyResourceChange(ctx, req)
}

// Sources changed after Terraform's apply-time re-plan are caught by the
// provider before it builds: apply fails with "Build inputs changed since
// plan" and writes no Artifact for the unreviewed sources.
func TestAccPythonPackage_sourcesChangedJustBeforeBuild(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	n := 0
	edit := func() {
		n++
		writeFile(t, project, "app/handlers/greet.py", fmt.Sprintf("def greet(name):\n    return %d\n", n))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: editBeforeApply(edit),
		Steps: []resource.TestStep{{
			Config:      hcl(project, out, `name = "code-only"`),
			ExpectError: regexp.MustCompile(`Build inputs changed since plan`),
		}},
	})
	if err := expectZips(t, out)(nil); err != nil {
		t.Errorf("Artifact of unplanned sources left behind: %v", err)
	}
}

// recordedElsewhere serves the provider but records sha as output_sha256
// when a resource is created, as if the state had been written by a host
// whose build produced different bytes.
func recordedElsewhere(sha string) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"lambdabuild": func() (tfprotov6.ProviderServer, error) {
			s, err := protoV6Factories["lambdabuild"]()
			return otherHostState{ProviderServer: s, sha: sha}, err
		},
	}
}

type otherHostState struct {
	tfprotov6.ProviderServer
	sha string
}

func (s otherHostState) ApplyResourceChange(ctx context.Context, req *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error) {
	resp, err := s.ProviderServer.ApplyResourceChange(ctx, req)
	if err != nil || resp.NewState == nil || len(resp.Diagnostics) > 0 {
		return resp, err
	}
	schemas, err := s.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		return nil, err
	}
	typ := schemas.ResourceSchemas[req.TypeName].ValueType()
	prior, err := req.PriorState.Unmarshal(typ)
	if err != nil || !prior.IsNull() {
		return resp, err // only a create is recorded "elsewhere"
	}
	v, err := resp.NewState.Unmarshal(typ)
	if err != nil {
		return nil, err
	}
	shaPath := tftypes.NewAttributePath().WithAttributeName("output_sha256")
	v, err = tftypes.Transform(v, func(p *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		if p.Equal(shaPath) {
			return tftypes.NewValue(tftypes.String, s.sha), nil
		}
		return v, nil
	})
	if err != nil {
		return nil, err
	}
	dv, err := tfprotov6.NewDynamicValue(typ, v)
	if err != nil {
		return nil, err
	}
	resp.NewState = &dv
	return resp, nil
}

// A rebuild of a missing Artifact that does not reproduce the hash in
// state fails as a non-reproducible build naming both hashes, instead of
// silently changing the Lambda.
func TestAccPythonPackage_nonReproducibleRebuild(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := withLambda(hcl(project, out, `name = "code-only"`))
	const recorded = "0000000000000000000000000000000000000000000000000000000000000000"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: recordedElsewhere(recorded),
		CheckDestroy:             expectZips(t, out),
		Steps: []resource.TestStep{
			// This host's zip never matches the recorded hash.
			{Config: cfg, ExpectNonEmptyPlan: true},
			{
				// The zip on disk does not match the recorded hash, so the
				// Artifact is missing and a rebuild is planned.
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						expectOnlyChanged(addr, "artifact_present"),
						plancheck.ExpectKnownValue(addr, tfjsonpath.New("output_sha256"), knownvalue.StringExact(recorded)),
						plancheck.ExpectResourceAction(lambdaAddr, plancheck.ResourceActionNoop),
					},
				},
				ExpectError: regexp.MustCompile(`Non-reproducible build(.|\n)*` + goldenZipSHA256 + `(.|\n)*` + recorded),
			},
		},
	})
}

// Drift detection (story 29): the scheduled job sets on_missing_artifact =
// "ignore" through a variable against state written by the deploy pipeline
// with the default "rebuild", on a runner without the Artifact. The plan is
// empty (terraform plan -detailed-exitcode exits 0): changing
// on_missing_artifact alone never produces a diff, and nothing is built.
func TestAccPythonPackage_driftDetectionWithIgnoreViaVariablePlansNothing(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := withLambda(`
variable "on_missing_artifact" {
  type    = string
  default = "rebuild"
}
` + hcl(project, out, `name = "code-only"`, `on_missing_artifact = var.on_missing_artifact`))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy:             expectZips(t, out),
		Steps: []resource.TestStep{
			{Config: cfg}, // the deploy pipeline
			{
				PreConfig:       freshRunner(t, out), // the drift-detection runner
				Config:          cfg,
				ConfigVariables: config.Variables{"on_missing_artifact": config.StringVariable("ignore")},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeTestCheckFunc(
					expectZips(t, out),
					resource.TestCheckResourceAttr(addr, "artifact_present", "false"),
				),
			},
			{
				// A real change still plans normally under "ignore".
				PreConfig:       func() { writeFile(t, project, "app/handlers/greet.py", "def greet(name):\n    return 5\n") },
				Config:          cfg,
				ConfigVariables: config.Variables{"on_missing_artifact": config.StringVariable("ignore")},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction(lambdaAddr, plancheck.ResourceActionUpdate),
					},
				},
			},
		},
	})
}

// Switching an existing resource between "rebuild" and "ignore" never
// produces a diff; the configured value decides what a missing Artifact
// plans. Back on "rebuild", the missing Artifact plans its rebuild.
func TestAccPythonPackage_onMissingArtifactChangesProduceNoDiff(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	rebuild := withLambda(hcl(project, out, `name = "code-only"`))
	ignore := withLambda(hcl(project, out, `name = "code-only"`, `on_missing_artifact = "ignore"`))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy:             expectZips(t, out),
		Steps: []resource.TestStep{
			{Config: rebuild},
			{
				Config: ignore, // Artifact present: nothing to do
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				PreConfig: freshRunner(t, out),
				Config:    ignore,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: rebuild,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						expectOnlyChanged(addr, "artifact_present"),
						plancheck.ExpectResourceAction(lambdaAddr, plancheck.ResourceActionNoop),
					},
				},
				Check: expectZips(t, out, goldenZipName),
			},
		},
	})
}

// The SBOM is part of the Artifact: losing it alone plans the same
// artifact_present-only rebuild.
func TestAccPythonPackage_missingSBOMRebuilds(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := withLambda(hcl(project, out, `name = "code-only"`))
	missingRebuildSteps(t, cfg, out, remove(t, filepath.Join(out, goldenSBOMName)))
}

// A zip whose bytes no longer match the recorded hash is not the Artifact.
func TestAccPythonPackage_tamperedZipRebuilds(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := withLambda(hcl(project, out, `name = "code-only"`))
	missingRebuildSteps(t, cfg, out, func() { writeFile(t, out, goldenZipName, "not the artifact") })
}

// on_missing_artifact = "ignore": a missing Artifact plans no diff and
// apply runs nothing, so the zip stays missing.
func TestAccPythonPackage_missingArtifactIgnored(t *testing.T) {
	project := copyFixture(t, "code-only")
	out := filepath.Join(t.TempDir(), "out")
	cfg := withLambda(hcl(project, out, `name = "code-only"`, `on_missing_artifact = "ignore"`))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy:             expectZips(t, out),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: remove(t, filepath.Join(out, goldenZipName), filepath.Join(out, goldenSBOMName)),
				Config:    cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeTestCheckFunc(
					expectZips(t, out),
					resource.TestCheckResourceAttr(addr, "artifact_present", "false"),
					resource.TestCheckResourceAttr(addr, "output_sha256", goldenZipSHA256),
				),
			},
		},
	})
}
