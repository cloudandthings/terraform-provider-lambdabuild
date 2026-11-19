package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

var (
	_ resource.Resource                = (*pythonPackageResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*pythonPackageResource)(nil)
	_ resource.ResourceWithImportState = (*pythonPackageResource)(nil)
	_ resource.ResourceWithConfigure   = (*pythonPackageResource)(nil)
)

// on_missing_artifact modes.
const (
	OnMissingRebuild = "rebuild"
	OnMissingIgnore  = "ignore"
)

type pythonPackageResource struct {
	// toolchain is the provider-level configuration, set by Configure.
	toolchain core.ToolchainConfig
}

func (r *pythonPackageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	tc, ok := req.ProviderData.(core.ToolchainConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("got %T, want core.ToolchainConfig", req.ProviderData))
		return
	}
	r.toolchain = tc
}

func newPythonPackageResource() resource.Resource { return &pythonPackageResource{} }

// pythonPackageModel mirrors the lambdabuild_python_package schema. Arguments
// first, then computed attributes. Keep tfsdk tags equal to the attribute
// names in the technical specification.
type pythonPackageModel struct {
	ProjectDir          types.String `tfsdk:"project_dir"`
	OutputDir           types.String `tfsdk:"output_dir"`
	PythonVersion       types.String `tfsdk:"python_version"`
	Name                types.String `tfsdk:"name"`
	Architecture        types.String `tfsdk:"architecture"`
	ArtifactType        types.String `tfsdk:"artifact_type"`
	PlatformTag         types.String `tfsdk:"platform_tag"`
	WorkspaceMember     types.String `tfsdk:"workspace_member"`
	Extras              types.List   `tfsdk:"extras"`
	Groups              types.List   `tfsdk:"groups"`
	ExcludePackages     types.List   `tfsdk:"exclude_packages"`
	LockMode            types.String `tfsdk:"lock_mode"`
	OnMissingArtifact   types.String `tfsdk:"on_missing_artifact"`
	SizeLimitMode       types.String `tfsdk:"size_limit_mode"`
	InstallDependencies types.Bool   `tfsdk:"install_dependencies"`
	InstallProject      types.Bool   `tfsdk:"install_project"`
	SourceDir           types.String `tfsdk:"source_dir"`
	SourceInclude       types.List   `tfsdk:"source_include"`
	SourceExclude       types.List   `tfsdk:"source_exclude"`
	SBOM                types.Bool   `tfsdk:"sbom"`
	Triggers            types.Map    `tfsdk:"triggers"`

	InputFingerprint types.String `tfsdk:"input_fingerprint"`
	OutputPath       types.String `tfsdk:"output_path"`
	SBOMPath         types.String `tfsdk:"sbom_path"`
	ArtifactPresent  types.Bool   `tfsdk:"artifact_present"`
	buildOutputs
}

// buildOutputs are the computed attributes known only after a build. A plan
// either keeps them from state (inputs unchanged) or marks them all unknown.
type buildOutputs struct {
	OutputSHA256       types.String `tfsdk:"output_sha256"`
	OutputBase64SHA256 types.String `tfsdk:"output_base64sha256"`
	OutputSize         types.Int64  `tfsdk:"output_size"`
	UnpackedSize       types.Int64  `tfsdk:"unpacked_size"`
	SBOMSHA256         types.String `tfsdk:"sbom_sha256"`
	UVVersion          types.String `tfsdk:"uv_version"`
	PythonFullVersion  types.String `tfsdk:"python_full_version"`
}

func (r *pythonPackageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_python_package"
}

func stringList(values []string) types.List {
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.StringValue(v)
	}
	return types.ListValueMust(types.StringType, elems)
}

