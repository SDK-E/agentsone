// SPDX-License-Identifier: MIT

package fetch_test

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func serve(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetchURL(t *testing.T, body []byte, spec domain.FetchSpec, opts ...fetch.Option) (domain.Tree, string, error) {
	t.Helper()
	srv := serve(t, body)
	f, tmp := newFetcher(t, append([]fetch.Option{fetch.WithInsecureHTTP()}, opts...)...)
	spec.Kind = domain.FetchURL
	if spec.Ref == "" {
		spec.Ref = srv.URL + "/plugin"
	}
	tree, err := f.Fetch(context.Background(), spec)
	return tree, tmp, err
}

func TestFetch_URLZipIsExtractedAndASingleTopLevelFolderIsStripped(t *testing.T) {
	t.Parallel()
	body := zipBytes(t,
		entry{name: "garden-1.0/", kind: tar.TypeDir, mode: 0o755},
		entry{name: "garden-1.0/skills/prune/SKILL.md", body: "skill"},
		entry{name: "garden-1.0/bin/run", body: "#!/bin/sh", mode: 0o755},
	)

	tree, tmp, err := fetchURL(t, body, domain.FetchSpec{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if got := readTree(t, tree, "skills/prune/SKILL.md"); got != "skill" {
		t.Errorf("file = %q", got)
	}
	sum := sha256.Sum256(body)
	if tree.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %q", tree.SHA256)
	}
	info, err := os.Stat(tree.Dir + "/bin/run")
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("exec bit lost: %v %v", info, err)
	}
	coretest.Check(t, tree.Cleanup)
	assertNothingLeft(t, tmp)
}

func TestFetch_URLTgzWithDotSlashNamesAndSeveralTopLevelEntriesKeepsTheLayout(t *testing.T) {
	t.Parallel()
	body := tgzBytes(t,
		entry{name: "./", kind: tar.TypeDir, mode: 0o755},
		entry{name: "./a.txt", body: "a"},
		entry{name: "./sub/b.txt", body: "b"},
	)

	tree, _, err := fetchURL(t, body, domain.FetchSpec{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	defer coretest.Check(t, tree.Cleanup)

	if readTree(t, tree, "a.txt") != "a" || readTree(t, tree, "sub/b.txt") != "b" {
		t.Error("layout not kept")
	}
}

func TestFetch_URLSubdirIsThePluginRoot(t *testing.T) {
	t.Parallel()
	body := zipBytes(t, entry{name: "repo/plugins/one/x.txt", body: "1"}, entry{name: "repo/other.txt", body: "2"})

	tree, _, err := fetchURL(t, body, domain.FetchSpec{Subdir: "plugins/one"})
	if err != nil {
		t.Fatal(err)
	}
	defer coretest.Check(t, tree.Cleanup)

	if readTree(t, tree, "x.txt") != "1" {
		t.Error("subdir not applied")
	}
}

func TestFetch_URLChecksTheExpectedSHA256(t *testing.T) {
	t.Parallel()
	body := zipBytes(t, entry{name: "a.txt", body: "a"})
	sum := sha256.Sum256(body)

	good, _, err := fetchURL(t, body, domain.FetchSpec{SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatalf("matching digest: %v", err)
	}
	coretest.Check(t, good.Cleanup)

	_, tmp, err := fetchURL(t, body, domain.FetchSpec{SHA256: "00" + hex.EncodeToString(sum[:])[2:]})
	if !errors.Is(err, domain.ErrIntegrity) {
		t.Fatalf("error = %v, want ErrIntegrity", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_ArchivesThatTryToLeaveTheTreeAreRefused(t *testing.T) {
	t.Parallel()
	tests := map[string][]byte{
		"zip-slip":                zipBytes(t, entry{name: "../evil.txt", body: "x"}),
		"zip-slip deep":           zipBytes(t, entry{name: "a/../../evil.txt", body: "x"}),
		"zip absolute path":       zipBytes(t, entry{name: "/etc/evil.txt", body: "x"}),
		"zip backslash":           zipBytes(t, entry{name: `..\evil.txt`, body: "x"}),
		"zip symlink up":          zipBytes(t, entry{name: "l", kind: tar.TypeSymlink, link: "../../etc"}),
		"zip symlink absolute":    zipBytes(t, entry{name: "l", kind: tar.TypeSymlink, link: "/etc/passwd"}),
		"tar path traversal":      tgzBytes(t, entry{name: "../evil.txt", body: "x"}),
		"tar traversal in middle": tgzBytes(t, entry{name: "a/../../evil.txt", body: "x"}),
		"tar absolute path":       tgzBytes(t, entry{name: "/etc/evil.txt", body: "x"}),
		"tar symlink up":          tgzBytes(t, entry{name: "l", kind: tar.TypeSymlink, link: "../.."}),
		"tar symlink absolute":    tgzBytes(t, entry{name: "l", kind: tar.TypeSymlink, link: "/etc"}),
		"tar hard link":           tgzBytes(t, entry{name: "h", kind: tar.TypeLink, link: "/etc/passwd"}),
		"tar device":              tgzBytes(t, entry{name: "d", kind: tar.TypeChar}),
		"tar fifo":                tgzBytes(t, entry{name: "p", kind: tar.TypeFifo}),
		"tar duplicate entry":     tgzBytes(t, entry{name: "a", body: "1"}, entry{name: "a", body: "2"}),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, tmp, err := fetchURL(t, body, domain.FetchSpec{})
			if !errors.Is(err, domain.ErrUnsafePath) {
				t.Fatalf("error = %v, want ErrUnsafePath", err)
			}
			assertNothingLeft(t, tmp)
		})
	}
}

func TestFetch_ArchiveSizeLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		limits domain.InstallLimits
		body   []byte
	}{
		{"one file over the file limit (zip)", domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 4, MaxTotalSize: 1 << 20},
			zipBytes(t, entry{name: "big", body: "12345"})},
		{"one file over the file limit (tar)", domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 4, MaxTotalSize: 1 << 20},
			tgzBytes(t, entry{name: "big", body: "12345"})},
		{"files add up over the total limit", domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 10, MaxTotalSize: 8},
			tgzBytes(t, entry{name: "a", body: "12345"}, entry{name: "b", body: "12345"})},
		{"too many files", domain.InstallLimits{MaxFiles: 2, MaxFileBytes: 10, MaxTotalSize: 1 << 20},
			zipBytes(t, entry{name: "a", body: "1"}, entry{name: "b", body: "1"}, entry{name: "c", body: "1"})},
		{"download over the total limit", domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 1 << 20, MaxTotalSize: 50},
			tgzBytes(t, entry{name: "a", body: "1"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, tmp, err := fetchURL(t, tt.body, domain.FetchSpec{}, fetch.WithLimits(tt.limits))
			if !errors.Is(err, domain.ErrTooLarge) {
				t.Fatalf("error = %v, want ErrTooLarge", err)
			}
			assertNothingLeft(t, tmp)
		})
	}
}

