package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/artifact"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/builder/python"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/packager"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/sbom"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/sourcetree"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/toolchain"
	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/uvlock"
)

// Build of a plan: install, clean, lay out and write the Artifact.

func (p *buildPlan) build(ctx context.Context) (BuildResult, error) {
	sources, err := p.sources()
	if err != nil {
		return BuildResult{}, err
	}
	fp, err := p.fingerprintWith(ctx, sources)
	if err != nil {
		return BuildResult{}, err
	}

	layout := ""
	if p.req.ArtifactType == ArtifactTypeLayer {
		layout = "python/"
	}
	entries := make([]packager.Entry, 0, len(sources))
	if !p.installProject { // the project wheel replaces the source files
		for _, f := range sources {
			entries = append(entries, packager.Entry{Name: layout + f.Rel, Path: f.Abs})
		}
	}

	var uvVersion, pythonFull string
	if p.req.InstallDependencies || p.installProject {
		work, err := os.MkdirTemp("", "lambdabuild-")
		if err != nil {
			return BuildResult{}, err
		}
		defer func() { _ = os.RemoveAll(work) }()
		staged, tc, py, err := p.installPackages(ctx, work)
		if err != nil {
			return BuildResult{}, err
		}
		uvVersion, pythonFull = tc.Version, py.FullVersion
		for _, f := range staged {
			entries = append(entries, packager.Entry{Name: layout + f.Rel, Path: f.Abs})
		}
	}

	sizes, preUnpacked, err := packageSizes(entries, layout)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return BuildResult{}, fmt.Errorf("source changed during build: %w", err)
		}
		return BuildResult{}, err
	}
	if preUnpacked > maxUnzippedBytes {
		w := sizeWarning(WarningUnpackedSize, "250 MB unzipped", preUnpacked, sizes)
		if p.req.SizeLimitMode == SizeLimitModeError {
			return BuildResult{}, errors.New(w.Message + " Set size_limit_mode = \"warn\" to build it anyway")
		}
		fp.Warnings = append(fp.Warnings, w)
	}

	// The zip and the SBOM are staged as temp files and put in place
	// together, so a failure never leaves a zip without its SBOM.
	var unpacked int64
	zipFile, err := artifact.Stage(p.req.OutputDir, fp.OutputPath, func(w io.Writer) error {
		n, err := packager.WriteZip(w, entries)
		unpacked = n
		return err
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return BuildResult{}, fmt.Errorf("source changed during build: %w", err)
		}
		return BuildResult{}, err
	}

	if zipFile.Size > maxZippedBytes {
		fp.Warnings = append(fp.Warnings, sizeWarning(WarningZipSize, "50 MB direct-upload", zipFile.Size, sizes))
	}

	res := BuildResult{
		FingerprintResult:  fp,
		OutputSHA256:       zipFile.HexSHA256(),
		OutputBase64SHA256: base64.StdEncoding.EncodeToString(zipFile.SHA256[:]),
		OutputSize:         zipFile.Size,
		UnpackedSize:       unpacked,
		UVVersion:          uvVersion,
		PythonFullVersion:  pythonFull,
		ExcludedPackages:   p.excluded,
	}
	var sbomFile *artifact.Staged
	if p.req.SBOM {
		doc := sbom.Document{Name: p.req.Name, ZipSHA256: res.OutputSHA256, Packages: p.sbomPackages(p.installed)}
		sbomFile, err = artifact.Stage(p.req.OutputDir, fp.SBOMPath, func(w io.Writer) error {
			return sbom.Write(w, doc)
		})
		if err != nil {
			artifact.Discard(zipFile)
			return BuildResult{}, fmt.Errorf("write SBOM: %w", err)
		}
		res.SBOMSHA256 = sbomFile.HexSHA256()
	}
	// SBOM first: should the zip rename fail, the SBOM is removed again.
	if err := artifact.Commit(sbomFile, zipFile); err != nil {
		return BuildResult{}, fmt.Errorf("write Artifact: %w", err)
	}
	return res, nil
}

