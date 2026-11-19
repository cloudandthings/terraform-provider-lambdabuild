package core

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/builder/python"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/uvlock"
)

// SupportedPythonVersions are Lambda's non-deprecated Python runtimes
// compiled into this release. A new runtime ships as a patch release.
var SupportedPythonVersions = []string{"3.11", "3.12", "3.13", "3.14"}

// buildPlan is a validated Request with every default resolved. Both
// Fingerprint and Build start from it so they can never disagree.
type buildPlan struct {
	req            Request
	installProject bool
	platformTag    string
	// rootDir holds the pyproject.toml of the uv project being built: the
	// Workspace member's directory, or ProjectDir.
	rootDir string
	// locals are the local dependencies (sibling Workspace members, path
	// dependencies) reachable from the root in uv.lock that the build
	// installs. Empty unless install_dependencies.
	locals []uvlock.Local
	// excluded is filled by installPackages with the Packages that
	// exclude_packages removed.
	excluded []ExcludedPackage
	// installed lists the Packages left in staging, for the SBOM.
	installed []python.Installed
	// lock is the parsed uv.lock; rootName the normalized name of the uv
	// project being built.
	lock     *uvlock.Lock
	rootName string
}

var platformTagPattern = regexp.MustCompile(`^manylinux(_[0-9]+_[0-9]+|2014|2010|1)$`)

