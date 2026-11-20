// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/ide/adapters/http"
	"github.com/SDK-E/agentsone/internal/ide/domain"
)

var (
	ada = core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}
	bob = core.Caller{Kind: core.CallerHuman, Name: "bob", UID: -1}
)

// servers is a fake of the app's servers: ada's editor runs on socket.
type servers struct {
	socket  string
	running bool
	started []string
	stopped []string
	err     error
}

func (s *servers) Start(_ context.Context, c core.Caller, human string) (domain.Server, error) {
	if err := domain.CanControl(c, human); err != nil {
		return domain.Server{}, err
	}
	s.started = append(s.started, human)
	return domain.Server{Human: human, State: domain.Starting, StartedAt: epoch}, nil
}

func (s *servers) Stop(_ context.Context, c core.Caller, human string) error {
	if err := domain.CanControl(c, human); err != nil {
		return err
	}
	s.stopped = append(s.stopped, human)
	return nil
}

func (s *servers) Get(_ context.Context, human string) (domain.Server, error) {
	if s.err != nil {
		return domain.Server{}, s.err
	}
	if human == "ada" && s.running {
		return domain.Server{Human: "ada", State: domain.Running, StartedAt: epoch}, nil
	}
	if human != "ada" && human != "bob" {
		return domain.Server{}, core.ErrNotFound
	}
	return domain.Server{Human: human, State: domain.Stopped}, nil
}

func (s *servers) List(context.Context) ([]domain.Server, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []domain.Server{
		{Human: "ada", State: domain.Running, StartedAt: epoch},
		{Human: "bob", State: domain.Failed, Error: "the editor stopped: exit status 1"},
	}, nil
}

func (s *servers) Socket(c core.Caller, human string) (string, error) {
	if err := domain.CanControl(c, human); err != nil {
		return "", err
	}
	if !s.running {
		return "", core.ErrUnavailable
	}
	return s.socket, nil
}

// upstream is a fake editor server on a Unix socket. It records the last
// request and answers with the editor's own CSP, or switches protocols
// and echoes for a WebSocket upgrade.
type upstream struct {
	socket string
	last   chan *http.Request
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "idep")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { coretest.Check(t, func() error { return os.RemoveAll(dir) }) })
	u := &upstream{socket: filepath.Join(dir, "server.sock"), last: make(chan *http.Request, 8)}
	ln, err := net.Listen("unix", u.socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.last <- r.Clone(context.Background())
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			conn, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}
			defer coretest.Close(t, conn)
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = rw.Flush()
			line, _ := rw.ReadString('\n')
			_, _ = rw.WriteString("echo " + line)
			_ = rw.Flush()
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-src 'self';")
		_, _ = io.WriteString(w, "workbench")
	}))
	coretest.Close(t, srv.Listener)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return u
}

// door stands in for the API layer: the default page headers, then the
// caller the session named.
func door(c core.Caller, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	})
}

