package core_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core"
)

// Corporate and air-gapped network tests. A private index or
// mirror is simulated by an in-test HTTP(S) server that serves the pure-deps
// fixture's PEP 503 index, so no test touches the real network (once the
// managed Python is cached).

type testIndex struct {
	*httptest.Server
	hits atomic.Int64
}

// URL of the simple index root.
func (i *testIndex) simple() string { return i.URL + "/simple" }

type indexOpts struct {
	tls        bool
	user, pass string   // basic auth when set
	only       []string // serve only these packages (all when empty)
}

func startIndex(t *testing.T, o indexOpts) *testIndex {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "fixtures", "pure-deps")
	files := http.FileServer(http.Dir(root))
	idx := &testIndex{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o.user != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != o.user || p != o.pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="corp"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if len(o.only) > 0 {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/simple/"), "/")
			if !slices.Contains(o.only, parts[0]) {
				http.NotFound(w, r)
				return
			}
		}
		idx.hits.Add(1)
		files.ServeHTTP(w, r)
	})
	if o.tls {
		idx.Server = httptest.NewUnstartedServer(h)
		startTLS(t, idx.Server)
	} else {
		idx.Server = httptest.NewServer(h)
	}
	t.Cleanup(idx.Close)
	return idx
}

// startTLS switches an unstarted index server to TLS with a leaf certificate
// signed by a throwaway CA (httptest's own certificate is a CA used as an
// end-entity, which uv's rustls rejects) and points SSL_CERT_FILE at that CA,
// like the CA bundle of a TLS-inspecting proxy.
func startTLS(t *testing.T, s *httptest.Server) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test corp CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "index.corp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}
	s.StartTLS()

	pemFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(pemFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", pemFile)
}

// externalUV is the uv on PATH (mise pins it to the embedded version).
func externalUV(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("no uv on PATH (run under mise)")
	}
	abs, _ := filepath.Abs(p)
	return abs
}

// lockedProject writes a project with the pure-deps dependencies and
// pyprojectTail (index config), then creates uv.lock with the uv on PATH
// using lockArgs. The environment of the lock run is the test's own.
func lockedProject(t *testing.T, pyprojectTail string, lockArgs ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "def handler(event, context):\n    return 1\n")
	writeFile(t, filepath.Join(dir, "pyproject.toml"), `[project]
name = "corp-deps"
version = "0.1.0"
requires-python = ">=3.12"
dependencies = ["six==1.17.0", "tabulate==0.9.0"]
`+pyprojectTail)
	cmd := exec.Command(externalUV(t), append([]string{"lock"}, lockArgs...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+t.TempDir(), "UV_NO_SYSTEM_CONFIG=1",
		"UV_CACHE_DIR="+t.TempDir(),
		"UV_PYTHON_INSTALL_DIR="+filepath.Join(testCacheDir(), "python"), "UV_PYTHON_PREFERENCE=only-managed")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("uv lock: %v\n%s", err, out)
	}
	return dir
}

func corpRequest(t *testing.T, project string, tc core.ToolchainConfig) core.Request {
	t.Helper()
	r := depsRequest(project, filepath.Join(t.TempDir(), "out"))
	r.Name = "corp-deps"
	if tc.CacheDir == "" {
		tc.CacheDir = testCacheDir()
	}
	if tc.UVCacheDir == "" {
		tc.UVCacheDir = t.TempDir() // never reuse wheels cached by another test
	}
	r.Toolchain = tc
	return r
}