func plan(req Request) (*buildPlan, error) {
	var errs []error
	if req.ProjectDir == "" {
		errs = append(errs, errors.New("project_dir is required"))
	}
	if req.OutputDir == "" {
		errs = append(errs, errors.New("output_dir is required"))
	}
	if !slices.Contains(SupportedPythonVersions, req.PythonVersion) {
		errs = append(errs, fmt.Errorf("python_version %q is not a supported Lambda Python runtime; supported: %s",
			req.PythonVersion, strings.Join(SupportedPythonVersions, ", ")))
	}
	if req.Architecture == "" {
		req.Architecture = ArchitectureX8664
	}
	if req.Architecture != ArchitectureX8664 && req.Architecture != ArchitectureARM64 {
		errs = append(errs, fmt.Errorf("architecture %q must be %q or %q", req.Architecture, ArchitectureX8664, ArchitectureARM64))
	}
	if req.ArtifactType == "" {
		req.ArtifactType = ArtifactTypeFunction
	}
	if req.ArtifactType != ArtifactTypeFunction && req.ArtifactType != ArtifactTypeLayer {
		errs = append(errs, fmt.Errorf("artifact_type %q must be %q or %q", req.ArtifactType, ArtifactTypeFunction, ArtifactTypeLayer))
	}
	if req.LockMode == "" {
		req.LockMode = LockModeLocked
	}
	if req.LockMode != LockModeLocked && req.LockMode != LockModeFrozen {
		errs = append(errs, fmt.Errorf("lock_mode %q must be %q or %q", req.LockMode, LockModeLocked, LockModeFrozen))
	}
	if req.PlatformTag != "" && !platformTagPattern.MatchString(req.PlatformTag) {
		errs = append(errs, fmt.Errorf("platform_tag %q must look like manylinux_2_34 or manylinux2014", req.PlatformTag))
	}
	if strings.ContainsAny(req.Name, `/\`) {
		errs = append(errs, fmt.Errorf("name %q must not contain path separators", req.Name))
	}
	if slices.Contains(req.Extras, "") {
		errs = append(errs, errors.New("extras must not contain empty strings"))
	}
	if slices.Contains(req.Groups, "") {
		errs = append(errs, errors.New("groups must not contain empty strings"))
	}
	if slices.Contains(req.ExcludePackages, "") {
		errs = append(errs, errors.New("exclude_packages must not contain empty strings"))
	}
	if req.SizeLimitMode == "" {
		req.SizeLimitMode = SizeLimitModeError
	}
	if req.SizeLimitMode != SizeLimitModeError && req.SizeLimitMode != SizeLimitModeWarn {
		errs = append(errs, fmt.Errorf("size_limit_mode %q must be %q or %q", req.SizeLimitMode, SizeLimitModeError, SizeLimitModeWarn))
	}
	errs = append(errs, checkNoCredentials("index_url", req.Toolchain.IndexURL))
	for _, u := range req.Toolchain.ExtraIndexURLs {
		errs = append(errs, checkNoCredentials("extra_index_urls", u))
	}
	errs = append(errs, checkNoCredentials("python_install_mirror", req.Toolchain.PythonInstallMirror))
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	for _, f := range []string{"pyproject.toml", "uv.lock"} {
		if _, err := os.Stat(filepath.Join(req.ProjectDir, f)); err != nil {
			return nil, fmt.Errorf("project_dir %s: %s not found", req.ProjectDir, f)
		}
	}

	if req.Name == "" {
		abs, err := filepath.Abs(req.ProjectDir)
		if err != nil {
			return nil, err
		}
		req.Name = filepath.Base(abs)
	}
	lock, err := uvlock.Load(filepath.Join(req.ProjectDir, "uv.lock"))
	if err != nil {
		return nil, err
	}
	root, err := lock.Root(req.WorkspaceMember)
	if err != nil {
		return nil, err
	}
	rootDir := lockPath(req.ProjectDir, root.Path)
	if req.SourceDir == "" {
		req.SourceDir = rootDir
	}
	if req.SourceInclude == nil {
		req.SourceInclude = DefaultSourceInclude
	}
	if req.SourceExclude == nil {
		req.SourceExclude = DefaultSourceExclude
	}

	p := &buildPlan{req: req, platformTag: req.PlatformTag, rootDir: rootDir, lock: lock,
		rootName: python.NormalizeName(root.Name)}
	if req.InstallDependencies {
		if p.locals, err = lock.Reachable(req.WorkspaceMember, req.Extras, req.Groups); err != nil {
			return nil, err
		}
	}
	if p.platformTag == "" {
		p.platformTag = defaultPlatformTag(req.PythonVersion)
	}
	if req.InstallProject != nil {
		p.installProject = *req.InstallProject
	} else {
		has, err := hasBuildSystem(filepath.Join(rootDir, "pyproject.toml"))
		if err != nil {
			return nil, err
		}
		p.installProject = has
	}
	return p, nil
}

// checkNoCredentials rejects a URL argument that embeds userinfo. Provider
// arguments are persisted in state, plan JSON and saved plan files, so index
// credentials must come from the process environment. The value is never
// echoed.
func checkNoCredentials(arg, raw string) error {
	if raw == "" {
		return nil
	}
	_, rawURL := splitIndexName(raw)
	if u, err := url.Parse(rawURL); err == nil && u.User == nil {
		return nil
	} else if err != nil && !strings.Contains(rawURL, "@") {
		return nil // not a credentials problem; uv reports the bad URL
	}
	return fmt.Errorf("%s must not contain credentials (user:password@): Terraform persists provider arguments in state and plan files. "+
		"Name the index (\"corp=https://...\", or [[tool.uv.index]] name = \"corp\" in pyproject.toml) and set UV_INDEX_<NAME>_USERNAME and UV_INDEX_<NAME>_PASSWORD "+
		"(e.g. UV_INDEX_CORP_PASSWORD) in the environment instead; the value is not shown", arg)
}

// splitIndexName splits uv's named index form "name=url" into its name and
// URL. A plain URL has an empty name.
func splitIndexName(raw string) (name, rawURL string) {
	if i := strings.Index(raw, "="); i > 0 && !strings.ContainsAny(raw[:i], ":/@") {
		return raw[:i], raw[i+1:]
	}
	return "", raw
}

// lockPath resolves a source path recorded in uv.lock, which is relative to
// the lock's directory (projectDir) unless absolute.
func lockPath(projectDir, p string) string {
	if filepath.IsAbs(filepath.FromSlash(p)) {
		return filepath.FromSlash(p)
	}
	return filepath.Join(projectDir, filepath.FromSlash(p))
}

// defaultPlatformTag follows the runtime's Amazon Linux generation:
// 3.12+ run on Amazon Linux 2023 (glibc 2.34), earlier on Amazon Linux 2.
func defaultPlatformTag(pythonVersion string) string {
	// pythonVersion is already validated against SupportedPythonVersions.
	_, minorStr, _ := strings.Cut(pythonVersion, ".")
	minor, _ := strconv.Atoi(minorStr)
	if minor >= 12 {
		return "manylinux_2_34"
	}
	return "manylinux_2_26"
}

// hasBuildSystem reports whether pyproject.toml declares a [build-system]
// table header.
func hasBuildSystem(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if strings.ReplaceAll(line, " ", "") == "[build-system]" {
			return true, nil
		}
	}
	return false, sc.Err()
}