func (r *pythonPackageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Builds a deterministic Lambda zip Artifact (function or layer) from a uv-managed Python project. " +
			"Plan computes the Input fingerprint; apply builds.",
		Attributes: map[string]schema.Attribute{
			"project_dir": schema.StringAttribute{
				Required:    true,
				Description: "Directory containing pyproject.toml and uv.lock. Relative paths resolve against Terraform's working directory.",
			},
			"output_dir": schema.StringAttribute{
				Required:    true,
				Description: "Directory for the zip and SBOM. Keep it relative so state stays valid across machines.",
			},
			"python_version": schema.StringAttribute{
				Required:    true,
				Validators:  []validator.String{oneOf(core.SupportedPythonVersions...)},
				Description: `Lambda Python runtime version, one of Lambda's non-deprecated Python runtimes compiled into this release: "` + strings.Join(core.SupportedPythonVersions, `", "`) + `".`,
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Description: "Prefix of the zip file name. Defaults to the base name of project_dir.",
			},
			"architecture": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(core.ArchitectureX8664),
				Validators:  []validator.String{oneOf(core.ArchitectureX8664, core.ArchitectureARM64)},
				Description: `"x86_64" or "arm64".`,
			},
			"artifact_type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(core.ArtifactTypeFunction),
				Validators:  []validator.String{oneOf(core.ArtifactTypeFunction, core.ArtifactTypeLayer)},
				Description: `"function", or "layer" to place content under python/.`,
			},
			"platform_tag": schema.StringAttribute{
				Optional:    true,
				Description: "Wheel platform tag. Defaults to manylinux_2_34 for Python 3.12+ and manylinux_2_26 before.",
			},
			"workspace_member": schema.StringAttribute{
				Optional:    true,
				Validators:  []validator.String{nonEmpty()},
				Description: "uv workspace member to build (uv's --package). project_dir is then the workspace root holding uv.lock; the member's pyproject.toml decides install_project, its directory is the default source_dir, and sibling members and path dependencies it uses are fingerprinted and installed as wheels.",
			},
			"extras": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(stringList(nil)),
				Validators:  []validator.List{each(nonEmpty())},
				Description: "Optional-dependency extras to install.",
			},
			"groups": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(stringList(nil)),
				Validators:  []validator.List{each(nonEmpty())},
				Description: "Dependency groups to install (the dev group is never installed unless listed).",
			},
			"exclude_packages": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(stringList(nil)),
				Validators:  []validator.List{each(nonEmpty())},
				Description: "Packages removed after install, such as boto3. Nothing is stripped by default.",
			},
			"lock_mode": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(core.LockModeLocked),
				Validators:  []validator.String{oneOf(core.LockModeLocked, core.LockModeFrozen)},
				Description: `"locked" fails on a stale uv.lock; "frozen" skips that check.`,
			},
			"on_missing_artifact": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString(OnMissingRebuild),
				Validators:    []validator.String{oneOf(OnMissingRebuild, OnMissingIgnore)},
				PlanModifiers: []planmodifier.String{keepStateValue{}},
				Description: `Behaviour when the Artifact is absent on this runner and inputs are unchanged: "rebuild" plans an in-place change to artifact_present only (hashes keep their values) and rebuilds at apply, failing if the hash differs from state; "ignore" plans no change and builds nothing. ` +
					`Changing this argument alone never produces a diff, so a drift-detection job can set "ignore" through a variable against state written with "rebuild" and still get an empty plan; the configured value always decides, and the value in state is the one set at creation.`,
			},
			"size_limit_mode": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(core.SizeLimitModeError),
				Validators:  []validator.String{oneOf(core.SizeLimitModeError, core.SizeLimitModeWarn)},
				Description: `"error" fails a build above Lambda's 250 MB unzipped limit; "warn" builds anyway.`,
			},
			"install_dependencies": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Install third-party Packages. false builds a code-only function Artifact.",
			},
			"install_project": schema.BoolAttribute{
				Optional:    true,
				Description: "Install the project as a wheel. Defaults to true when pyproject.toml has a [build-system] table.",
			},
			"source_dir": schema.StringAttribute{
				Optional:    true,
				Description: "Root for copied source when install_project is false. Defaults to project_dir, or the workspace_member's directory.",
			},
			"source_include": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(stringList(core.DefaultSourceInclude)),
				Description: "Globs of source files to copy when install_project is false.",
			},
			"source_exclude": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(stringList(core.DefaultSourceExclude)),
				Description: "Globs skipped when copying and fingerprinting. A set value replaces the defaults. .gitignore'd paths are also skipped.",
			},
			"sbom": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Write a CycloneDX SBOM beside the zip.",
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values that force a rebuild when they change.",
			},

			"input_fingerprint": schema.StringAttribute{
				Computed:    true,
				Description: "SHA-256 over every build input. Known at plan.",
			},
			"output_path": schema.StringAttribute{
				Computed:    true,
				Description: "<output_dir>/<name>-<fingerprint prefix>.zip. Known at plan.",
			},
			"sbom_path": schema.StringAttribute{
				Computed:    true,
				Description: "<output_dir>/<name>-<fingerprint prefix>.cdx.json; null when sbom is false. Known at plan.",
			},
			"artifact_present": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the zip (and SBOM) exist on this runner and match the stored hashes. Refreshed by Read.",
			},
			"output_sha256": schema.StringAttribute{
				Computed:    true,
				Description: "Hex SHA-256 of the zip.",
			},
			"output_base64sha256": schema.StringAttribute{
				Computed:    true,
				Description: "Base64 SHA-256 of the zip, for aws_lambda_function.source_code_hash.",
			},
			"output_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Zip size in bytes.",
			},
			"unpacked_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Total uncompressed size in bytes.",
			},
			"sbom_sha256": schema.StringAttribute{
				Computed:    true,
				Description: "Hex SHA-256 of the SBOM; null when sbom is false.",
			},
			"uv_version": schema.StringAttribute{
				Computed:    true,
				Description: "uv version used for the build; null when uv did not run.",
			},
			"python_full_version": schema.StringAttribute{
				Computed:    true,
				Description: "Exact managed interpreter version used; null when uv did not run.",
			},
		},
	}
}

