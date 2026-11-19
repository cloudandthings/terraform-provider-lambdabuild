// Package provider is the Terraform provider server (terraform-plugin-framework,
// protocol 6). It owns schema, plan modification and CRUD, and reaches the
// build pipeline only through internal/core.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// Address is the provider's registry address.
const Address = "registry.terraform.io/cloudandthings/lambdabuild"

var _ provider.Provider = (*lambdabuildProvider)(nil)

type lambdabuildProvider struct {
	version string
}

// New returns a provider factory for the given release version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &lambdabuildProvider{version: version}
	}
}

func (p *lambdabuildProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "lambdabuild"
	resp.Version = p.version
}

func (p *lambdabuildProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Builds deterministic AWS Lambda zip Artifacts from uv-managed Python projects. " +
			"The process environment (proxies, certificates, index credentials) is passed to uv; there is no env argument.",
		Attributes: map[string]schema.Attribute{
			"cache_dir": schema.StringAttribute{
				Optional:    true,
				Description: "Where uv is extracted and managed Python is installed. Must allow executing binaries. Defaults to the user cache dir + /lambdabuild.",
				Validators:  []validator.String{nonEmpty()},
			},
			"uv_path": schema.StringAttribute{
				Optional:    true,
				Description: "Use an external uv binary instead of the embedded one.",
				Validators:  []validator.String{nonEmpty()},
			},
			"uv_cache_dir": schema.StringAttribute{
				Optional:    true,
				Description: "Wheel and package cache shared across builds. Defaults to uv's own default.",
				Validators:  []validator.String{nonEmpty()},
			},
			"index_url": schema.StringAttribute{
				Optional:    true,
				Description: "Default package index (uv's UV_DEFAULT_INDEX): a URL, or a named index \"name=url\". Must not contain credentials: name the index and set UV_INDEX_<NAME>_USERNAME and UV_INDEX_<NAME>_PASSWORD in the environment.",
				Validators:  []validator.String{namedIndexURL()},
			},
			"extra_index_urls": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Additional package indexes (uv's UV_INDEX): URLs, or named indexes \"name=url\". Must not contain credentials: name the index and set UV_INDEX_<NAME>_USERNAME and UV_INDEX_<NAME>_PASSWORD in the environment.",
				Validators:  []validator.List{each(namedIndexURL())},
			},
			"python_install_mirror": schema.StringAttribute{
				Optional:    true,
				Description: "Mirror for managed Python builds (UV_PYTHON_INSTALL_MIRROR). Must not contain credentials.",
				Validators:  []validator.String{indexURL()},
			},
			"native_tls": schema.BoolAttribute{
				Optional:    true,
				Description: "Trust the OS certificate store (UV_NATIVE_TLS). Default false.",
			},
			"offline": schema.BoolAttribute{
				Optional:    true,
				Description: "Use only the local cache (UV_OFFLINE). Default false.",
			},
		},
	}
}

type providerModel struct {
	CacheDir            types.String `tfsdk:"cache_dir"`
	UVPath              types.String `tfsdk:"uv_path"`
	UVCacheDir          types.String `tfsdk:"uv_cache_dir"`
	IndexURL            types.String `tfsdk:"index_url"`
	ExtraIndexURLs      types.List   `tfsdk:"extra_index_urls"`
	PythonInstallMirror types.String `tfsdk:"python_install_mirror"`
	NativeTLS           types.Bool   `tfsdk:"native_tls"`
	Offline             types.Bool   `tfsdk:"offline"`
}

// Configure maps the provider arguments into a core.ToolchainConfig handed to
// resources. Their behaviour lives in core.
func (p *lambdabuildProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tc := core.ToolchainConfig{
		CacheDir:            m.CacheDir.ValueString(),
		UVPath:              m.UVPath.ValueString(),
		UVCacheDir:          m.UVCacheDir.ValueString(),
		IndexURL:            m.IndexURL.ValueString(),
		PythonInstallMirror: m.PythonInstallMirror.ValueString(),
		NativeTLS:           m.NativeTLS.ValueBool(),
		Offline:             m.Offline.ValueBool(),
	}
	if !m.ExtraIndexURLs.IsNull() && !m.ExtraIndexURLs.IsUnknown() {
		resp.Diagnostics.Append(m.ExtraIndexURLs.ElementsAs(ctx, &tc.ExtraIndexURLs, false)...)
	}
	resp.ResourceData = tc
}

func (p *lambdabuildProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newPythonPackageResource}
}

func (p *lambdabuildProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}
