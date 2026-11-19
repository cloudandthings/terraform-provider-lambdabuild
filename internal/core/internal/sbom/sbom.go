// Package sbom writes the CycloneDX 1.6 JSON SBOM that sits beside an
// Artifact. The output is a pure function of its input: fixed key order
// (struct field order), sorted components, no timestamp, a UUIDv5 serial
// number derived from the zip hash and LF newlines. Every rule here feeds
// the packaging-format version: change a rule, bump packager.FormatVersion.
package sbom

import (
	"crypto/sha1" //nolint:gosec // UUIDv5 is defined over SHA-1; not a security use.
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

// namespace is the UUIDv5 namespace of lambdabuild SBOM serial numbers:
// itself a UUIDv5 (of the URL namespace) of the provider's source address.
var namespace = uuid5(
	[16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8},
	"https://github.com/cloudandthings/terraform-provider-lambdabuild",
)

// Package is one shipped Package.
type Package struct {
	// Name and Version come from the Package's METADATA.
	Name    string
	Version string
	// LicenseExpression is an SPDX expression (License-Expression); License
	// is the free-text License header. At most one is written, the
	// expression first. Both empty omits the licence.
	LicenseExpression string
	License           string
	// SHA256 is the hex digest of the locked wheel; empty for local
	// Packages and when uv.lock has none.
	SHA256 string
	// LocalPath is the uv.lock source path of a local Package, listed as a
	// file: reference. Empty for registry Packages.
	LocalPath string
}

// Document is what Write needs.
type Document struct {
	// Name is the top-level component: the project the zip was built from.
	Name string
	// ZipSHA256 is the hex SHA-256 of the zip.
	ZipSHA256 string
	Packages  []Package
}

type bom struct {
	BomFormat    string    `json:"bomFormat"`
	SpecVersion  string    `json:"specVersion"`
	SerialNumber string    `json:"serialNumber"`
	Version      int       `json:"version"`
	Metadata     metadata  `json:"metadata"`
	Components   []element `json:"components"`
}

type metadata struct {
	Component element `json:"component"`
}

type element struct {
	Type               string        `json:"type"`
	BomRef             string        `json:"bom-ref"`
	Name               string        `json:"name"`
	Version            string        `json:"version,omitempty"`
	Purl               string        `json:"purl,omitempty"`
	Hashes             []hash        `json:"hashes,omitempty"`
	Licenses           []licence     `json:"licenses,omitempty"`
	ExternalReferences []externalRef `json:"externalReferences,omitempty"`
}

type hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

type licence struct {
	License    *licenceName `json:"license,omitempty"`
	Expression string       `json:"expression,omitempty"`
}

type licenceName struct {
	Name string `json:"name"`
}

type externalRef struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Write encodes d as the SBOM document.
func Write(w io.Writer, d Document) error {
	doc := bom{
		BomFormat:    "CycloneDX",
		SpecVersion:  "1.6",
		SerialNumber: "urn:uuid:" + formatUUID(uuid5(namespace, d.ZipSHA256)),
		Version:      1,
		Metadata: metadata{Component: element{
			Type:   "application",
			BomRef: "lambdabuild:" + d.Name,
			Name:   d.Name,
			Hashes: []hash{{Alg: "SHA-256", Content: d.ZipSHA256}},
		}},
		Components: make([]element, 0, len(d.Packages)),
	}

	pkgs := append([]Package(nil), d.Packages...)
	sort.SliceStable(pkgs, func(i, j int) bool {
		a, b := pkgs[i], pkgs[j]
		if pa, pb := purlName(a.Name), purlName(b.Name); pa != pb {
			return pa < pb
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.LocalPath < b.LocalPath
	})
	for _, p := range pkgs {
		purl := "pkg:pypi/" + purlName(p.Name)
		if p.Version != "" {
			purl += "@" + url.PathEscape(p.Version)
		}
		e := element{Type: "library", BomRef: purl, Name: p.Name, Version: p.Version, Purl: purl}
		if p.SHA256 != "" {
			e.Hashes = []hash{{Alg: "SHA-256", Content: p.SHA256}}
		}
		switch {
		case p.LicenseExpression != "":
			e.Licenses = []licence{{Expression: p.LicenseExpression}}
		case p.License != "" && !strings.EqualFold(p.License, "unknown"):
			e.Licenses = []licence{{License: &licenceName{Name: p.License}}}
		}
		if p.LocalPath != "" {
			e.ExternalReferences = []externalRef{{Type: "distribution", URL: "file:" + p.LocalPath}}
		}
		doc.Components = append(doc.Components, e)
	}

	enc := json.NewEncoder(w) // Encode ends the document with "\n".
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// purlName is the purl-spec form of a PyPI name: lower-case, "_" as "-".
func purlName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

func uuid5(ns [16]byte, name string) [16]byte {
	h := sha1.New() //nolint:gosec // see import
	h.Write(ns[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0f | 0x50
	u[8] = u[8]&0x3f | 0x80
	return u
}

func formatUUID(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
