// SPDX-License-Identifier: MIT

package httpsource_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/enginefetch/adapters/disk"
	"github.com/SDK-E/agentsone/internal/enginefetch/adapters/httpsource"
	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
	"github.com/SDK-E/agentsone/internal/enginefetch/enginefetchtest"
)

var engine = bytes.Repeat([]byte("0123456789abcdef"), 4096) // 64 KiB

// releases is a fake engine download server: it serves the engine at
// /rel/<version>/linux-x64/claude with byte ranges and records every request.
type releases struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newReleases(t *testing.T, h http.HandlerFunc) *releases {
	t.Helper()
	r := &releases{}
	if h == nil {
		h = func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/rel/2.1.328/linux-x64/claude" {
				http.NotFound(w, req)
				return
			}
			http.ServeContent(w, req, "claude", time.Time{}, bytes.NewReader(engine))
		}
	}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.reqs = append(r.reqs, req.Clone(context.Background()))
		r.mu.Unlock()
		h(w, req)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *releases) requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.reqs...)
}

func read(t *testing.T, b app.Body) []byte {
	t.Helper()
	defer func() { _ = b.Close() }()
	got, err := io.ReadAll(b)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return got
}

func TestFetch_DownloadsTheWholeFileFromZero(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)

	b, err := httpsource.New(r.srv.Client()).Fetch(t.Context(), r.srv.URL+"/rel/2.1.328/linux-x64/claude", 0)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if b.Offset != 0 || !bytes.Equal(read(t, b), engine) {
		t.Fatal("whole file not served from offset 0")
	}
	req := r.requests()[0]
	if req.Header.Get("Range") != "" || req.Header.Get("User-Agent") != httpsource.UserAgent {
		t.Fatalf("request headers = %v, want no range and the generic user agent", req.Header)
	}
}

func TestFetch_ResumesWithARange(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)

	b, err := httpsource.New(nil).Fetch(t.Context(), r.srv.URL+"/rel/2.1.328/linux-x64/claude", 1000)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if b.Offset != 1000 || !bytes.Equal(read(t, b), engine[1000:]) {
		t.Fatal("the rest of the file was not served from offset 1000")
	}
	if got := r.requests()[0].Header.Get("Range"); got != "bytes=1000-" {
		t.Fatalf("Range = %q, want bytes=1000-", got)
	}
}

func TestFetch_ReportsARangeThatCannotBeServed(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)

	_, err := httpsource.New(nil).Fetch(t.Context(), r.srv.URL+"/rel/2.1.328/linux-x64/claude", int64(len(engine)+10))

	if !errors.Is(err, app.ErrRangeRefused) {
		t.Fatalf("Fetch() error = %v, want %v", err, app.ErrRangeRefused)
	}
}

func TestFetch_ReportsTheServerStatusInPlainWords(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)

	_, err := httpsource.New(nil).Fetch(t.Context(), r.srv.URL+"/rel/9.9.9/linux-x64/claude", 0)

	if err == nil || !strings.Contains(err.Error(), "answered 404 Not Found") {
		t.Fatalf("Fetch() error = %v, want the 404 named", err)
	}
}

func TestFetch_RejectsAnUnreadableContentRange(t *testing.T) {
	t.Parallel()
	r := newReleases(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "pages 1-2")
		w.WriteHeader(http.StatusPartialContent)
	})

	_, err := httpsource.New(nil).Fetch(t.Context(), r.srv.URL+"/x", 10)

	if err == nil || !strings.Contains(err.Error(), "Content-Range") {
		t.Fatalf("Fetch() error = %v, want an unreadable Content-Range error", err)
	}
}

func TestFetch_RefusesARedirectToAnotherHost(t *testing.T) {
	t.Parallel()
	other := newReleases(t, nil)
	r := newReleases(t, func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, strings.Replace(other.srv.URL, "127.0.0.1", "localhost", 1)+"/rel/2.1.328/linux-x64/claude", http.StatusFound)
	})

	_, err := httpsource.New(nil).Fetch(t.Context(), r.srv.URL+"/rel/2.1.328/linux-x64/claude", 0)

	if err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("Fetch() error = %v, want the redirect refused", err)
	}
	if n := len(other.requests()); n != 0 {
		t.Fatalf("other host got %d requests, want 0", n)
	}
}

func TestFetch_NamesTheAllowListWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)
	url := r.srv.URL + "/rel/2.1.328/linux-x64/claude"
	r.srv.Close()

	_, err := httpsource.New(nil).Fetch(t.Context(), url, 0)

	if err == nil || !strings.Contains(err.Error(), "allow list") {
		t.Fatalf("Fetch() error = %v, want a hint about the allow list", err)
	}
}

func TestFetch_StopsWhenCancelled(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := httpsource.New(nil).Fetch(ctx, r.srv.URL+"/rel/2.1.328/linux-x64/claude", 0)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want %v", err, context.Canceled)
	}
}

