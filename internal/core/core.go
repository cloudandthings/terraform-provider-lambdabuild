// Package core is the core build interface (test seam B) of lambdabuild.
//
// The provider and the CLI harness both call exactly two operations:
//
//   - Fingerprint computes the Input fingerprint of a Request, plus warnings
//     such as source files with CRLF line endings. It never writes anything
//     and is what the provider runs at plan time.
//   - Build produces the Artifact for a Request in Request.OutputDir and
//     returns its paths, hashes and sizes.
//
// The fingerprinter, packager and artifact store live under
// internal/core/internal/ so that nothing outside this package can reach
// them; they are tested only through this interface.
package core

import (
	"context"
)

// Artifact types.
const (
	ArtifactTypeFunction = "function"
	ArtifactTypeLayer    = "layer"
)

// Architectures.
const (
	ArchitectureX8664 = "x86_64"
	ArchitectureARM64 = "arm64"
)

// Lock modes.
const (
	LockModeLocked = "locked"
	LockModeFrozen = "frozen"
)

// Request describes one Artifact build. Field names follow the
// lambdabuild_python_package resource arguments. Zero values mean
// "use the resource default" except where noted: the provider must pass
// booleans (InstallDependencies, SBOM) explicitly because their resource
// defaults are true.
type Request struct {
	// ProjectDir holds pyproject.toml and uv.lock. Required.
	ProjectDir string
	// OutputDir receives the zip (and SBOM). Created if missing. Required.
	OutputDir string
	// Name is the zip file name prefix. Default: base name of ProjectDir.
	Name string

	// PythonVersion is a Lambda Python runtime version such as "3.12". Required.
	PythonVersion string
	// Architecture is "x86_64" (default) or "arm64".
	Architecture string
	// ArtifactType is "function" (default) or "layer".
	ArtifactType string
	// PlatformTag overrides the wheel platform tag derived from PythonVersion.
	PlatformTag string

	// WorkspaceMember selects a uv workspace member to build.
	WorkspaceMember string
	// Extras and Groups select optional dependencies and dependency groups.
	Extras []string
	Groups []string
	// ExcludePackages are Packages removed after install.
	ExcludePackages []string

	// InstallDependencies installs third-party Packages. false builds a
	// code-only Artifact.
	InstallDependencies bool
	// LockMode is "locked" (default: fail when uv.lock is stale) or
	// "frozen" (skip the staleness check). Not part of the fingerprint: it
	// never changes the Artifact bytes.
	LockMode string

	// InstallProject installs the project as a wheel. nil means "true when
	// pyproject.toml has a [build-system] table".
	InstallProject *bool

	// SourceDir is the root for copied source when the project is not
	// installed as a wheel. Default: ProjectDir.
	SourceDir string
	// SourceInclude are globs of source files to copy. nil means
	// DefaultSourceInclude.
	SourceInclude []string
	// SourceExclude are globs skipped when copying and fingerprinting.
	// nil means DefaultSourceExclude.
	SourceExclude []string

	// SBOM writes a CycloneDX SBOM beside the zip.
	SBOM bool
	// Triggers are arbitrary values that force a rebuild when they change.
	Triggers map[string]string

	// SizeLimitMode is "error" (default: fail when the unpacked Artifact
	// exceeds Lambda's 250 MB limit) or "warn" (still write it). Not part
	// of the fingerprint.
	SizeLimitMode string

	// Toolchain is provider-level configuration. It never changes the
	// Artifact bytes and is not part of the fingerprint.
	Toolchain ToolchainConfig
}

