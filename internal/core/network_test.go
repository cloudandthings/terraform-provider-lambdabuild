//go:build network

package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// The numpy fixture is the one fixture that downloads from real PyPI, so it
// is excluded from `mise run test:unit` by the "network" build tag. Run it
// with `mise run test:network`.

func numpyRequest(arch string) func(projectDir, outputDir string) core.Request {
	return func(projectDir, outputDir string) core.Request {
		r := depsRequest(projectDir, outputDir)
		r.Name = "numpy-deps"
		r.Architecture = arch
		return r
	}
}

var numpyGolden = []goldenCase{
	{
		name:        "numpy, x86_64",
		fixture:     "numpy-deps",
		request:     numpyRequest(core.ArchitectureX8664),
		fingerprint: "89d58b7cdaf2f723f8b7e76e6f8c0409206d1e7196b3868ec29ecc805acbf790",
		zipSHA256:   "507060f588e7ac1fe9e1ca1b7a29401b7c2690faa91fd61b55681e0d81259b6b",
	},
	{
		name:        "numpy, arm64",
		fixture:     "numpy-deps",
		request:     numpyRequest(core.ArchitectureARM64),
		fingerprint: "5336b1c1714ee9067f813ec4eb03825c28afa16952a11559d0937ea057dda22d",
		zipSHA256:   "eb8c427457169cedc8b5a6aea0ec70463ce8d304192f80762b02098a39fd0982",
	},
}

func TestNetworkNumpyGoldenHashes(t *testing.T) {
	for _, gc := range numpyGolden {
		t.Run(gc.name, func(t *testing.T) {
			project := copyFixture(t, gc.fixture)
			res, err := core.Build(context.Background(), gc.request(project, filepath.Join(t.TempDir(), "out")))
			if err != nil {
				t.Fatal(err)
			}
			var so string
			for _, n := range entryNames(zipEntries(t, res.OutputPath)) {
				if strings.HasPrefix(n, "numpy/_core/_multiarray_umath.") {
					so = n
				}
			}
			if so == "" || !strings.HasSuffix(so, ".so") {
				t.Errorf("no compiled numpy extension in the Artifact (found %q)", so)
			}
			if res.Fingerprint != gc.fingerprint {
				t.Errorf("fingerprint\n got %s\nwant %s", res.Fingerprint, gc.fingerprint)
			}
			if res.OutputSHA256 != gc.zipSHA256 {
				t.Errorf("zip sha256\n got %s\nwant %s", res.OutputSHA256, gc.zipSHA256)
			}
		})
	}
}