// fetcher wires the real disk and HTTP source to a fake download server.
func fetcher(t *testing.T, r *releases, gate *enginefetchtest.Gate, root string) (*app.Fetcher, *enginefetchtest.Sink, domain.Pin) {
	t.Helper()
	pin := domain.Pin{
		Engine: "claude-code", Version: "2.1.328", BaseURL: "https://downloads.example/rel",
		Artifacts: map[string]domain.Artifact{
			"linux/amd64": {Platform: "linux-x64", Binary: "claude", SHA256: enginefetchtest.Sum(engine), Size: int64(len(engine))},
		},
	}
	// The pin must be https; the test server is plain http, so the client
	// rewrites the scheme and host on the way out.
	client := &http.Client{Transport: rewrite{to: r.srv.URL, next: http.DefaultTransport}}
	sink := &enginefetchtest.Sink{}
	f, err := app.New(app.Deps{
		Pin: pin, GOOS: "linux", GOARCH: "amd64", Licence: gate, Status: sink,
		Source: httpsource.New(client), Disk: disk.New(root),
		Clock: coretest.NewClock(time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)), ReportEvery: 16 << 10,
	})
	if err != nil {
		t.Fatalf("app.New() error = %v", err)
	}
	return f, sink, pin
}

type rewrite struct {
	to   string
	next http.RoundTripper
}

func (rw rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	u := strings.TrimPrefix(rw.to, "http://")
	req.URL.Scheme, req.URL.Host = "http", u
	return rw.next.RoundTrip(req)
}

func TestEngineFetch_DownloadsNothingBeforeTheLicenceIsAccepted(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)
	root := t.TempDir()
	f, _, _ := fetcher(t, r, enginefetchtest.NewGate(false), root)

	_, err := f.Ensure(t.Context())

	if !errors.Is(err, app.ErrLicenceNotAccepted) {
		t.Fatalf("Ensure() error = %v, want %v", err, app.ErrLicenceNotAccepted)
	}
	if n := len(r.requests()); n != 0 {
		t.Fatalf("download server got %d requests before licence acceptance, want 0", n)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("engine folder has %d entries before licence acceptance, want none", len(entries))
	}
}

func TestEngineFetch_InstallsFromTheDownloadServerOnceAccepted(t *testing.T) {
	t.Parallel()
	r := newReleases(t, nil)
	root := t.TempDir()
	gate := enginefetchtest.NewGate(false)
	f, sink, pin := fetcher(t, r, gate, root)
	_, _ = f.Ensure(t.Context())
	gate.Accept()

	st, err := f.Ensure(t.Context())

	if err != nil || st.State != domain.StateReady || sink.Last().State != domain.StateReady {
		t.Fatalf("Ensure() = %q, %v, want ready", st.State, err)
	}
	got, err := os.ReadFile(domain.BinaryPath(root, pin))
	if err != nil || !bytes.Equal(got, engine) {
		t.Fatalf("installed engine differs (%v)", err)
	}
	if path := r.requests()[0].URL.Path; path != "/rel/2.1.328/linux-x64/claude" {
		t.Fatalf("download path = %q, want the pinned version and platform", path)
	}
}

func TestEngineFetch_ResumesAcrossARestartWithoutDownloadingAgain(t *testing.T) {
	t.Parallel()
	cut := true
	var mu sync.Mutex
	r := newReleases(t, func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		first := cut
		cut = false
		mu.Unlock()
		if first {
			// The first response breaks off after 20000 bytes.
			w.Header().Set("Content-Length", "65536")
			_, _ = w.Write(engine[:20000])
			return
		}
		http.ServeContent(w, req, "claude", time.Time{}, bytes.NewReader(engine))
	})
	root := t.TempDir()
	gate := enginefetchtest.NewGate(true)
	f, _, pin := fetcher(t, r, gate, root)

	if _, err := f.Ensure(t.Context()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	reqs := r.requests()
	if len(reqs) != 2 || reqs[1].Header.Get("Range") != "bytes=20000-" {
		t.Fatalf("requests = %d, second Range %q, want a resume at byte 20000", len(reqs), reqs[len(reqs)-1].Header.Get("Range"))
	}

	// A new daemon process (same volume) finds the engine and downloads nothing.
	again, _, _ := fetcher(t, r, gate, root)
	if st, err := again.Ensure(t.Context()); err != nil || st.State != domain.StateReady {
		t.Fatalf("Ensure() after restart = %q, %v, want ready", st.State, err)
	}
	if n := len(r.requests()); n != 2 {
		t.Fatalf("requests after restart = %d, want still 2", n)
	}
	if got, _ := os.ReadFile(domain.BinaryPath(root, pin)); !bytes.Equal(got, engine) {
		t.Fatal("installed engine differs from the served engine")
	}
}