// ToolchainConfig locates uv and the managed Python. Field names follow
// the provider arguments.
type ToolchainConfig struct {
	// CacheDir holds the extracted uv (<CacheDir>/uv/<version>/uv) and the
	// managed Pythons (<CacheDir>/python). Must allow executing binaries.
	// Default: the user cache directory + "/lambdabuild".
	CacheDir string
	// UVCacheDir is uv's package cache. Default: uv's own default.
	UVCacheDir string
	// UVPath uses an external uv binary instead of the embedded one.
	UVPath string
	// IndexURL is the default package index; ExtraIndexURLs add indexes.
	IndexURL       string
	ExtraIndexURLs []string
	// PythonInstallMirror is the managed Python mirror
	// (UV_PYTHON_INSTALL_MIRROR).
	PythonInstallMirror string
	// NativeTLS trusts the OS certificate store (UV_NATIVE_TLS); Offline
	// uses only local caches (UV_OFFLINE).
	NativeTLS bool
	Offline   bool
}

// DefaultSourceInclude is the default for Request.SourceInclude.
var DefaultSourceInclude = []string{"**/*.py"}

// DefaultSourceExclude is the default for Request.SourceExclude.
var DefaultSourceExclude = []string{
	".venv", ".git", "__pycache__", "dist", "build", "*.egg-info",
	".pytest_cache", ".lambdabuild", "tests",
}

// Warning is a non-fatal diagnostic. Code is stable for programmatic use;
// Paths lists the files it concerns, relative and with "/" separators.
type Warning struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Paths   []string `json:"paths"`
}

// Warning codes.
const (
	WarningCRLF         = "crlf_line_endings"
	WarningZipSize      = "zip_size_exceeds_direct_upload_limit"
	WarningUnpackedSize = "unpacked_size_exceeds_lambda_limit"
)

// FingerprintResult is the outcome of Fingerprint.
type FingerprintResult struct {
	// Fingerprint is the hex SHA-256 Input fingerprint.
	Fingerprint string `json:"input_fingerprint"`
	// OutputPath and SBOMPath are known at plan time because they embed a
	// fingerprint prefix. SBOMPath is empty when Request.SBOM is false.
	OutputPath string    `json:"output_path"`
	SBOMPath   string    `json:"sbom_path,omitempty"`
	Warnings   []Warning `json:"warnings,omitempty"`
}

// BuildResult is the outcome of Build.
type BuildResult struct {
	FingerprintResult

	// OutputSHA256 is the hex SHA-256 of the zip; OutputBase64SHA256 the
	// base64 form expected by aws_lambda_function.source_code_hash.
	OutputSHA256       string `json:"output_sha256"`
	OutputBase64SHA256 string `json:"output_base64sha256"`
	// OutputSize is the zip size in bytes; UnpackedSize the sum of the
	// uncompressed entry sizes.
	OutputSize   int64 `json:"output_size"`
	UnpackedSize int64 `json:"unpacked_size"`

	// SBOMSHA256 is the hex SHA-256 of the SBOM; empty when disabled.
	SBOMSHA256 string `json:"sbom_sha256,omitempty"`

	// UVVersion and PythonFullVersion are empty for builds that do not run
	// uv (code-only Artifacts from copied source).
	UVVersion         string `json:"uv_version,omitempty"`
	PythonFullVersion string `json:"python_full_version,omitempty"`

	// ExcludedPackages lists the installed Packages that exclude_packages
	// actually removed, sorted by name. Not a Terraform attribute; the SBOM
	// omits these Packages.
	ExcludedPackages []ExcludedPackage `json:"-"`
}

// ExcludedPackage is a Package removed by exclude_packages. Name is the
// normalized (PEP 503) name.
type ExcludedPackage struct {
	Name    string
	Version string
}

// Fingerprint computes the Input fingerprint for req without writing anything.
func Fingerprint(ctx context.Context, req Request) (FingerprintResult, error) {
	p, err := plan(req)
	if err != nil {
		return FingerprintResult{}, err
	}
	return p.fingerprint(ctx)
}

// Build produces the Artifact for req.
func Build(ctx context.Context, req Request) (BuildResult, error) {
	p, err := plan(req)
	if err != nil {
		return BuildResult{}, err
	}
	return p.build(ctx)
}