// installPackages prepares the toolchain, installs the third-party Packages
// (install_dependencies) and the project wheel (install_project) into
// <work>/staging, cleans it and returns its files.
func (p *buildPlan) installPackages(ctx context.Context, work string) ([]sourcetree.File, *toolchain.Toolchain, toolchain.Python, error) {
	cacheDir := p.req.Toolchain.CacheDir
	if cacheDir == "" {
		userCache, err := os.UserCacheDir()
		if err != nil {
			return nil, nil, toolchain.Python{}, fmt.Errorf("cache_dir not set and no user cache directory: %w", err)
		}
		cacheDir = filepath.Join(userCache, "lambdabuild")
	}
	tc, err := toolchain.Ensure(p.toolchainConfig(cacheDir))
	if err != nil {
		return nil, nil, toolchain.Python{}, err
	}
	py, err := tc.Python(ctx, p.req.PythonVersion)
	if err != nil {
		return nil, nil, toolchain.Python{}, err
	}
	projectDir, err := filepath.Abs(p.req.ProjectDir)
	if err != nil {
		return nil, nil, toolchain.Python{}, err
	}
	staging := filepath.Join(work, "staging")
	opts := python.Options{
		ProjectDir:      projectDir,
		Python:          py.Path,
		PythonVersion:   p.req.PythonVersion,
		Platform:        p.pythonPlatform(),
		Frozen:          p.req.LockMode == LockModeFrozen,
		WorkspaceMember: p.req.WorkspaceMember,
		Extras:          sortedSet(p.req.Extras),
		Groups:          sortedSet(p.req.Groups),
		TmpDir:          work,
		Staging:         staging,
	}
	if p.req.InstallDependencies {
		if err := python.InstallDependencies(ctx, tc, opts); err != nil {
			return nil, nil, toolchain.Python{}, err
		}
	}
	// The project and every local dependency are built as wheels into one
	// dist dir and installed together without deps: their third-party deps
	// came from the hash-verified export above.
	dist := filepath.Join(work, "dist")
	if p.installProject {
		copied, err := p.copyTree(p.rootDir, filepath.Join(work, "src", "project"))
		if err != nil {
			return nil, nil, toolchain.Python{}, err
		}
		if err := python.BuildWheel(ctx, tc, opts, copied, dist); err != nil {
			return nil, nil, toolchain.Python{}, err
		}
	}
	for i, l := range p.locals {
		if err := p.buildLocalWheel(ctx, tc, opts, l, filepath.Join(work, "src", strconv.Itoa(i)), dist); err != nil {
			return nil, nil, toolchain.Python{}, err
		}
	}
	if err := python.InstallWheels(ctx, tc, opts, dist); err != nil {
		return nil, nil, toolchain.Python{}, err
	}
	if err := python.Clean(staging); err != nil {
		return nil, nil, toolchain.Python{}, err
	}
	excluded, err := python.ExcludePackages(staging, p.req.ExcludePackages)
	if err != nil {
		return nil, nil, toolchain.Python{}, err
	}
	for _, e := range excluded {
		p.excluded = append(p.excluded, ExcludedPackage(e))
	}
	if p.req.SBOM {
		if p.installed, err = python.ListInstalled(staging); err != nil {
			return nil, nil, toolchain.Python{}, err
		}
	}
	files, err := listFiles(staging)
	return files, tc, py, err
}

// buildLocalWheel puts a wheel of local dependency l into dist, built from a
// copy of its tree in copyDir; a path source that already is a wheel file
// is copied as is.
func (p *buildPlan) buildLocalWheel(ctx context.Context, tc *toolchain.Toolchain, opts python.Options, l uvlock.Local, copyDir, dist string) error {
	abs, err := filepath.Abs(lockPath(p.req.ProjectDir, l.Path))
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("local dependency %s: %w", l.Name, err)
	}
	src := abs // an sdist file: uv builds it in its own temp dir
	if info.IsDir() {
		if src, err = p.copyTree(abs, copyDir); err != nil {
			return fmt.Errorf("local dependency %s: %w", l.Name, err)
		}
	} else if strings.HasSuffix(abs, ".whl") {
		if err := os.MkdirAll(dist, 0o755); err != nil {
			return err
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dist, filepath.Base(abs)), b, 0o644)
	}
	if err := python.BuildWheel(ctx, tc, opts, src, dist); err != nil {
		return fmt.Errorf("build local dependency %s (%s): %w", l.Name, l.Path, err)
	}
	return nil
}

// copyTree copies the fingerprinted files of the local tree srcDir (every
// file minus source_exclude and .gitignore'd paths) to dst and returns
// dst's absolute path. Wheels are built from this per-build copy, never in
// place: build backends such as setuptools write build/ and *.egg-info
// into the tree they build, which races with parallel builds sharing the
// tree and could change the next fingerprint; and a wheel built from the
// copy can only contain files the Input fingerprint covers.
func (p *buildPlan) copyTree(srcDir, dst string) (string, error) {
	files, err := sourcetree.Select(srcDir, []string{"**"}, p.req.SourceExclude)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if err := copyFile(f.Abs, filepath.Join(dst, filepath.FromSlash(f.Rel))); err != nil {
			return "", err
		}
	}
	return filepath.Abs(dst)
}

// copyFile copies src to dst, keeping the permission bits (a build backend
// may care about executable scripts).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// pythonPlatform is uv's --python-platform for the Lambda target, e.g.
// "aarch64-manylinux_2_34".
func (p *buildPlan) pythonPlatform() string {
	arch := "x86_64"
	if p.req.Architecture == ArchitectureARM64 {
		arch = "aarch64"
	}
	return arch + "-" + p.platformTag
}

// listFiles returns every regular file under root. Unlike source selection
// it applies no include, exclude or .gitignore rules: everything left in
// staging after cleaning ships.
func listFiles(root string) ([]sourcetree.File, error) {
	var files []sourcetree.File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root {
				return fs.SkipAll // nothing installed
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, sourcetree.File{Rel: filepath.ToSlash(rel), Abs: p})
		return nil
	})
	return files, err
}

// toolchainConfig maps the provider-level toolchain arguments onto the uv
// toolchain, which overlays them on the uv environment.
func (p *buildPlan) toolchainConfig(cacheDir string) toolchain.Config {
	t := p.req.Toolchain
	return toolchain.Config{
		CacheDir:            cacheDir,
		UVCacheDir:          t.UVCacheDir,
		UVPath:              t.UVPath,
		IndexURL:            t.IndexURL,
		ExtraIndexURLs:      t.ExtraIndexURLs,
		PythonInstallMirror: t.PythonInstallMirror,
		NativeTLS:           t.NativeTLS,
		Offline:             t.Offline,
	}
}