func hasEntry(t *testing.T, path, prefix string) bool {
	t.Helper()
	for _, n := range entryNames(zipEntries(t, path)) {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

func TestIndexURLArgumentIsUsedForInstalls(t *testing.T) {
	idx := startIndex(t, indexOpts{})
	project := lockedProject(t, "", "--default-index", idx.simple())
	res, err := core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{IndexURL: idx.simple()}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(t, res.OutputPath, "six.py") || !hasEntry(t, res.OutputPath, "tabulate/") {
		t.Errorf("packages missing: %v", entryNames(zipEntries(t, res.OutputPath)))
	}
	if idx.hits.Load() == 0 {
		t.Error("the private index was never contacted")
	}
}

func TestExtraIndexURLsAreUsedForInstalls(t *testing.T) {
	a := startIndex(t, indexOpts{only: []string{"six"}})
	b := startIndex(t, indexOpts{only: []string{"tabulate"}})
	project := lockedProject(t, "", "--default-index", a.simple(), "--index", b.simple())
	res, err := core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{
		IndexURL: a.simple(), ExtraIndexURLs: []string{b.simple()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(t, res.OutputPath, "six.py") || !hasEntry(t, res.OutputPath, "tabulate/") {
		t.Errorf("packages missing: %v", entryNames(zipEntries(t, res.OutputPath)))
	}
	if a.hits.Load() == 0 || b.hits.Load() == 0 {
		t.Errorf("hits a=%d b=%d, want both indexes contacted", a.hits.Load(), b.hits.Load())
	}
}

func TestProjectIndexWithPerIndexCredentialsFromEnvironment(t *testing.T) {
	const user, pass = "svc-builder", "s3cr3t-pa55"
	idx := startIndex(t, indexOpts{user: user, pass: pass})
	t.Setenv("UV_INDEX_CORP_USERNAME", user)
	t.Setenv("UV_INDEX_CORP_PASSWORD", pass)
	project := lockedProject(t, "\n[[tool.uv.index]]\nname = \"corp\"\nurl = \""+idx.simple()+"\"\ndefault = true\n")

	res, err := core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(t, res.OutputPath, "six.py") {
		t.Errorf("six missing: %v", entryNames(zipEntries(t, res.OutputPath)))
	}

	// Wrong credentials fail, and the error never carries either value.
	t.Setenv("UV_INDEX_CORP_PASSWORD", "wrong-pa55word")
	_, err = core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{}))
	if err == nil {
		t.Fatal("want failure with wrong credentials")
	}
	for _, leak := range []string{pass, "wrong-pa55word", user} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error leaks %q: %v", leak, err)
		}
	}
}

func TestIndexURLsWithEmbeddedCredentialsAreRejectedWithoutEchoingThem(t *testing.T) {
	// Provider arguments are persisted in saved plan files, so secrets must
	// come from the environment (UV_INDEX_<NAME>_USERNAME / _PASSWORD).
	const user, pass = "svc-builder", "s3cr3t-pa55"
	withCreds := "https://" + user + ":" + pass + "@index.corp.example/simple"
	project := copyFixture(t, "pure-deps")
	for name, tc := range map[string]core.ToolchainConfig{
		"index_url":             {IndexURL: withCreds},
		"extra_index_urls":      {ExtraIndexURLs: []string{"https://index.corp.example/simple", "corp=" + withCreds}},
		"python_install_mirror": {PythonInstallMirror: withCreds},
	} {
		t.Run(name, func(t *testing.T) {
			req := corpRequest(t, project, tc)
			for op, run := range map[string]func() error{
				"Fingerprint": func() error { _, err := core.Fingerprint(context.Background(), req); return err },
				"Build":       func() error { _, err := core.Build(context.Background(), req); return err },
			} {
				err := run()
				if err == nil {
					t.Fatalf("%s accepted %s with credentials", op, name)
				}
				if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "UV_INDEX_<NAME>_PASSWORD") {
					t.Errorf("%s error should name %s and point at UV_INDEX_<NAME>_PASSWORD: %v", op, name, err)
				}
				for _, leak := range []string{user, pass} {
					if strings.Contains(err.Error(), leak) {
						t.Errorf("%s error leaks %q: %v", op, leak, err)
					}
				}
			}
		})
	}
}