func proxyFor(t *testing.T, s *servers, c core.Caller) http.Handler {
	t.Helper()
	p := httpadapter.NewProxy(s, slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	route := p.Route()
	mux.Handle(route.Pattern, route.Handler)
	return door(c, mux)
}

func TestProxyForwardsTheOwnerToTheirEditorAndAllowsSameOriginFraming(t *testing.T) {
	up := newUpstream(t)
	h := proxyFor(t, &servers{socket: up.socket, running: true}, ada)
	req := httptest.NewRequest(http.MethodGet, "http://agents.example.com/ide/ada/static/x.js?v=1", nil)
	req.Header.Set("Authorization", "Bearer abc")
	req.Header.Set("X-Agentsone-Via", "edge")
	req.Header.Set("Cookie", "agentsone_session=abc; vscode-secret-key-path=keep")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "workbench" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
	got := <-up.last
	if got.URL.Path != "/ide/ada/static/x.js" || got.URL.RawQuery != "v=1" {
		t.Fatalf("upstream path %q query %q", got.URL.Path, got.URL.RawQuery)
	}
	if got.Host != "agents.example.com" || got.Header.Get("X-Forwarded-Host") != "agents.example.com" {
		t.Fatalf("upstream host %q forwarded %q", got.Host, got.Header.Get("X-Forwarded-Host"))
	}
	if got.Header.Get("Authorization") != "" || got.Header.Get("X-Agentsone-Via") != "" {
		t.Fatalf("credentials reached the editor: %v", got.Header)
	}
	if _, err := got.Cookie("agentsone_session"); err == nil {
		t.Fatal("the session cookie reached the editor")
	}
	if c, err := got.Cookie("vscode-secret-key-path"); err != nil || c.Value != "keep" {
		t.Fatalf("the editor's own cookie was dropped: %v", err)
	}
	csp := rec.Header().Values("Content-Security-Policy")
	if len(csp) != 1 || csp[0] != "default-src 'self'; frame-src 'self'; frame-ancestors 'self'" {
		t.Fatalf("csp %q", csp)
	}
	if xfo := rec.Header().Values("X-Frame-Options"); len(xfo) != 1 || xfo[0] != "SAMEORIGIN" {
		t.Fatalf("x-frame-options %q", xfo)
	}
}

func TestProxyKeepsOthersOut(t *testing.T) {
	up := newUpstream(t)
	cases := map[string]struct {
		caller core.Caller
		s      *servers
		want   int
	}{
		"another human": {bob, &servers{socket: up.socket, running: true}, http.StatusForbidden},
		"not signed in": {core.Anonymous, &servers{socket: up.socket, running: true}, http.StatusUnauthorized},
		"not running":   {ada, &servers{socket: up.socket}, http.StatusServiceUnavailable},
		"not answering": {ada, &servers{socket: up.socket + ".gone", running: true}, http.StatusBadGateway},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			proxyFor(t, c.s, c.caller).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ide/ada/", nil))
			if rec.Code != c.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, c.want, rec.Body)
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
				t.Fatalf("content type %q", rec.Header().Get("Content-Type"))
			}
			select {
			case r := <-up.last:
				t.Fatalf("request reached the editor: %s", r.URL)
			default:
			}
		})
	}
}

func TestProxyShowsTheIDEPanelAPlainPageItMayFrame(t *testing.T) {
	up := newUpstream(t)
	cases := map[string]struct {
		caller core.Caller
		s      *servers
		status int
		text   string
	}{
		"not running":   {ada, &servers{socket: up.socket}, http.StatusServiceUnavailable, "Your editor is not running. Start it from the IDE panel."},
		"not answering": {ada, &servers{socket: up.socket + ".gone", running: true}, http.StatusBadGateway, "Your editor did not answer. Stop it and start it again from the IDE panel."},
		"not signed in": {core.Anonymous, &servers{socket: up.socket, running: true}, http.StatusUnauthorized, "Sign in to open your editor."},
		"another human": {bob, &servers{socket: up.socket, running: true}, http.StatusForbidden, "This editor belongs to someone else."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ide/ada/", nil)
			req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
			rec := httptest.NewRecorder()
			proxyFor(t, c.s, c.caller).ServeHTTP(rec, req)
			if rec.Code != c.status || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
			}
			body := rec.Body.String()
			if !strings.Contains(body, "<p>"+c.text+"</p>") || !strings.Contains(body, `<meta name="color-scheme" content="light dark">`) ||
				!strings.Contains(body, `name="viewport"`) || strings.Contains(body, "<script") {
				t.Fatalf("page %s", body)
			}
			if xfo := rec.Header().Values("X-Frame-Options"); len(xfo) != 1 || xfo[0] != "SAMEORIGIN" {
				t.Fatalf("x-frame-options %q", xfo)
			}
			// The one inline style is allowed by its hash, nothing else.
			_, style, _ := strings.Cut(body, "<style>")
			style, _, _ = strings.Cut(style, "</style>")
			sum := sha256.Sum256([]byte(style))
			want := "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; frame-ancestors 'self'"
			if csp := rec.Header().Values("Content-Security-Policy"); len(csp) != 1 || csp[0] != want {
				t.Fatalf("csp %q, want %q", csp, want)
			}
		})
	}
}

func TestProxyPassesWebSockets(t *testing.T) {
	up := newUpstream(t)
	srv := httptest.NewServer(proxyFor(t, &servers{socket: up.socket, running: true}, ada))
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer coretest.Close(t, conn)
	_, err = io.WriteString(conn, "GET /ide/ada/?reconnectionToken=x HTTP/1.1\r\nHost: agents.example.com\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close the answer: %v", err)
		}
	}()
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d", res.StatusCode)
	}
	if _, err := io.WriteString(conn, "hello\n"); err != nil {
		t.Fatal(err)
	}
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) || line != "echo hello\n" {
		t.Fatalf("echo %q %v", line, err)
	}
}