func TestFetch_URLRefusesPlainHTTPUnlessAllowed(t *testing.T) {
	t.Parallel()
	srv := serve(t, zipBytes(t, entry{name: "a", body: "1"}))
	f, tmp := newFetcher(t) // no WithInsecureHTTP

	_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchURL, Ref: srv.URL + "/p"})

	if !errors.Is(err, domain.ErrUnsupportedSource) {
		t.Fatalf("error = %v, want ErrUnsupportedSource", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_URLRefusesOtherSchemes(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"file:///etc/passwd", "ftp://example.com/a.zip", "example.com/a.zip", "https:///nohost"} {
		f, _ := newFetcher(t, fetch.WithInsecureHTTP())
		_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchURL, Ref: ref})
		if !errors.Is(err, domain.ErrUnsupportedSource) {
			t.Errorf("Fetch(%q) error = %v, want ErrUnsupportedSource", ref, err)
		}
	}
}

func TestFetch_URLRefusesARedirectToPlainHTTP(t *testing.T) {
	t.Parallel()
	plain := serve(t, zipBytes(t, entry{name: "a", body: "1"}))
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/p", http.StatusFound)
	}))
	t.Cleanup(tls.Close)
	f, tmp := newFetcher(t, fetch.WithHTTPClient(tls.Client()))

	_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchURL, Ref: tls.URL + "/p"})

	if err == nil || !errors.Is(err, domain.ErrUnsupportedSource) {
		t.Fatalf("error = %v, want ErrUnsupportedSource", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_URLOverHTTPSWorksWithTheServersCertificate(t *testing.T) {
	t.Parallel()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(zipBytes(t, entry{name: "a.txt", body: "secure"}))
	}))
	t.Cleanup(tls.Close)
	f, _ := newFetcher(t, fetch.WithHTTPClient(tls.Client()))

	tree, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchURL, Ref: tls.URL + "/p"})
	if err != nil {
		t.Fatal(err)
	}
	defer coretest.Check(t, tree.Cleanup)
	if readTree(t, tree, "a.txt") != "secure" {
		t.Error("content wrong")
	}
}

func TestFetch_URLErrors(t *testing.T) {
	t.Parallel()
	srv := serve(t, []byte("plain text, not an archive"))
	f, tmp := newFetcher(t, fetch.WithInsecureHTTP())

	_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchURL, Ref: srv.URL + "/missing"})
	if !errors.Is(err, domain.ErrNothingToInstall) {
		t.Errorf("404: error = %v, want ErrNothingToInstall", err)
	}
	_, err = f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchURL, Ref: srv.URL + "/text"})
	if !errors.Is(err, domain.ErrUnsupportedSource) {
		t.Errorf("not an archive: error = %v, want ErrUnsupportedSource", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = f.Fetch(ctx, domain.FetchSpec{Kind: domain.FetchURL, Ref: srv.URL + "/x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: error = %v, want context.Canceled", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_BrokenArchivesAreRefused(t *testing.T) {
	t.Parallel()
	for name, body := range map[string][]byte{
		"truncated zip": []byte("PK\x03\x04 truncated"),
		"bad gzip":      {0x1f, 0x8b, 0x00},
		"empty zip":     zipBytes(t),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, tmp, err := fetchURL(t, body, domain.FetchSpec{})
			if !errors.Is(err, domain.ErrUnsupportedSource) && !errors.Is(err, domain.ErrNothingToInstall) {
				t.Fatalf("error = %v, want ErrUnsupportedSource or ErrNothingToInstall", err)
			}
			assertNothingLeft(t, tmp)
		})
	}
}
