// SPDX-License-Identifier: MIT

package edge_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge"
	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// recorder is a component that remembers the env it was started with and signals it started.
type recorder struct {
	got     chan component.Env
	stopped bool
}

func (r *recorder) Name() string { return "recorder" }

func (r *recorder) Start(_ context.Context, env *component.Env) (component.Stopper, error) {
	r.got <- *env
	return r, nil
}

func (r *recorder) Stop() error { r.stopped = true; return nil }

func testEnv() *edgeenv.Env {
	return &edgeenv.Env{
		Out: &bytes.Buffer{}, Err: &bytes.Buffer{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		DataDir: "/var/lib/agentsone-edge", ConfigDir: "/etc/agentsone-edge",
	}
}

// serve runs `edge args...` with rec as the only component and returns the env rec got.
func serve(t *testing.T, rec *recorder, args ...string) component.Env {
	t.Helper()
	cmd := edge.NewServeCommand(testEnv(), []component.Component{rec})
	cmd.SetArgs(append([]string{"--admin-socket=" + socketPath(t)}, args...))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	var got component.Env
	select {
	case got = <-rec.got:
	case err := <-done:
		t.Fatalf("edge stopped before starting its components: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("edge error = %v", err)
	}
	return got
}

func TestEdge_StartsComponentsOnTheInnerAddressWithTheDefaultFolders(t *testing.T) {
	t.Parallel()
	rec := &recorder{got: make(chan component.Env, 1)}

	got := serve(t, rec, "--inner-addr=10.77.42.2")

	if got.InnerAddr != netip.MustParseAddr("10.77.42.2") || got.DataDir != "/var/lib/agentsone-edge" || got.ConfigDir != "/etc/agentsone-edge" {
		t.Errorf("env = %+v", got)
	}
	if !rec.stopped {
		t.Error("component not stopped when the edge stopped")
	}
}

func TestEdge_TakesADataDirFlag(t *testing.T) {
	t.Parallel()
	rec := &recorder{got: make(chan component.Env, 1)}

	got := serve(t, rec, "--inner-addr=10.77.42.2", "--data-dir=/srv/edge")

	if got.DataDir != "/srv/edge" {
		t.Errorf("data dir = %q, want /srv/edge", got.DataDir)
	}
}

func TestEdge_PassesEveryDoorAddress(t *testing.T) {
	t.Parallel()
	rec := &recorder{got: make(chan component.Env, 1)}

	got := serve(t, rec, "--inner-addr=10.77.42.2", "--door-addr=machine:7701", "--door-addr=10.77.42.3:7701")

	if diff := cmp.Diff([]string{"machine:7701", "10.77.42.3:7701"}, got.DoorAddrs); diff != "" {
		t.Errorf("door addresses (-want +got):\n%s", diff)
	}
}

func TestEdge_ServesTheAdminSocket(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	ready := make(chan component.Env, 1)
	pinger := &pingComponent{ready: ready}
	cmd := edge.NewServeCommand(testEnv(), []component.Component{pinger})
	cmd.SetArgs([]string{"--inner-addr=10.77.42.2", "--admin-socket=" + path})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	<-ready

	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
	resp, err := client.Get("http://edge/v1/ping/")
	if err != nil {
		t.Fatalf("Get() over the admin socket error = %v", err)
	}
	_ = resp.Body.Close()
	client.CloseIdleConnections()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("edge error = %v", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

func TestEdge_RefusesAnAdminSocketItCannotOpen(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	rec := &recorder{got: make(chan component.Env, 1)}
	cmd := edge.NewServeCommand(testEnv(), []component.Component{rec})
	cmd.SetArgs([]string{"--inner-addr=10.77.42.2", "--admin-socket=" + filepath.Join(file, "admin.sock")})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.ExecuteContext(t.Context()); err == nil {
		t.Fatal("edge started with an admin socket it cannot open")
	}
	if len(rec.got) != 0 {
		t.Error("a component started")
	}
}

// pingComponent adds GET /v1/ping/ to the admin mux.
type pingComponent struct{ ready chan component.Env }

func (p *pingComponent) Name() string { return "ping" }

func (p *pingComponent) Start(_ context.Context, env *component.Env) (component.Stopper, error) {
	env.Admin.HandleFunc("GET /v1/ping/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	p.ready <- *env
	return p, nil
}

func (p *pingComponent) Stop() error { return nil }

// socketPath is a socket path short enough for macOS.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ed")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "admin.sock")
}

func TestEdge_RefusesToStartWithoutAGoodInnerAddress(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{}, {"--inner-addr=edge"}, {"--inner-addr="}} {
		rec := &recorder{got: make(chan component.Env, 1)}
		cmd := edge.NewServeCommand(testEnv(), []component.Component{rec})
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.ExecuteContext(t.Context())
		if err == nil || !strings.Contains(err.Error(), "inner-addr") {
			t.Errorf("%v: error = %v, want one naming --inner-addr", args, err)
		}
		if len(rec.got) != 0 {
			t.Errorf("%v: a component started", args)
		}
	}
}

func TestCommands_RegistersTheEdgeCommand(t *testing.T) {
	t.Parallel()
	cmds := edge.Commands(testEnv())
	if len(cmds) != 1 || cmds[0].Name() != "edge" || cmds[0].Flags().Lookup("inner-addr") == nil {
		t.Fatalf("Commands() = %v, want edge with --inner-addr", cmds)
	}
}