func TestNamedIndexURLArgumentsTakeCredentialsFromTheEnvironment(t *testing.T) {
	// index_url / extra_index_urls accept uv's "name=url" form, so the
	// per-index credential variables bind to them.
	const user, pass = "svc-builder", "s3cr3t-pa55"
	a := startIndex(t, indexOpts{user: user, pass: pass, only: []string{"six"}})
	b := startIndex(t, indexOpts{user: user, pass: pass, only: []string{"tabulate"}})
	t.Setenv("UV_INDEX_CORP_USERNAME", user)
	t.Setenv("UV_INDEX_CORP_PASSWORD", pass)
	t.Setenv("UV_INDEX_EXTRA_USERNAME", user)
	t.Setenv("UV_INDEX_EXTRA_PASSWORD", pass)
	project := lockedProject(t, "", "--default-index", "corp="+a.simple(), "--index", "extra="+b.simple())
	res, err := core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{
		IndexURL: "corp=" + a.simple(), ExtraIndexURLs: []string{"extra=" + b.simple()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(t, res.OutputPath, "six.py") || !hasEntry(t, res.OutputPath, "tabulate/") {
		t.Errorf("packages missing: %v", entryNames(zipEntries(t, res.OutputPath)))
	}
}

func TestCredentialsInTheProjectIndexURLNeverReachErrors(t *testing.T) {
	// The project's own pyproject.toml may embed credentials in an index
	// URL (it is not Terraform configuration). When the index rejects them
	// the build fails without echoing them, nor any per-index credentials
	// in the environment.
	const user, pass = "svc-builder", "s3cr3t-pa55"
	idx := startIndex(t, indexOpts{user: user, pass: pass})
	withCreds := strings.Replace(idx.simple(), "http://", "http://"+user+":"+pass+"@", 1)
	project := lockedProject(t, "\n[[tool.uv.index]]\nname = \"corp\"\nurl = \""+withCreds+"\"\ndefault = true\n")

	pyproject := filepath.Join(project, "pyproject.toml")
	b, err := os.ReadFile(pyproject)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, pyproject, strings.Replace(string(b), pass, "wrong-pa55word", 1))
	t.Setenv("UV_INDEX_OTHER_USERNAME", "env-user-name")
	t.Setenv("UV_INDEX_OTHER_PASSWORD", "env-pa55word")
	req := corpRequest(t, project, core.ToolchainConfig{})
	req.LockMode = core.LockModeFrozen // pyproject edited after locking
	_, err = core.Build(context.Background(), req)
	if err == nil {
		t.Fatal("want failure with wrong credentials")
	}
	for _, leak := range []string{pass, "wrong-pa55word", "env-user-name", "env-pa55word"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error leaks %q: %v", leak, err)
		}
	}
}

func TestInheritedUVSettingsAreIgnored(t *testing.T) {
	// uv settings in the process environment would make builds depend on
	// the host: they are dropped, and only provider arguments apply.
	idx := startIndex(t, indexOpts{})
	project := lockedProject(t, "", "--default-index", idx.simple())
	t.Setenv("UV_DEFAULT_INDEX", "http://127.0.0.1:1/simple")
	t.Setenv("UV_INDEX", "http://127.0.0.1:1/extra")
	t.Setenv("UV_OFFLINE", "1")
	t.Setenv("UV_NO_BINARY", "1")
	res, err := core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{IndexURL: idx.simple()}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(t, res.OutputPath, "six.py") || idx.hits.Load() == 0 {
		t.Errorf("index hits %d, entries %v", idx.hits.Load(), entryNames(zipEntries(t, res.OutputPath)))
	}
}

func TestMissingExternalUVFails(t *testing.T) {
	req := depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "out"))
	req.Toolchain.UVPath = "no-such-uv-binary"
	if _, err := core.Build(context.Background(), req); err == nil || !strings.Contains(err.Error(), "uv_path") {
		t.Fatalf("want a uv_path error, got %v", err)
	}
}

func TestTLSInspectingIndexTrustedThroughSSLCertFile(t *testing.T) {
	idx := startIndex(t, indexOpts{tls: true})
	project := lockedProject(t, "", "--default-index", idx.simple())

	res, err := core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{IndexURL: idx.simple()}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(t, res.OutputPath, "six.py") {
		t.Errorf("six missing")
	}

	// Without the CA the same build fails: the trust really comes from the env.
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "empty.pem"))
	if _, err := core.Build(context.Background(), corpRequest(t, project, core.ToolchainConfig{IndexURL: idx.simple()})); err == nil {
		t.Fatal("want TLS failure without the CA")
	}
}

