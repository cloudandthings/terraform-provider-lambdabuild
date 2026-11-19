package core

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/artifact"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/builder/python"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/fingerprint"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/packager"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/sourcetree"
)

// Input fingerprint of a build plan: output-affecting arguments and the
// source trees they select.

// fingerprintArgs are the output-affecting arguments. JSON field names are
// part of the fingerprint: renaming one changes every fingerprint.
type fingerprintArgs struct {
	PythonVersion       string            `json:"python_version"`
	Architecture        string            `json:"architecture"`
	ArtifactType        string            `json:"artifact_type"`
	PlatformTag         string            `json:"platform_tag"`
	WorkspaceMember     string            `json:"workspace_member"`
	Extras              []string          `json:"extras"`
	Groups              []string          `json:"groups"`
	ExcludePackages     []string          `json:"exclude_packages"`
	InstallDependencies bool              `json:"install_dependencies"`
	InstallProject      bool              `json:"install_project"`
	SourceInclude       []string          `json:"source_include"`
	SourceExclude       []string          `json:"source_exclude"`
	SBOM                bool              `json:"sbom"`
	Triggers            map[string]string `json:"triggers"`
}

// normalizedNames is sortedSet of PEP 503-normalised Package names, so
// "Six" and "six" (which exclude the same Package) fingerprint alike.
func normalizedNames(in []string) []string {
	out := make([]string, len(in))
	for i, n := range in {
		out[i] = python.NormalizeName(n)
	}
	return sortedSet(out)
}

// sortedSet returns a sorted, de-duplicated, non-nil copy, so argument order
// never changes the fingerprint.
func sortedSet(in []string) []string {
	out := append([]string{}, in...)
	slices.Sort(out)
	return slices.Compact(out)
}

func (p *buildPlan) args() fingerprintArgs {
	triggers := p.req.Triggers
	if triggers == nil {
		triggers = map[string]string{}
	}
	return fingerprintArgs{
		PythonVersion:       p.req.PythonVersion,
		Architecture:        p.req.Architecture,
		ArtifactType:        p.req.ArtifactType,
		PlatformTag:         p.platformTag,
		WorkspaceMember:     p.req.WorkspaceMember,
		Extras:              sortedSet(p.req.Extras),
		Groups:              sortedSet(p.req.Groups),
		ExcludePackages:     normalizedNames(p.req.ExcludePackages),
		InstallDependencies: p.req.InstallDependencies,
		InstallProject:      p.installProject,
		SourceInclude:       sortedSet(p.req.SourceInclude),
		SourceExclude:       sortedSet(p.req.SourceExclude),
		SBOM:                p.req.SBOM,
		Triggers:            triggers,
	}
}

// sources are the files the Input fingerprint covers. With install_project
// that is the whole tree of the uv project being built (project_dir, or
// the Workspace member's directory), since any file, e.g. package data, can
// end up in the wheel, minus source_exclude and .gitignore'd paths;
// otherwise the source_include files of source_dir, which also ship.
func (p *buildPlan) sources() ([]sourcetree.File, error) {
	if p.installProject {
		return sourcetree.Select(p.rootDir, []string{"**"}, p.req.SourceExclude)
	}
	return sourcetree.Select(p.req.SourceDir, p.req.SourceInclude, p.req.SourceExclude)
}

func (p *buildPlan) fingerprintWith(ctx context.Context, sources []sourcetree.File) (FingerprintResult, error) {
	if err := ctx.Err(); err != nil {
		return FingerprintResult{}, err
	}
	project := fingerprint.Section{Name: "project"}
	for _, f := range []string{"pyproject.toml", "uv.lock"} {
		project.Files = append(project.Files, fingerprint.File{Rel: f, Abs: filepath.Join(p.req.ProjectDir, f)})
	}
	source := fingerprint.Section{Name: "source"}
	for _, f := range sources {
		source.Files = append(source.Files, fingerprint.File{Rel: f.Rel, Abs: f.Abs})
	}

	locals, err := p.localSections()
	if err != nil {
		return FingerprintResult{}, err
	}

	fp, err := fingerprint.Compute(fingerprint.Input{
		FormatVersion: packager.FormatVersion,
		Args:          p.args(),
		Sections:      append([]fingerprint.Section{project, source}, locals...),
	})
	if err != nil {
		return FingerprintResult{}, err
	}

	res := FingerprintResult{
		Fingerprint: fp.Hex,
		OutputPath:  artifact.ZipPath(p.req.OutputDir, p.req.Name, fp.Hex),
	}
	if p.req.SBOM {
		res.SBOMPath = artifact.SBOMPath(p.req.OutputDir, p.req.Name, fp.Hex)
	}
	if len(fp.CRLF) > 0 {
		res.Warnings = append(res.Warnings, Warning{
			Code: WarningCRLF,
			Message: fmt.Sprintf("source files contain CRLF line endings, so the Input fingerprint differs from an LF checkout of the same commit: %s. Set `* text=auto eol=lf` in .gitattributes.",
				strings.Join(fp.CRLF, ", ")),
			Paths: fp.CRLF,
		})
	}
	return res, nil
}

// localSections fingerprint each local dependency tree the build installs,
// one section per dependency named "local <uv.lock path>", with the same exclude
// and .gitignore rules as the project tree. File paths are prefixed with
// the uv.lock path so warnings name them unambiguously. A path source that
// is a file (a wheel or sdist) is hashed as that one file.
func (p *buildPlan) localSections() ([]fingerprint.Section, error) {
	var sections []fingerprint.Section
	for _, l := range p.locals {
		s := fingerprint.Section{Name: "local " + l.Path}
		abs := lockPath(p.req.ProjectDir, l.Path)
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("local dependency %s: %w", l.Name, err)
		}
		if !info.IsDir() {
			s.Files = []fingerprint.File{{Rel: l.Path, Abs: abs}}
			sections = append(sections, s)
			continue
		}
		files, err := sourcetree.Select(abs, []string{"**"}, p.req.SourceExclude)
		if err != nil {
			return nil, fmt.Errorf("local dependency %s: %w", l.Name, err)
		}
		for _, f := range files {
			s.Files = append(s.Files, fingerprint.File{Rel: path.Join(l.Path, f.Rel), Abs: f.Abs})
		}
		sections = append(sections, s)
	}
	return sections, nil
}

func (p *buildPlan) fingerprint(ctx context.Context) (FingerprintResult, error) {
	sources, err := p.sources()
	if err != nil {
		return FingerprintResult{}, err
	}
	return p.fingerprintWith(ctx, sources)
}
