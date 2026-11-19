package provider

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Early (validate-time) checks. core remains the source of truth for what
// is allowed: these only reject the same values sooner, with the attribute
// named, and use core's constants for the enums that core owns.

type oneOfValidator struct{ allowed []string }

// oneOf accepts only the listed strings. Unknown and null pass.
func oneOf(allowed ...string) validator.String { return oneOfValidator{allowed} }

func (v oneOfValidator) Description(context.Context) string {
	return "value must be one of: " + strings.Join(v.allowed, ", ")
}

func (v oneOfValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v oneOfValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !slices.Contains(v.allowed, req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value",
			fmt.Sprintf("%s %q must be one of: %s", req.Path, req.ConfigValue.ValueString(), strings.Join(v.allowed, ", ")))
	}
}

type indexURLValidator struct{ named bool }

// indexURL accepts http, https and file URLs without credentials, and
// rejects empty strings.
func indexURL() validator.String { return indexURLValidator{} }

// namedIndexURL also accepts uv's "name=url" form, which binds the
// per-index credential variables UV_INDEX_<NAME>_USERNAME/_PASSWORD.
func namedIndexURL() validator.String { return indexURLValidator{named: true} }

func (v indexURLValidator) Description(context.Context) string {
	if v.named {
		return "must be an http, https or file URL, optionally named (name=url), without credentials"
	}
	return "must be an http, https or file URL without credentials"
}

func (v indexURLValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v indexURLValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	// The value may carry credentials: never echo it.
	raw := req.ConfigValue.ValueString()
	if i := strings.Index(raw, "="); v.named && i > 0 && !strings.ContainsAny(raw[:i], ":/@") {
		raw = raw[i+1:]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" && u.Scheme != "file" || !slices.Contains([]string{"http", "https", "file"}, u.Scheme) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid URL",
			fmt.Sprintf("%s %s (the value is not shown because it may contain credentials)", req.Path, v.Description(ctx)))
		return
	}
	if u.User != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Credentials in URL",
			fmt.Sprintf("%s must not contain credentials (user:password@): Terraform persists provider arguments in state and plan files. "+
				"Name the index (\"corp=https://...\", or [[tool.uv.index]] name = \"corp\" in pyproject.toml) and set "+
				"UV_INDEX_<NAME>_USERNAME and UV_INDEX_<NAME>_PASSWORD (e.g. UV_INDEX_CORP_PASSWORD) in the environment instead; the value is not shown", req.Path))
	}
}

type eachValidator struct{ inner []validator.String }

// each applies string validators to every element of a list of strings.
func each(inner ...validator.String) validator.List { return eachValidator{inner} }

func (eachValidator) Description(context.Context) string { return "every element is validated" }

func (v eachValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v eachValidator) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for i, e := range req.ConfigValue.Elements() {
		s, ok := e.(basetypes.StringValue)
		if !ok {
			continue
		}
		for _, in := range v.inner {
			r := validator.StringResponse{}
			in.ValidateString(ctx, validator.StringRequest{
				Path:        req.Path.AtListIndex(i),
				ConfigValue: s,
			}, &r)
			resp.Diagnostics.Append(r.Diagnostics...)
		}
	}
}

type nonEmptyValidator struct{}

// nonEmpty rejects the empty string.
func nonEmpty() validator.String { return nonEmptyValidator{} }

func (nonEmptyValidator) Description(context.Context) string { return "must not be empty" }

func (v nonEmptyValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (nonEmptyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() && req.ConfigValue.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", fmt.Sprintf("%s must not be empty", req.Path))
	}
}
