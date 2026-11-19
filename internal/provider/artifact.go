package provider

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/filehash"
)

// artifactRef is the Artifact a resource records: the zip and, when sbom is
// enabled, the SBOM, each with its hex SHA-256.
type artifactRef struct {
	zipPath, zipSHA256   string
	sbomPath, sbomSHA256 string
}

func (m *pythonPackageModel) artifact() artifactRef {
	return artifactRef{
		zipPath: m.OutputPath.ValueString(), zipSHA256: m.OutputSHA256.ValueString(),
		sbomPath: m.SBOMPath.ValueString(), sbomSHA256: m.SBOMSHA256.ValueString(),
	}
}

// present reports whether the Artifact exists on this runner with the
// recorded hashes. A missing or differing file is (false, nil); only
// unexpected I/O errors are returned.
func (a artifactRef) present() (bool, error) {
	ok, err := fileHashMatches(a.zipPath, a.zipSHA256)
	if err != nil || !ok || a.sbomPath == "" {
		return ok, err
	}
	return fileHashMatches(a.sbomPath, a.sbomSHA256)
}

func fileHashMatches(path, wantHex string) (bool, error) {
	got, err := filehash.SHA256(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("hash %s: %w", path, err)
	}
	return got == wantHex, nil
}

// removeArtifact deletes Artifact files (zip, SBOM). Empty paths and files
// that are already gone are ignored, so destroy never fails on a runner
// that does not have the Artifact.
func removeArtifact(paths ...string) error {
	var errs []error
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
