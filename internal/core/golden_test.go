package core_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// goldenCase pins the Input fingerprint and Artifact hashes of one fixture
// build. The same table runs on every OS in the CI matrix, so
// any host-dependent byte fails here.
//
// When a change is intended (packaging-format version bump, new fingerprint
// argument, Go version bump), run `mise run test:golden`, copy the "got"
// values from the failure output, and call the change out in the changelog.
type goldenCase struct {
	name    string
	fixture string
	request func(projectDir, outputDir string) core.Request

	fingerprint string
	zipSHA256   string
	sbomSHA256  string // "" for sbom = false rows; see golden_sbom_test.go
}

var goldenCases = []goldenCase{
	{
		name:        "code-only function",
		fixture:     "code-only",
		request:     codeOnlyRequest,
		fingerprint: "de0df3005c704acc1dfd499fadd5d0dbf63f5b38c6bebdd7b469646e8898fe61",
		zipSHA256:   "cc5f921b541dbd86ccac391910d012f07babc87b31b0cc785738b6955422c428",
	},
	{
		name:        "function with pure-Python dependencies",
		fixture:     "pure-deps",
		request:     depsRequest,
		fingerprint: "90c0d14d998574ba4cf29317941f27a71ed96a74b23b90ad76cf36486c127184",
		zipSHA256:   "14731136f7fdf51f4dc1a8a42fc62f0f630cd6bcbaf51583105a7ece85756007",
	},
	{
		name:        "function with compiled wheels, x86_64",
		fixture:     "compiled-deps",
		request:     compiledRequest,
		fingerprint: "81acd2667c67b3295a3a53c0f1118f3e7b525766bf8ae740b7d90d00f2590c73",
		zipSHA256:   "35eed199fdf6fb791fd38310e0db38e7f1f1838cd8227d21d6d83f6e546f7fdb",
	},
	{
		name:    "function with compiled wheels, arm64",
		fixture: "compiled-deps",
		request: func(projectDir, outputDir string) core.Request {
			r := compiledRequest(projectDir, outputDir)
			r.Architecture = core.ArchitectureARM64
			return r
		},
		fingerprint: "fd2afc2aed64587fa1d3eebc8edf64b1d236050fe022c2670c93a9a9319e5e03",
		zipSHA256:   "94f29e7f3ceab239edc37cf05a253c8ac2e8a51a5e3cb53d092231a859614077",
	},
	{
		name:        "function with dependencies and the project installed as a wheel",
		fixture:     "packaged",
		request:     packagedRequest,
		fingerprint: "db517e89c26b3d65d51549a380e234f0f4a3302efe936ce750fa9571418989c0",
		zipSHA256:   "24fd1c7c7a1d13d60b256f57ad215df0c498e99cf5810e46a0e11ea60ff26aab",
	},
	{
		name:        "workspace member with a sibling member and a path dependency",
		fixture:     "workspace",
		request:     workspaceRequest, // project_dir is <fixture>/ws
		fingerprint: "5aa03160b7fae39207457873c3953590fc6319693d824c3e0817b42f7c62a391",
		zipSHA256:   "f38c13708f500655fdfea5f9a6eff46429e376c77e29de079e14cdb23684e0e2",
	},
}

func TestGoldenHashes(t *testing.T) {
	for _, gc := range goldenCases {
		t.Run(gc.name, func(t *testing.T) {
			project := copyFixture(t, gc.fixture)
			res, err := core.Build(context.Background(), gc.request(project, filepath.Join(t.TempDir(), "out")))
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
