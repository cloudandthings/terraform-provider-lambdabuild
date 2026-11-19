package core_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// sbomGoldenCases pin the fingerprint (sbom = true is a fingerprinted
// argument), zip and SBOM hashes of builds with the SBOM enabled. Zip hashes
// equal the sbom = false rows in goldenCases: the SBOM never changes the zip.
// Regenerate like goldenCases: set "TODO", run `mise run test:golden`.
var sbomGoldenCases = []goldenCase{
	{
		name:        "SBOM of a function with pure-Python dependencies",
		fixture:     "pure-deps",
		request:     depsRequest,
		fingerprint: "1d38976a367124989f4cb87bf7e0941cd6a5b3171c69c194035552b6edc143a2",
		zipSHA256:   "14731136f7fdf51f4dc1a8a42fc62f0f630cd6bcbaf51583105a7ece85756007",
		sbomSHA256:  "5ada0aba3322ff059711cbc50e3c51a9bfdf43d2cb1a243e5c7698ba58394368",
	},
	{
		name:        "SBOM of a workspace member with a sibling member and a path dependency",
		fixture:     "workspace",
		request:     workspaceRequest,
		fingerprint: "b3551161a6b73547bef55fbdc550d44ab199e9e2f3bebee1d037db7a51c98299",
		zipSHA256:   "f38c13708f500655fdfea5f9a6eff46429e376c77e29de079e14cdb23684e0e2",
		sbomSHA256:  "eb1d82e9bf4203b44133a5295736609a5374e575305c25a1e1774894f9a884ab",
	},
	{
		name:        "SBOM of a code-only function",
		fixture:     "code-only",
		request:     codeOnlyRequest,
		fingerprint: "0915df4890f73648dd8fe43ba49d825c35732f39c5edab56a741bbf2b8355282",
		zipSHA256:   "cc5f921b541dbd86ccac391910d012f07babc87b31b0cc785738b6955422c428",
		sbomSHA256:  "6aec11deeb3e28fcb2a6a020b961aed69d9363c5574d9a257d3ad4654a168504",
	},
}

func TestGoldenSBOMHashes(t *testing.T) {
	for _, gc := range sbomGoldenCases {
		t.Run(gc.name, func(t *testing.T) {
			project := copyFixture(t, gc.fixture)
			req := gc.request(project, filepath.Join(t.TempDir(), "out"))
			req.SBOM = true
			res, err := core.Build(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if res.Fingerprint != gc.fingerprint {
				t.Errorf("fingerprint\n got %s\nwant %s", res.Fingerprint, gc.fingerprint)
			}
			if res.OutputSHA256 != gc.zipSHA256 {
				t.Errorf("zip sha256\n got %s\nwant %s", res.OutputSHA256, gc.zipSHA256)
			}
			if res.SBOMSHA256 != gc.sbomSHA256 {
				t.Errorf("sbom sha256\n got %s\nwant %s", res.SBOMSHA256, gc.sbomSHA256)
			}
		})
	}
}