func (r *pythonPackageResource) ImportState(_ context.Context, _ resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError(
		"Import is not supported",
		"lambdabuild_python_package has nothing remote to import: declare the resource and run terraform apply to build the Artifact.",
	)
}

// knownInputs reports whether every argument that feeds core is known. When
// one is unknown (for example project_dir from another resource) the plan
// leaves all derived attributes unknown.
func (m *pythonPackageModel) knownInputs() bool {
	vals := []attr.Value{
		m.ProjectDir, m.OutputDir, m.PythonVersion, m.Name, m.Architecture, m.ArtifactType,
		m.PlatformTag, m.InstallDependencies, m.InstallProject, m.SourceDir, m.SBOM,
		m.WorkspaceMember, m.LockMode, m.OnMissingArtifact, m.SizeLimitMode,
	}
	for _, v := range vals {
		if v.IsUnknown() {
			return false
		}
	}
	for _, coll := range []interface {
		attr.Value
		Elements() []attr.Value
	}{m.SourceInclude, m.SourceExclude, m.Extras, m.Groups, m.ExcludePackages} {
		if coll.IsUnknown() {
			return false
		}
		for _, e := range coll.Elements() {
			if e.IsUnknown() {
				return false
			}
		}
	}
	if m.Triggers.IsUnknown() {
		return false
	}
	for _, e := range m.Triggers.Elements() {
		if e.IsUnknown() {
			return false
		}
	}
	return true
}