func TestPythonInstallMirrorIsContactedForManagedPython(t *testing.T) {
	var paths []string
	var hits atomic.Int64
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		paths = append(paths, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer mirror.Close()

	// A fresh cache_dir has no managed Python, so uv must download one.
	req := depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "out"))
	req.Toolchain = core.ToolchainConfig{
		CacheDir:            t.TempDir(),
		UVCacheDir:          t.TempDir(),
		PythonInstallMirror: mirror.URL,
	}
	if _, err := core.Build(context.Background(), req); err == nil {
		t.Fatal("want failure: the mirror serves nothing")
	}
	if hits.Load() == 0 {
		t.Fatal("the Python mirror was never contacted")
	}
	if !strings.Contains(strings.Join(paths, " "), "cpython-3.12") {
		t.Errorf("mirror paths = %v, want a cpython-3.12 download", paths)
	}
}

func TestUVPathUsesExternalUVAndSkipsExtraction(t *testing.T) {
	uv := externalUV(t)
	cache := t.TempDir()
	linkManagedPython(t, cache)
	req := depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "out"))
	req.Toolchain = core.ToolchainConfig{CacheDir: cache, UVCacheDir: t.TempDir(), UVPath: uv}
	res, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.UVVersion == "" {
		t.Error("uv version not reported for the external uv")
	}
	if _, err := os.Stat(filepath.Join(cache, "uv")); err == nil {
		t.Error("the embedded uv was extracted although uv_path is set")
	}
}

func TestCacheDirRelocatesExtractionAndManagedPython(t *testing.T) {
	cache := t.TempDir()
	linkManagedPython(t, cache)
	req := depsRequest(copyFixture(t, "pure-deps"), filepath.Join(t.TempDir(), "out"))
	req.Toolchain = core.ToolchainConfig{CacheDir: cache, UVCacheDir: t.TempDir()}
	if _, err := core.Build(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(cache, "uv", "*", "uv*"))
	if len(matches) == 0 {
		t.Errorf("no extracted uv under %s/uv", cache)
	}
	if _, err := os.Stat(filepath.Join(cache, "uv-empty-config")); err != nil {
		t.Errorf("empty user-config dir not under cache_dir: %v", err)
	}
}

// linkManagedPython makes cache/python point at the shared test cache's
// managed Pythons, so tests with a fresh cache_dir do not download one.
func linkManagedPython(t *testing.T, cache string) {
	t.Helper()
	if err := os.Symlink(filepath.Join(testCacheDir(), "python"), filepath.Join(cache, "python")); err != nil {
		t.Fatal(err)
	}
}

// TestAirGappedBuildFromPrimedCaches primes the uv cache online, then builds
// again with offline = true, lock_mode = "frozen", the index server gone and
// every proxy variable aimed at a closed port.
func TestAirGappedBuildFromPrimedCaches(t *testing.T) {
	idx := startIndex(t, indexOpts{})
	project := lockedProject(t, "", "--default-index", idx.simple())
	uvCache := t.TempDir()
	cacheDir := t.TempDir()
	linkManagedPython(t, cacheDir)
	tc := core.ToolchainConfig{CacheDir: cacheDir, UVCacheDir: uvCache, IndexURL: idx.simple()}

	online, err := core.Build(context.Background(), corpRequest(t, project, tc))
	if err != nil {
		t.Fatal(err)
	}

	idx.Close() // nothing listens any more
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(k, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	// Control: without offline the build must fail, proving the network is gone.
	if _, err := core.Build(context.Background(), corpRequest(t, copyOf(t, project), core.ToolchainConfig{
		CacheDir: cacheDir, UVCacheDir: t.TempDir(), IndexURL: idx.simple(),
	})); err == nil {
		t.Fatal("control build with a fresh uv cache unexpectedly succeeded")
	}

	tc.Offline = true
	req := corpRequest(t, project, tc)
	req.LockMode = core.LockModeFrozen
	offline, err := core.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("offline build: %v", err)
	}
	if offline.OutputSHA256 != online.OutputSHA256 {
		t.Errorf("offline artifact differs from the online one:\n%s\n%s", offline.OutputSHA256, online.OutputSHA256)
	}
}

func copyOf(t *testing.T, dir string) string {
	t.Helper()
	dst := t.TempDir()
	for _, f := range []string{"pyproject.toml", "uv.lock", "handler.py"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dst, f), string(b))
	}
	return dst
}
