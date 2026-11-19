package core

import (
	"path"
	"slices"
	"strings"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/builder/python"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/sbom"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/uvlock"
)

// sbomPackages lists what the SBOM reports: every Package left in staging
// after exclusions, except the project itself (the top-level component).
// Local Packages carry a file: reference and no hash; registry Packages the
// SHA-256 of the locked wheel that was installed.
func (p *buildPlan) sbomPackages(installed []python.Installed) []sbom.Package {
	locals := map[string]string{}
	for _, l := range p.locals {
		locals[python.NormalizeName(l.Name)] = l.Path
	}
	var out []sbom.Package
	for _, in := range installed {
		if p.installProject && in.NormalizedName == p.rootName {
			continue
		}
		pkg := sbom.Package{
			Name:              in.Name,
			Version:           in.Version,
			LicenseExpression: in.LicenseExpression,
			License:           in.License,
		}
		if lp, ok := locals[in.NormalizedName]; ok {
			pkg.LocalPath = lp
		} else {
			pkg.SHA256 = installedWheelHash(p.lock.Wheels(in.Name, in.Version), in.Tags)
		}
		out = append(out, pkg)
	}
	return out
}

// installedWheelHash picks the locked wheel that was installed: the one
// whose filename tags overlap the Tag headers of the installed WHEEL file.
// A Package with a single locked wheel uses it when the tags give no
// answer; otherwise the hash is omitted rather than guessed.
func installedWheelHash(wheels []uvlock.Wheel, tags []string) string {
	for _, w := range wheels {
		if w.SHA256 != "" && slices.ContainsFunc(wheelTags(w.Filename), func(t string) bool { return slices.Contains(tags, t) }) {
			return w.SHA256
		}
	}
	if len(wheels) == 1 {
		return wheels[0].SHA256
	}
	return ""
}

// wheelTags expands the compressed tag triple at the end of a wheel file
// name (name-ver[-build]-py.py-abi.abi-plat.plat.whl) into "py-abi-plat"
// tags.
func wheelTags(filename string) []string {
	parts := strings.Split(strings.TrimSuffix(path.Base(filename), ".whl"), "-")
	if len(parts) < 5 {
		return nil
	}
	py, abi, plat := parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
	var tags []string
	for _, a := range strings.Split(py, ".") {
		for _, b := range strings.Split(abi, ".") {
			for _, c := range strings.Split(plat, ".") {
				tags = append(tags, a+"-"+b+"-"+c)
			}
		}
	}
	return tags
}