// request translates the model into a core.Request. Every argument is passed
// explicitly, including the booleans whose core zero value (false) differs
// from the resource default (true).
func (m *pythonPackageModel) request(ctx context.Context, tc core.ToolchainConfig) (core.Request, error) {
	req := core.Request{
		ProjectDir:          m.ProjectDir.ValueString(),
		OutputDir:           m.OutputDir.ValueString(),
		Name:                m.Name.ValueString(),
		PythonVersion:       m.PythonVersion.ValueString(),
		Architecture:        m.Architecture.ValueString(),
		ArtifactType:        m.ArtifactType.ValueString(),
		PlatformTag:         m.PlatformTag.ValueString(),
		InstallDependencies: m.InstallDependencies.ValueBool(),
		SourceDir:           m.SourceDir.ValueString(),
		SBOM:                m.SBOM.ValueBool(),
		WorkspaceMember:     m.WorkspaceMember.ValueString(),
		LockMode:            m.LockMode.ValueString(),
		SizeLimitMode:       m.SizeLimitMode.ValueString(),
		Toolchain:           tc,
	}
	for name, dst := range map[string]*[]string{"extras": &req.Extras, "groups": &req.Groups, "exclude_packages": &req.ExcludePackages} {
		list := map[string]types.List{"extras": m.Extras, "groups": m.Groups, "exclude_packages": m.ExcludePackages}[name]
		if list.IsNull() {
			continue
		}
		*dst = []string{}
		if d := list.ElementsAs(ctx, dst, false); d.HasError() {
			return req, fmt.Errorf("%s: %v", name, d)
		}
	}
	if !m.InstallProject.IsNull() {
		v := m.InstallProject.ValueBool()
		req.InstallProject = &v
	}
	if !m.SourceInclude.IsNull() {
		req.SourceInclude = []string{}
		if d := m.SourceInclude.ElementsAs(ctx, &req.SourceInclude, false); d.HasError() {
			return req, fmt.Errorf("source_include: %v", d)
		}
	}
	if !m.SourceExclude.IsNull() {
		req.SourceExclude = []string{}
		if d := m.SourceExclude.ElementsAs(ctx, &req.SourceExclude, false); d.HasError() {
			return req, fmt.Errorf("source_exclude: %v", d)
		}
	}
	if !m.Triggers.IsNull() {
		req.Triggers = map[string]string{}
		if d := m.Triggers.ElementsAs(ctx, &req.Triggers, false); d.HasError() {
			return req, fmt.Errorf("triggers: %v", d)
		}
	}
	return req, nil
}

// setBuildOutputs records a Build result.
func (m *pythonPackageModel) setBuildOutputs(res core.BuildResult) {
	m.InputFingerprint = types.StringValue(res.Fingerprint)
	m.OutputPath = types.StringValue(res.OutputPath)
	m.SBOMPath = optionalString(res.SBOMPath)
	m.ArtifactPresent = types.BoolValue(true)
	m.buildOutputs = buildOutputs{
		OutputSHA256:       types.StringValue(res.OutputSHA256),
		OutputBase64SHA256: types.StringValue(res.OutputBase64SHA256),
		OutputSize:         types.Int64Value(res.OutputSize),
		UnpackedSize:       types.Int64Value(res.UnpackedSize),
		SBOMSHA256:         optionalString(res.SBOMSHA256),
		UVVersion:          optionalString(res.UVVersion),
		PythonFullVersion:  optionalString(res.PythonFullVersion),
	}
}

// unknownBuildOutputs are the after-build attributes of a plan that builds
// a new Artifact: all unknown, except sbom_sha256 when sbom is false.
func unknownBuildOutputs(sbom types.Bool) buildOutputs {
	o := buildOutputs{
		OutputSHA256:       types.StringUnknown(),
		OutputBase64SHA256: types.StringUnknown(),
		OutputSize:         types.Int64Unknown(),
		UnpackedSize:       types.Int64Unknown(),
		SBOMSHA256:         types.StringUnknown(),
		UVVersion:          types.StringUnknown(),
		PythonFullVersion:  types.StringUnknown(),
	}
	if !sbom.IsUnknown() && !sbom.ValueBool() {
		o.SBOMSHA256 = types.StringNull()
	}
	return o
}

