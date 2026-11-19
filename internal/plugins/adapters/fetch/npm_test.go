// SPDX-License-Identifier: MIT

package fetch_test

import (
	"context"
	"crypto/sha1" // #nosec G505 -- npm's legacy shasum, test only
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// registry is a fake npm registry with one package.
type registry struct {
	srv      *httptest.Server
	tarballs map[string][]byte // version -> tarball
	meta     map[string]map[string]string
	latest   string
	pkgPath  string
	tarHost  string // when set, used instead of the server URL in dist.tarball
}

func newRegistry(t *testing.T, pkgPath string, versions map[string][]byte) *registry {
	t.Helper()
	r := &registry{tarballs: versions, meta: map[string]map[string]string{}, pkgPath: pkgPath}
	for v, b := range versions {
		sum := sha512.Sum512(b)
		r.meta[v] = map[string]string{"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:])}
		r.latest = v
	}
	r.srv = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *registry) handle(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == r.pkgPath {
		base := r.srv.URL
		if r.tarHost != "" {
			base = r.tarHost
		}
		versions := map[string]any{}
		for v := range r.tarballs {
			dist := map[string]string{"tarball": base + "/tarball/" + v + ".tgz"}
			for k, val := range r.meta[v] {
				dist[k] = val
			}
			versions[v] = map[string]any{"dist": dist}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"dist-tags": map[string]string{"latest": r.latest}, "versions": versions})
		return
	}
	for v, b := range r.tarballs {
		if req.URL.Path == "/tarball/"+v+".tgz" {
			_, _ = w.Write(b)
			return
		}
	}
	http.NotFound(w, req)
}

func npmTarball(t *testing.T, body string) []byte {
	t.Helper()
	return tgzBytes(t,
		entry{name: "package/package.json", body: `{"name":"x","scripts":{"postinstall":"touch /tmp/pwned"}}`},
		entry{name: "package/skills/a/SKILL.md", body: body},
	)
}

func fetchNPMSpec(f *fetch.Fetcher, ref, rev string) (domain.Tree, error) {
	return f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchNPM, Ref: ref, Rev: rev})
}

func TestFetch_NPMDownloadsTheLatestVersionAndUnpacksThePackageFolder(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t, "/garden-kit", map[string][]byte{"1.2.0": npmTarball(t, "skill")})
	f, tmp := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL), fetch.WithInsecureHTTP())

	tree, err := fetchNPMSpec(f, "garden-kit", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if readTree(t, tree, "skills/a/SKILL.md") != "skill" {
		t.Error("file missing")
	}
	if tree.Rev != "1.2.0" || len(tree.SHA256) != 64 {
		t.Errorf("Rev %q SHA256 %q", tree.Rev, tree.SHA256)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, "package.json")); err != nil {
		t.Errorf("package.json missing: %v", err)
	}
	if _, err := os.Stat("/tmp/pwned"); err == nil {
		t.Error("an install script ran")
	}
	tree.Cleanup()
	assertNothingLeft(t, tmp)
}

func TestFetch_NPMPicksTheRequestedVersionAndSupportsScopedNames(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t, "/@acme/garden", map[string][]byte{"1.0.0": npmTarball(t, "old"), "2.0.0": npmTarball(t, "new")})
	f, _ := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL), fetch.WithInsecureHTTP())

	tree, err := fetchNPMSpec(f, "@acme/garden", "1.0.0")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	defer tree.Cleanup()

	if readTree(t, tree, "skills/a/SKILL.md") != "old" || tree.Rev != "1.0.0" {
		t.Errorf("got %q at %q", readTree(t, tree, "skills/a/SKILL.md"), tree.Rev)
	}
}

func TestFetch_NPMAcceptsATarballWithoutAPackageFolder(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t, "/flat", map[string][]byte{"1.0.0": tgzBytes(t, entry{name: "a.txt", body: "a"}, entry{name: "b.txt", body: "b"})})
	f, _ := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL), fetch.WithInsecureHTTP())

	tree, err := fetchNPMSpec(f, "flat", "")
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Cleanup()

	if readTree(t, tree, "a.txt") != "a" {
		t.Error("layout not kept")
	}
}

