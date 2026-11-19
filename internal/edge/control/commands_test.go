// SPDX-License-Identifier: MIT

package control_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/control"
	"github.com/SDK-E/agentsone/internal/edge/control/adapters/edgeadmin"
	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/controltest"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// edgeWithControl starts the control component on an admin mux served over a Unix socket, like
// the edge does, and returns the socket path.
func edgeWithControl(t *testing.T) (string, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	env := &component.Env{DataDir: dir, Admin: http.NewServeMux(), Log: slog.New(slog.DiscardHandler)}
	stop, err := control.Component{}.Start(t.Context(), env)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = stop.Stop() })
	sock := filepath.Join(dir, "admin.sock")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: env.Admin, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock, dir
}

// The passphrase hash lives only in the edge data folder (a volume only the edge mounts), in a
// file only the edge user can read, and travels only over the admin socket (a volume only the
// edge and control mount). The compose e2e proves the machine mounts neither.
func TestComponent_KeepsTheHashInTheEdgeDataFolderForTheEdgeUserOnly(t *testing.T) {
	t.Parallel()
	sock, dir := edgeWithControl(t)
	c := edgeadmin.New(sock, time.Second)

	if err := c.Create(t.Context(), "$argon2id$v=19$m=64,t=1,p=1$c2FsdA$aGFzaA"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "control-credential"))
	if err != nil {
		t.Fatalf("no hash file in the edge data folder: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("hash file mode = %v, want 0600", info.Mode().Perm())
	}
	if control.Component.Name(control.Component{}) == "" {
		t.Fatal("component has no name")
	}
}

func TestReset_ClearsThePassphraseThroughTheAdminSocket(t *testing.T) {
	t.Parallel()
	sock, _ := edgeWithControl(t)
	c := edgeadmin.New(sock, time.Second)
	if err := c.Create(t.Context(), "$argon2id$v=19$m=64,t=1,p=1$c2FsdA$aGFzaA"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	env := &edgeenv.Env{Out: &out, Err: io.Discard, Log: slog.New(slog.DiscardHandler)}
	cmd := control.Commands(env)[0]
	cmd.SetArgs([]string{"reset", "--admin-socket", sock})
	cmd.SetOut(io.Discard)

	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("control reset: %v", err)
	}

	if _, err := c.Get(t.Context()); !errors.Is(err, app.ErrNoCredential) {
		t.Fatalf("Get() after reset error = %v, want %v", err, app.ErrNoCredential)
	}
	if !strings.Contains(out.String(), "docker compose logs control") {
		t.Fatalf("reset output does not say where the new code is: %q", out.String())
	}
}

func TestReset_WithoutAnEdgeFails(t *testing.T) {
	t.Parallel()
	env := &edgeenv.Env{Out: io.Discard, Err: io.Discard, Log: slog.New(slog.DiscardHandler)}
	cmd := control.Commands(env)[0]
	cmd.SetArgs([]string{"reset", "--admin-socket", filepath.Join(t.TempDir(), "none.sock")})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.ExecuteContext(t.Context()); err == nil {
		t.Fatal("control reset without an edge: error = nil")
	}
}

func TestControl_RefusesABadTrustedProxy(t *testing.T) {
	t.Parallel()
	env := &edgeenv.Env{Out: io.Discard, Err: io.Discard, Log: slog.New(slog.DiscardHandler)}
	cmd := control.Commands(env)[0]
	cmd.SetArgs([]string{"--trusted-proxy", "not-an-address", "--listen", "127.0.0.1:0"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "--trusted-proxy") {
		t.Fatalf("control with a bad proxy: error = %v", err)
	}
}

func TestServe_ServesThePageAndPrintsTheSetupCodeAtStart(t *testing.T) {
	t.Parallel()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	clock := controltest.NewClock(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	edge := struct {
		*controltest.Credentials
		*controltest.Edge
	}{&controltest.Credentials{}, controltest.NewEdge(clock.Now)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- control.Serve(ctx, ln, control.ServeOptions{
			Edge: edge, Hasher: controltest.Hasher{}, Log: slog.New(slog.NewTextHandler(&lockedWriter{w: &logs}, nil)), Clock: clock,
		})
	}()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/setup", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /setup = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop")
	}
	if !strings.Contains(logs.String(), "setup_code=") {
		t.Fatalf("no setup code in the log:\n%s", logs.String())
	}
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