func optionalString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// ModifyPlan computes the Input fingerprint and the paths derived from it.
// Nothing is built at plan (ADR 0001).
//
//   - Fingerprint and paths equal to state: every computed attribute keeps its
//     state value, so an unchanged resource plans no diff (and a diff in a
//     non-fingerprinted argument such as project_dir keeps the hashes known).
//     If Read found the Artifact missing and on_missing_artifact is
//     "rebuild", artifact_present alone changes to true: an in-place update
//     whose hashes stay known, so dependents such as a Lambda see no diff.
//   - Otherwise: new fingerprint and paths, artifact_present = true, and the
//     after-build attributes (hashes, sizes, versions) unknown.
func (r *pythonPackageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy
	}
	var plan pythonPackageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.knownInputs() {
		plan.InputFingerprint = types.StringUnknown()
		plan.OutputPath = types.StringUnknown()
		plan.SBOMPath = types.StringUnknown()
		plan.ArtifactPresent = types.BoolUnknown()
		plan.buildOutputs = unknownBuildOutputs(plan.SBOM)
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
		return
	}

	creq, err := plan.request(ctx, r.toolchain)
	if err != nil {
		resp.Diagnostics.AddError("Invalid lambdabuild_python_package arguments", err.Error())
		return
	}
	fp, err := core.Fingerprint(ctx, creq)
	if err != nil {
		resp.Diagnostics.AddError("Cannot compute the Input fingerprint", err.Error())
		return
	}
	for _, w := range fp.Warnings {
		resp.Diagnostics.AddWarning("lambdabuild: "+w.Code, w.Message)
	}

	plan.InputFingerprint = types.StringValue(fp.Fingerprint)
	plan.OutputPath = types.StringValue(fp.OutputPath)
	plan.SBOMPath = optionalString(fp.SBOMPath)

	var state pythonPackageModel
	unchanged := false
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		unchanged = state.InputFingerprint.Equal(plan.InputFingerprint) &&
			state.OutputPath.Equal(plan.OutputPath) &&
			state.SBOMPath.Equal(plan.SBOMPath)
	}
	if unchanged {
		plan.buildOutputs = state.buildOutputs
		plan.ArtifactPresent = state.ArtifactPresent
		// A missing Artifact with "rebuild": only artifact_present changes,
		// so the hashes stay known and dependents see no diff; Update
		// rebuilds and must reproduce them.
		if !state.ArtifactPresent.ValueBool() && onMissingArtifact(ctx, req.Config) != OnMissingIgnore {
			plan.ArtifactPresent = types.BoolValue(true)
		}
	} else {
		plan.ArtifactPresent = types.BoolValue(true)
		plan.buildOutputs = unknownBuildOutputs(plan.SBOM)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

// build runs core.Build for the planned model and checks the result against
// the plan: the Input fingerprint must not have changed since plan, and
// hashes known at plan (an unchanged fingerprint) must be reproduced.
func (r *pythonPackageResource) build(ctx context.Context, plan *pythonPackageModel, diags *diag.Diagnostics) (string, error) {
	creq, err := plan.request(ctx, r.toolchain)
	if err != nil {
		return "Invalid lambdabuild_python_package arguments", err
	}
	changed := func(got string) error {
		if plan.InputFingerprint.IsUnknown() || plan.InputFingerprint.ValueString() == got {
			return nil
		}
		return fmt.Errorf(
			"the Input fingerprint was %s at plan but is %s at apply: sources or arguments changed after the plan was made; run terraform plan again",
			plan.InputFingerprint.ValueString(), got)
	}
	// Check before building, so unreviewed sources never produce an Artifact.
	fp, err := core.Fingerprint(ctx, creq)
	if err != nil {
		return "Cannot compute the Input fingerprint", err
	}
	if err := changed(fp.Fingerprint); err != nil {
		return "Build inputs changed since plan", err
	}
	res, err := core.Build(ctx, creq)
	if err != nil {
		return "Build failed", err
	}
	for _, w := range res.Warnings {
		diags.AddWarning("lambdabuild: "+w.Code, w.Message)
	}
	if err := changed(res.Fingerprint); err != nil { // changed during the build
		_ = removeArtifact(res.OutputPath, res.SBOMPath)
		return "Build inputs changed since plan", err
	}
	if !plan.OutputSHA256.IsUnknown() && !plan.OutputSHA256.IsNull() && plan.OutputSHA256.ValueString() != res.OutputSHA256 {
		return "Non-reproducible build", fmt.Errorf(
			"rebuilding %s from unchanged inputs produced sha256 %s, but state records %s; the Artifact was left at the new hash",
			res.OutputPath, res.OutputSHA256, plan.OutputSHA256.ValueString())
	}
	if !plan.SBOMSHA256.IsUnknown() && !plan.SBOMSHA256.IsNull() && plan.SBOMSHA256.ValueString() != res.SBOMSHA256 {
		return "Non-reproducible build", fmt.Errorf(
			"rebuilding %s from unchanged inputs produced sha256 %s, but state records %s; the SBOM was left at the new hash",
			res.SBOMPath, res.SBOMSHA256, plan.SBOMSHA256.ValueString())
	}
	plan.setBuildOutputs(res)
	return "", nil
}

func (r *pythonPackageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan pythonPackageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if summary, err := r.build(ctx, &plan, &resp.Diagnostics); err != nil {
		resp.Diagnostics.AddError(summary, err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes artifact_present only. It never removes the resource from
// state: a missing Artifact is rebuilt in place, not replaced.
func (r *pythonPackageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state pythonPackageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	present, err := state.artifact().present()
	if err != nil {
		resp.Diagnostics.AddWarning("Cannot check the Artifact", err.Error())
		present = false
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("artifact_present"), present)...)
}

// needsBuild reports whether an Update must build. An update whose Input
// fingerprint and paths are unchanged (only a non-fingerprinted argument
// such as on_missing_artifact changed) builds nothing when the planned
// Artifact is already on this runner, or when "ignore" planned it absent.
func (r *pythonPackageResource) needsBuild(plan, state pythonPackageModel) bool {
	unchanged := plan.InputFingerprint.Equal(state.InputFingerprint) &&
		plan.OutputPath.Equal(state.OutputPath) &&
		plan.SBOMPath.Equal(state.SBOMPath) &&
		!plan.OutputSHA256.IsUnknown()
	if !unchanged {
		return true
	}
	if !plan.ArtifactPresent.ValueBool() {
		return false // on_missing_artifact = "ignore": nothing runs
	}
	present, err := plan.artifact().present()
	return err != nil || !present
}

func (r *pythonPackageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state pythonPackageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.needsBuild(plan, state) {
		if summary, err := r.build(ctx, &plan, &resp.Diagnostics); err != nil {
			resp.Diagnostics.AddError(summary, err.Error())
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

	// Delete the superseded Artifact on this runner, if any.
	var stale []string
	if !state.OutputPath.Equal(plan.OutputPath) {
		stale = append(stale, state.OutputPath.ValueString())
	}
	if !state.SBOMPath.Equal(plan.SBOMPath) {
		stale = append(stale, state.SBOMPath.ValueString())
	}
	if err := removeArtifact(stale...); err != nil {
		resp.Diagnostics.AddWarning("Cannot remove the superseded Artifact", err.Error())
	}
}

// Delete removes the Artifact if present; it never fails when it is gone.
func (r *pythonPackageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state pythonPackageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := removeArtifact(state.OutputPath.ValueString(), state.SBOMPath.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot remove the Artifact", err.Error())
	}
}

// keepStateValue plans the prior state value for an existing resource, so a
// change of the attribute alone never produces a diff (on_missing_artifact:
// drift detection must plan nothing when only that setting differs).
type keepStateValue struct{}

func (keepStateValue) Description(context.Context) string {
	return "changing this attribute alone never produces a diff"
}

func (m keepStateValue) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (keepStateValue) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return // create: plan the configured value
	}
	resp.PlanValue = req.StateValue
}

// onMissingArtifact is the configured on_missing_artifact. The planned and
// stored value stays at its creation value (keepStateValue), so behaviour
// follows the configuration. Unset or not yet known means the default.
func onMissingArtifact(ctx context.Context, cfg tfsdk.Config) string {
	var v types.String
	if d := cfg.GetAttribute(ctx, path.Root("on_missing_artifact"), &v); d.HasError() || v.IsNull() || v.IsUnknown() {
		return OnMissingRebuild
	}
	return v.ValueString()
}
