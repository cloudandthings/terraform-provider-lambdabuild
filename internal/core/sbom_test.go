package core_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// bomDoc is the slice of CycloneDX 1.6 these tests read.
type bomDoc struct {
	BomFormat    string `json:"bomFormat"`
	SpecVersion  string `json:"specVersion"`
	SerialNumber string `json:"serialNumber"`
	Version      int    `json:"version"`
	Metadata     struct {
		Component bomComp `json:"component"`
	} `json:"metadata"`
	Components []bomComp `json:"components"`
}

type bomComp struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Purl    string `json:"purl"`
	Hashes  []struct {
		Alg     string `json:"alg"`
		Content string `json:"content"`
	} `json:"hashes"`
	Licenses []struct {
		Expression string `json:"expression"`
		License    *struct {
			Name string `json:"name"`
		} `json:"license"`
	} `json:"licenses"`
	ExternalReferences []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"externalReferences"`
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// buildSBOM builds req with the SBOM enabled and parses the result.
func buildSBOM(t *testing.T, req core.Request) (core.BuildResult, bomDoc, []byte) {
	t.Helper()
	req.SBOM = true
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(res.SBOMPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc bomDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("SBOM is not JSON: %v", err)
	}
	return res, doc, raw
}

func (d bomDoc) component(name string) (bomComp, bool) {
	for _, c := range d.Components {
		if c.Name == name {
			return c, true
		}
	}
	return bomComp{}, false
}

func TestSBOMListsPackagesWithPurlWheelHashAndLicence(t *testing.T) {
	req := depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "out"))
	res, doc, raw := buildSBOM(t, req)

	if doc.BomFormat != "CycloneDX" || doc.SpecVersion != "1.6" || doc.Version != 1 {
		t.Errorf("header = %q %q %d", doc.BomFormat, doc.SpecVersion, doc.Version)
	}
	if !regexp.MustCompile(`^urn:uuid:[0-9a-f-]{36}$`).MatchString(doc.SerialNumber) {
		t.Errorf("serialNumber = %q", doc.SerialNumber)
	}
	if strings.Contains(string(raw), "timestamp") {
		t.Error("SBOM must carry no timestamp")
	}
	if bytes.Contains(raw, []byte("\r")) || !bytes.HasSuffix(raw, []byte("\n")) {
		t.Error("SBOM must use LF newlines and end with one")
	}

	top := doc.Metadata.Component
	if top.Name != "pure-deps" || len(top.Hashes) != 1 || top.Hashes[0].Alg != "SHA-256" || top.Hashes[0].Content != res.OutputSHA256 {
		t.Errorf("top-level component = %+v, want name pure-deps with zip sha %s", top, res.OutputSHA256)
	}

	if len(doc.Components) != 2 || doc.Components[0].Name != "six" || doc.Components[1].Name != "tabulate" {
		t.Fatalf("components = %+v, want six, tabulate sorted", doc.Components)
	}
	six := doc.Components[0]
	if six.Version != "1.17.0" || six.Purl != "pkg:pypi/six@1.17.0" {
		t.Errorf("six = %+v", six)
	}
	// Literals from the fixture's uv.lock.
	if len(six.Hashes) != 1 || six.Hashes[0].Alg != "SHA-256" ||
		six.Hashes[0].Content != "4721f391ed90541fddacab5acf947aa0d3dc7d27b2e1e8eda2be8970586c3274" {
		t.Errorf("six hashes = %+v", six.Hashes)
	}
	tab := doc.Components[1]
	if len(tab.Hashes) != 1 || tab.Hashes[0].Content != "024ca478df22e9340661486f85298cff5f6dcdba14f3813e8830015b9ed1948f" {
		t.Errorf("tabulate hashes = %+v", tab.Hashes)
	}
	if len(six.Licenses) != 1 || six.Licenses[0].License == nil || six.Licenses[0].License.Name != "MIT" {
		t.Errorf("six licence (License field) = %+v", six.Licenses)
	}

	sum, err := fileSHA256(res.SBOMPath)
	if err != nil || sum != res.SBOMSHA256 {
		t.Errorf("SBOMSHA256 = %s, file hashes to %s (%v)", res.SBOMSHA256, sum, err)
	}
	if !strings.HasSuffix(res.SBOMPath, ".cdx.json") || !strings.Contains(res.SBOMPath, res.Fingerprint[:16]) {
		t.Errorf("SBOMPath = %s", res.SBOMPath)
	}
}

func TestSBOMIsByteIdenticalAcrossBuildsAndDirectories(t *testing.T) {
	a, _, rawA := buildSBOM(t, depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "out")))
	b, _, rawB := buildSBOM(t, depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "other")))
	if !bytes.Equal(rawA, rawB) || a.SBOMSHA256 != b.SBOMSHA256 {
		t.Error("SBOM differs between identical builds")
	}
}

func TestSBOMSerialNumberFollowsZipHash(t *testing.T) {
	_, doc1, _ := buildSBOM(t, codeOnlyRequest(copyFixture(t, "code-only"), filepath.Join(t.TempDir(), "out")))
	project := copyFixture(t, "code-only")
	writeFile(t, filepath.Join(project, "handler.py"), "def handler(e, c):\n    return 2\n")
	_, doc2, _ := buildSBOM(t, codeOnlyRequest(project, filepath.Join(t.TempDir(), "out")))
	if doc1.SerialNumber == doc2.SerialNumber {
		t.Error("different zips must have different serial numbers")
	}
}

func TestSBOMOfCodeOnlyArtifactHasNoComponents(t *testing.T) {
	res, doc, _ := buildSBOM(t, codeOnlyRequest(copyFixture(t, "code-only"), filepath.Join(t.TempDir(), "out")))
	if len(doc.Components) != 0 {
		t.Errorf("components = %+v", doc.Components)
	}
	if doc.Metadata.Component.Hashes[0].Content != res.OutputSHA256 {
		t.Error("top-level hash must be the zip sha256")
	}
}

func TestSBOMDisabledWritesNoFile(t *testing.T) {
	req := codeOnlyRequest(copyFixture(t, "code-only"), filepath.Join(t.TempDir(), "out"))
	res, err := core.Build(context.Background(), req) // SBOM false
	if err != nil {
		t.Fatal(err)
	}
	if res.SBOMPath != "" || res.SBOMSHA256 != "" {
		t.Errorf("SBOM fields set: %q %q", res.SBOMPath, res.SBOMSHA256)
	}
	files, _ := filepath.Glob(filepath.Join(req.OutputDir, "*.cdx.json"))
	if len(files) != 0 {
		t.Errorf("found %v", files)
	}
}

func TestSBOMOmitsExcludedPackages(t *testing.T) {
	req := depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "out"))
	req.ExcludePackages = []string{"Six"}
	_, doc, _ := buildSBOM(t, req)
	if _, ok := doc.component("six"); ok {
		t.Error("excluded six is listed")
	}
	if _, ok := doc.component("tabulate"); !ok {
		t.Error("tabulate missing")
	}
}

func TestSBOMListsLocalDependenciesByFileReferenceWithoutHash(t *testing.T) {
	req := workspaceRequest(copyFixture(t, "workspace"), filepath.Join(t.TempDir(), "out"))
	_, doc, _ := buildSBOM(t, req)

	if doc.Metadata.Component.Name != "wsapp" {
		t.Errorf("top-level = %q", doc.Metadata.Component.Name)
	}
	if _, ok := doc.component("wsapp"); ok {
		t.Error("the project itself must be the top-level component only")
	}
	lib, ok := doc.component("wslib")
	if !ok {
		t.Fatalf("wslib missing: %+v", doc.Components)
	}
	if len(lib.Hashes) != 0 {
		t.Errorf("local package has hashes: %+v", lib.Hashes)
	}
	if len(lib.ExternalReferences) != 1 || lib.ExternalReferences[0].URL != "file:packages/wslib" {
		t.Errorf("wslib refs = %+v", lib.ExternalReferences)
	}
	pd, ok := doc.component("pathdep")
	if !ok || len(pd.ExternalReferences) != 1 || pd.ExternalReferences[0].URL != "file:../vendor/pathdep" {
		t.Errorf("pathdep = %+v", pd)
	}
	six, ok := doc.component("six")
	if !ok || len(six.Hashes) != 1 || len(six.ExternalReferences) != 0 {
		t.Errorf("six = %+v", six)
	}
}

func TestSBOMLicenceExpressionWinsAndUnknownOrMissingLicencesAreOmitted(t *testing.T) {
	req := depsRequest(copyFixture(t, "sbom-licences"), filepath.Join(t.TempDir(), "out"))
	req.Name = "sbom-licences"
	_, doc, raw := buildSBOM(t, req)

	zeta, ok := doc.component("Zeta_Pkg")
	if !ok {
		t.Fatalf("Zeta_Pkg missing: %+v", doc.Components)
	}
	if zeta.Purl != "pkg:pypi/zeta-pkg@1.0" {
		t.Errorf("purl = %q, want the normalised name", zeta.Purl)
	}
	if len(zeta.Licenses) != 1 || zeta.Licenses[0].Expression != "MIT OR Apache-2.0" || bytes.Contains(raw, []byte("ignored free text")) {
		t.Errorf("Zeta_Pkg licences = %+v, want only the License-Expression", zeta.Licenses)
	}
	for _, name := range []string{"unknownlic", "nolic"} {
		c, ok := doc.component(name)
		if !ok || len(c.Licenses) != 0 {
			t.Errorf("%s = %+v, want listed without licences", name, c)
		}
	}
	if bytes.Contains(raw, []byte("UNKNOWN")) {
		t.Error("UNKNOWN licence text in SBOM")
	}
	var names []string
	for _, c := range doc.Components {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "nolic,unknownlic,Zeta_Pkg" {
		t.Errorf("components %q, want sorted by normalised name", names)
	}
}

func TestSBOMWheelHashIsTheLockedWheelInstalledForTheArchitecture(t *testing.T) {
	// Literals from compiled-deps/uv.lock: one fakeext wheel per architecture.
	for arch, want := range map[string]string{
		"x86_64": "0769f3b6efe6276f2593670b90eaff2c5665bbc1823d3217d1e9b2d40b51bcdc",
		"arm64":  "4490702ccf453ae502890d87bc0d44891c47c1c8bd4ed18e3b93984c3627418d",
	} {
		t.Run(arch, func(t *testing.T) {
			req := compiledRequest(copyFixture(t, "compiled-deps"), filepath.Join(t.TempDir(), "out"))
			req.Architecture = arch
			_, doc, _ := buildSBOM(t, req)
			c, ok := doc.component("fakeext")
			if !ok || len(c.Hashes) != 1 || c.Hashes[0].Content != want {
				t.Errorf("fakeext = %+v, want hash %s", c, want)
			}
		})
	}
}

func TestSBOMSerialNumberDependsOnTheZipOnlyNotTheName(t *testing.T) {
	a := codeOnlyRequest(copyFixture(t, "code-only"), filepath.Join(t.TempDir(), "out"))
	b := a
	b.Name = "other-name"
	ra, docA, _ := buildSBOM(t, a)
	rb, docB, _ := buildSBOM(t, b)
	if ra.OutputSHA256 != rb.OutputSHA256 {
		t.Fatal("the name changed the zip bytes")
	}
	if docA.SerialNumber != docB.SerialNumber {
		t.Errorf("serial numbers differ for identical zips: %s %s", docA.SerialNumber, docB.SerialNumber)
	}
	// UUIDv5: version nibble at "urn:uuid:xxxxxxxx-xxxx-Vxxx".
	if v := docA.SerialNumber[len("urn:uuid:")+14]; v != '5' {
		t.Errorf("uuid version = %c, want 5", v)
	}
}

func TestZipAndSBOMAreWrittenTogetherOrNotAtAll(t *testing.T) {
	// The SBOM is part of the Artifact: when it cannot be written, no zip
	// is left behind without it, and no temp files either.
	req := codeOnlyRequest(copyFixture(t, "code-only"), filepath.Join(t.TempDir(), "out"))
	req.SBOM = true
	fp, err := core.Fingerprint(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fp.SBOMPath, "blocker"), 0o755); err != nil { // a directory where the SBOM goes
		t.Fatal(err)
	}
	if _, err := core.Build(context.Background(), req); err == nil {
		t.Fatal("Build succeeded although the SBOM could not be written")
	}
	entries, err := os.ReadDir(req.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(fp.SBOMPath) {
			t.Errorf("left behind in output_dir: %s", e.Name())
		}
	}
}

func TestSBOMPathIsKnownAtPlanTime(t *testing.T) {
	req := codeOnlyRequest(copyFixture(t, "code-only"), filepath.Join(t.TempDir(), "out"))
	req.SBOM = true
	fp, err := core.Fingerprint(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if fp.SBOMPath == "" || fp.SBOMPath != res.SBOMPath {
		t.Errorf("planned %q, built %q", fp.SBOMPath, res.SBOMPath)
	}
}
