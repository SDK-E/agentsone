// SPDX-License-Identifier: MIT

package cli_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func TestParseEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, raw  string
		wantSocket string
		wantBase   string
		wantErr    bool
	}{
		{"default is the daemon socket", "", "/run/agentsone/api.sock", "http://agentsone", false},
		{"unix scheme", "unix:///tmp/a.sock", "/tmp/a.sock", "http://agentsone", false},
		{"bare path", "/tmp/b.sock", "/tmp/b.sock", "http://agentsone", false},
		{"host and port", "127.0.0.1:7700", "", "http://127.0.0.1:7700", false},
		{"http url", "http://127.0.0.1:7700/", "", "http://127.0.0.1:7700", false},
		{"https url", "https://agents.example", "", "https://agents.example", false},
		{"unix without path", "unix://", "", "", true},
		{"nonsense", "banana", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := cli.ParseEndpoint(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseEndpoint(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if err == nil && (got.Socket != tt.wantSocket || got.Base != tt.wantBase) {
				t.Errorf("ParseEndpoint(%q) = %+v, want socket %q base %q", tt.raw, got, tt.wantSocket, tt.wantBase)
			}
		})
	}
}

func TestClient_DoSendsTheRequestWithTheBearerToken(t *testing.T) {
	t.Parallel()
	var gotAuth, gotType, gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotType, gotQuery = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(ts.Close)
	ep, _ := cli.ParseEndpoint(ts.URL)
	c := cli.NewClient(ep, "tok")

	status, body, err := c.Do(t.Context(), domain.Request{
		Method: http.MethodPost, Path: "/v1/agents", Query: url.Values{"a": {"1"}}, Body: []byte(`{"name":"bo"}`),
	})

	if err != nil || status != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("Do() = %d %q %v", status, body, err)
	}
	if gotAuth != "Bearer tok" || gotType != "application/json" || gotQuery != "a=1" {
		t.Errorf("auth %q type %q query %q", gotAuth, gotType, gotQuery)
	}
}

func TestClient_DoOverAUnixSocket(t *testing.T) {
	t.Parallel()
	sock := filepath.Join(t.TempDir(), "api.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Minute, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("a socket call carries no token unless asked")
		}
		_, _ = w.Write([]byte(`"pong"`))
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	ep, _ := cli.ParseEndpoint("unix://" + sock)

	status, body, err := cli.NewClient(ep, "").Do(t.Context(), domain.Request{Method: http.MethodGet, Path: "/v1/ping"})

	if err != nil || status != 200 || string(body) != `"pong"` {
		t.Fatalf("Do() = %d %q %v", status, body, err)
	}
}

func TestClient_DoNamesTheSocketWhenTheDaemonIsDown(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "none.sock")
	ep, _ := cli.ParseEndpoint(missing)

	_, _, err := cli.NewClient(ep, "").Do(t.Context(), domain.Request{Method: http.MethodGet, Path: "/v1/x"})

	if err == nil || !strings.Contains(err.Error(), "cannot reach the daemon at "+missing) {
		t.Fatalf("err = %v, want a plain message naming the socket", err)
	}
}

func TestClient_StreamPassesEachEventAndSkipsHeartbeats(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": hb\n\nevent: a\ndata: {\"n\":1}\n\ndata: {\"n\":\ndata: 2}\n\n"))
	}))
	t.Cleanup(ts.Close)
	ep, _ := cli.ParseEndpoint(ts.URL)
	var got []string

	err := cli.NewClient(ep, "").Stream(t.Context(), domain.Request{Method: http.MethodGet, Path: "/v1/events"}, func(b []byte) error {
		got = append(got, string(b))
		return nil
	})

	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(got) != 2 || got[0] != `{"n":1}` || got[1] != "{\"n\":\n2}" {
		t.Fatalf("events = %q", got)
	}
}

func TestClient_StreamReturnsAProblemAnswer(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"type":"urn:agentsone:problem:forbidden","title":"Forbidden","status":403,"detail":"no"}`))
	}))
	t.Cleanup(ts.Close)
	ep, _ := cli.ParseEndpoint(ts.URL)

	err := cli.NewClient(ep, "").Stream(t.Context(), domain.Request{Method: http.MethodGet, Path: "/v1/events"}, func([]byte) error { return nil })

	var p *domain.Problem
	if !errors.As(err, &p) || p.Status != 403 {
		t.Fatalf("err = %v, want a 403 problem", err)
	}
}

func TestClient_StreamEndsQuietlyWhenTheUserStops(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)
	ep, _ := cli.ParseEndpoint(ts.URL)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	err := cli.NewClient(ep, "").Stream(ctx, domain.Request{Method: http.MethodGet, Path: "/v1/events"}, func([]byte) error {
		close(started)
		cancel()
		return nil
	})

	<-started
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil after cancel", err)
	}
}

func TestReadEvents_StopsOnTheHandlersError(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")

	err := cli.ReadEvents(strings.NewReader("data: 1\n\ndata: 2\n\n"), func([]byte) error { return boom })

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestReadEvents_FlushesAFinalEventWithoutBlankLine(t *testing.T) {
	t.Parallel()
	var got []string

	_ = cli.ReadEvents(strings.NewReader("data: last"), func(b []byte) error { got = append(got, string(b)); return nil })

	if len(got) != 1 || got[0] != "last" {
		t.Fatalf("events = %q", got)
	}
}

func TestReadToken_FromFlagFileOrNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "tok")
	if err := os.WriteFile(file, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, token, tokenFile, want string
		wantErr                      bool
	}{
		{"none", "", "", "", false},
		{"flag", "abc", "", "abc", false},
		{"file is trimmed", "", file, "from-file", false},
		{"flag wins over file", "abc", file, "abc", false},
		{"missing file", "", filepath.Join(dir, "nope"), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := cli.ReadToken(tt.token, tt.tokenFile)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("ReadToken() = %q, %v; want %q, err %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
