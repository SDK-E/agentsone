// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	authdomain "github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/daemon"
)

// lockedLog is the daemon's log, written by its goroutines and read by the
// test.
type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// revoker is the real auth module's token revocation, which the port does
// not name.
type revoker interface {
	RevokeToken(ctx context.Context, by core.Caller, id string) error
}

// The real daemon, with the real auth module's tokens on a fake clock,
// refuses an expired and a revoked token with 401 on both listeners and on
// every kind of path, public operations and the interface included: the
// request never goes on as no one, or as the process the socket names. The
// answers and the daemon's log never hold the token.
func TestDaemon_RefusesAnExpiredOrRevokedTokenOnBothListeners(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	short, err := os.MkdirTemp("", "a2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { coretest.Check(t, func() error { return os.RemoveAll(short) }) })
	deps.Config.RuntimeDir, deps.Config.LibDir = short, t.TempDir()
	logs := &lockedLog{}
	deps.Log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan []net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, deps, daemon.Modules(deps), func(a []net.Addr) { ready <- a }) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon stopped with %v", err)
		}
	})
	var tcp string
	select {
	case addrs := <-ready:
		for _, a := range addrs {
			if a.Network() == "tcp" {
				tcp = a.String()
			}
		}
	case err := <-done:
		t.Fatalf("daemon did not start: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("daemon not ready after 30 s")
	}
	sock := deps.Config.SocketPath()
	// No keep-alive: an open connection would hold up the daemon's shutdown.
	listeners := map[string]*http.Client{
		"tcp": {Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", tcp)
		}}},
		"socket": {Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}},
	}
	call := func(listener, path, tok string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://agentsoned"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := listeners[listener].Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Errorf("%s GET %s: read body: %v", listener, path, err)
		}
		if err := res.Body.Close(); err != nil {
			t.Errorf("%s GET %s: close body: %v", listener, path, err)
		}
		return res.StatusCode, string(body)
	}

	scout := core.Caller{Kind: core.CallerAgent, Name: "scout", UID: -1}
	issue := func() string {
		tok, err := deps.Ports.Tokens.Issue(t.Context(), scout, []string{"tasks:read"}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok.Value
	}
	expiring, revoked := issue(), issue()
	for listener := range listeners {
		for _, tok := range []string{expiring, revoked} {
			if code, body := call(listener, "/v1/tasks", tok); code != http.StatusOK {
				t.Fatalf("%s: a fresh token on listTasks = %d %s, want 200", listener, code, body)
			}
		}
	}

	revokedID, _, err := authdomain.ParseTokenValue(revoked)
	if err != nil {
		t.Fatal(err)
	}
	ada := core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}
	if err := deps.Ports.Tokens.(revoker).RevokeToken(t.Context(), ada, revokedID); err != nil {
		t.Fatal(err)
	}
	fakes.Clock.Advance(time.Hour)

	paths := map[string]string{
		"a guarded operation": "/v1/tasks",
		"a public operation":  "/v1/setup",
		"an interface path":   "/agents/scout/chat",
		"an unknown v1 path":  "/v1/nothing",
	}
	secret := func(tok string) string {
		_, s, err := authdomain.ParseTokenValue(tok)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	tokens := map[string]string{"expired": expiring, "revoked": revoked}
	for listener := range listeners {
		for kind, path := range paths {
			for name, tok := range tokens {
				code, body := call(listener, path, tok)
				var problem struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal([]byte(body), &problem); err != nil || code != http.StatusUnauthorized || problem.Type != "urn:agentsone:problem:unauthenticated" {
					t.Errorf("%s, %s token on %s (%s) = %d %s, want 401 unauthenticated", listener, name, kind, path, code, body)
				}
				if strings.Contains(body, secret(tok)) {
					t.Errorf("%s, %s token on %s: the answer holds the token: %s", listener, name, kind, body)
				}
			}
		}
	}
	for name, tok := range tokens {
		if strings.Contains(logs.String(), secret(tok)) {
			t.Errorf("the daemon's log holds the %s token", name)
		}
	}
}