func TestFetch_NPMChecksTheRegistryDigest(t *testing.T) {
	t.Parallel()
	body := npmTarball(t, "skill")
	sha1sum := sha1.Sum(body) // #nosec G401

	tests := map[string]struct {
		meta map[string]string
		want error
	}{
		"wrong integrity": {map[string]string{"integrity": "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}, domain.ErrIntegrity},
		"wrong shasum":    {map[string]string{"shasum": hex.EncodeToString(make([]byte, 20))}, domain.ErrIntegrity},
		"right shasum":    {map[string]string{"shasum": hex.EncodeToString(sha1sum[:])}, nil},
		"no digest":       {map[string]string{}, nil},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reg := newRegistry(t, "/p", map[string][]byte{"1.0.0": body})
			reg.meta["1.0.0"] = tt.meta
			f, tmp := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL), fetch.WithInsecureHTTP())

			tree, err := fetchNPMSpec(f, "p", "")

			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if tt.want == nil {
				tree.Cleanup()
			}
			assertNothingLeft(t, tmp)
		})
	}
}

func TestFetch_NPMChecksTheExpectedSHA256(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t, "/p", map[string][]byte{"1.0.0": npmTarball(t, "skill")})
	f, tmp := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL), fetch.WithInsecureHTTP())

	_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchNPM, Ref: "p", SHA256: "00"})

	if !errors.Is(err, domain.ErrIntegrity) {
		t.Fatalf("error = %v, want ErrIntegrity", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_NPMErrors(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t, "/p", map[string][]byte{"1.0.0": npmTarball(t, "skill")})
	f, tmp := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL), fetch.WithInsecureHTTP())

	if _, err := fetchNPMSpec(f, "unknown", ""); !errors.Is(err, domain.ErrNothingToInstall) {
		t.Errorf("unknown package: error = %v", err)
	}
	if _, err := fetchNPMSpec(f, "p", "9.9.9"); !errors.Is(err, domain.ErrNothingToInstall) {
		t.Errorf("unknown version: error = %v", err)
	}
	for _, name := range []string{"../x", "UPPER", "a b", "@scope", "-x", "a/b/c", ""} {
		if _, err := fetchNPMSpec(f, name, ""); !errors.Is(err, domain.ErrUnsupportedSource) {
			t.Errorf("name %q: error = %v, want ErrUnsupportedSource", name, err)
		}
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_NPMRefusesPlainHTTPRegistryAndTarballs(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t, "/p", map[string][]byte{"1.0.0": npmTarball(t, "skill")})

	plainRegistry, tmp1 := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL))
	if _, err := fetchNPMSpec(plainRegistry, "p", ""); !errors.Is(err, domain.ErrUnsupportedSource) {
		t.Errorf("http registry: error = %v, want ErrUnsupportedSource", err)
	}
	assertNothingLeft(t, tmp1)

	// An https registry that points the tarball at plain http.
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions":  map[string]any{"1.0.0": map[string]any{"dist": map[string]string{"tarball": reg.srv.URL + "/tarball/1.0.0.tgz"}}},
		})
	}))
	t.Cleanup(tls.Close)
	f, tmp2 := newFetcher(t, fetch.WithRegistryURL(tls.URL), fetch.WithHTTPClient(tls.Client()))
	if _, err := fetchNPMSpec(f, "p", ""); !errors.Is(err, domain.ErrUnsupportedSource) {
		t.Errorf("http tarball: error = %v, want ErrUnsupportedSource", err)
	}
	assertNothingLeft(t, tmp2)
}

func TestFetch_NPMTarballWithTraversalIsRefused(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t, "/p", map[string][]byte{"1.0.0": tgzBytes(t, entry{name: "package/../../evil.txt", body: "x"})})
	f, tmp := newFetcher(t, fetch.WithRegistryURL(reg.srv.URL), fetch.WithInsecureHTTP())

	if _, err := fetchNPMSpec(f, "p", ""); !errors.Is(err, domain.ErrUnsafePath) {
		t.Fatalf("error = %v, want ErrUnsafePath", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_NPMRegistryProblems(t *testing.T) {
	t.Parallel()
	tests := map[string]http.HandlerFunc{
		"server error": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
		"not json":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(h)
			t.Cleanup(srv.Close)
			f, tmp := newFetcher(t, fetch.WithRegistryURL(srv.URL), fetch.WithInsecureHTTP())
			if _, err := fetchNPMSpec(f, "p", ""); !errors.Is(err, domain.ErrNothingToInstall) {
				t.Fatalf("error = %v, want ErrNothingToInstall", err)
			}
			assertNothingLeft(t, tmp)
		})
	}
}
