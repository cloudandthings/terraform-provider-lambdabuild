// Command lambdabuild-harness drives the core build interface from the
// command line (Phase 0/1 CLI harness). It mirrors the
// lambdabuild_python_package arguments and prints the result as JSON.
//
//	lambdabuild-harness fingerprint -project-dir P -output-dir O -python-version 3.12 [flags]
//	lambdabuild-harness build       -project-dir P -output-dir O -python-version 3.12 [flags]
//
// Defaults follow the resource: an SBOM is written unless -sbom=false, and
// a code-only Artifact needs -install-dependencies=false.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: lambdabuild-harness {fingerprint|build} [flags]  (-h for flags)"
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd := args[0]
	if cmd != "fingerprint" && cmd != "build" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var req core.Request
	var extras, groups, excludePkgs, include, exclude listFlag
	var installProject optBool
	triggers := mapFlag{}
	fs.StringVar(&req.ProjectDir, "project-dir", "", "directory with pyproject.toml and uv.lock (required)")
	fs.StringVar(&req.OutputDir, "output-dir", "", "directory for the zip and SBOM (required)")
	fs.StringVar(&req.Name, "name", "", "zip name prefix (default: project-dir base name)")
	fs.StringVar(&req.PythonVersion, "python-version", "", "Lambda Python runtime, e.g. 3.12 (required)")
	fs.StringVar(&req.Architecture, "architecture", "x86_64", "x86_64 or arm64")
	fs.StringVar(&req.ArtifactType, "artifact-type", "function", "function or layer")
	fs.StringVar(&req.PlatformTag, "platform-tag", "", "wheel platform tag (default: derived from python-version)")
	fs.StringVar(&req.WorkspaceMember, "workspace-member", "", "uv workspace member to build")
	fs.StringVar(&req.SourceDir, "source-dir", "", "root for copied source (default: project-dir)")
	fs.BoolVar(&req.InstallDependencies, "install-dependencies", true, "install third-party Packages")
	fs.StringVar(&req.LockMode, "lock-mode", "locked", "locked (fail on stale uv.lock) or frozen")
	fs.StringVar(&req.SizeLimitMode, "size-limit-mode", "error", "error or warn when the unpacked Artifact exceeds 250 MB")
	fs.StringVar(&req.Toolchain.CacheDir, "cache-dir", "", "where uv is extracted and managed Python installed (default: user cache dir + /lambdabuild)")
	fs.StringVar(&req.Toolchain.UVCacheDir, "uv-cache-dir", "", "uv package cache (default: uv's own)")
	fs.BoolVar(&req.SBOM, "sbom", true, "write a CycloneDX SBOM")
	fs.Var(&installProject, "install-project", "install the project as a wheel (default: when [build-system] exists)")
	fs.Var(&extras, "extra", "optional-dependency extra (repeatable)")
	fs.Var(&groups, "group", "dependency group (repeatable)")
	fs.Var(&excludePkgs, "exclude-package", "Package to remove after install (repeatable)")
	fs.Var(&include, "source-include", "source glob to copy (repeatable; default **/*.py)")
	fs.Var(&exclude, "source-exclude", "glob to skip (repeatable; replaces the defaults)")
	fs.Var(triggers, "trigger", "key=value that forces a rebuild when changed (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	req.Extras, req.Groups, req.ExcludePackages = extras, groups, excludePkgs
	req.SourceInclude, req.SourceExclude = include, exclude
	req.InstallProject = installProject.v
	req.Triggers = triggers

	var out any
	var err error
	if cmd == "fingerprint" {
		out, err = core.Fingerprint(ctx, req)
	} else {
		out, err = core.Build(ctx, req)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

type mapFlag map[string]string

func (m mapFlag) String() string { return "" }
func (m mapFlag) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok {
		return errors.New("want key=value")
	}
	m[k] = val
	return nil
}

type optBool struct{ v *bool }

func (o *optBool) String() string {
	if o.v == nil {
		return "auto"
	}
	return strconv.FormatBool(*o.v)
}
func (o *optBool) Set(s string) error {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	o.v = &b
	return nil
}
func (o *optBool) IsBoolFlag() bool { return true }
