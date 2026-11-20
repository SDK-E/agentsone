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

// testEnv is the edge env with a data folder of its own: the edge writes its gateway key there.
func testEnv(t *testing.T) *edgeenv.Env {
	t.Helper()
	return &edgeenv.Env{
		Out: &bytes.Buffer{}, Err: &bytes.Buffer{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		DataDir: t.TempDir(), ConfigDir: "/etc/agentsone-edge",
	}
}

// serve runs `edge args...` with rec as the only component and returns the env rec got.
func serve(t *testing.T, env *edgeenv.Env, rec *recorder, args ...string) component.Env {
	t.Helper()
	cmd := edge.NewServeCommand(env, []component.Component{rec})
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

	env := testEnv(t)

	got := serve(t, env, rec, "--inner-addr=10.77.42.2")

	if got.InnerAddr != netip.MustParseAddr("10.77.42.2") || got.DataDir != env.DataDir || got.ConfigDir != "/etc/agentsone-edge" {
		t.Errorf("env = %+v", got)
	}
	if len(got.TokenKey) == 0 {
		t.Error("components got no gateway key")
	}
	if !rec.stopped {
		t.Error("component not stopped when the edge stopped")
	}
}

func TestEdge_TakesADataDirFlag(t *testing.T) {
	t.Parallel()
	rec := &recorder{got: make(chan component.Env, 1)}

	dir := t.TempDir()

	got := serve(t, testEnv(t), rec, "--inner-addr=10.77.42.2", "--data-dir="+dir)

	if got.DataDir != dir {
		t.Errorf("data dir = %q, want %q", got.DataDir, dir)
	}
}

func TestEdge_PassesEveryDoorAddress(t *testing.T) {
	t.Parallel()
	rec := &recorder{got: make(chan component.Env, 1)}

	got := serve(t, testEnv(t), rec, "--inner-addr=10.77.42.2", "--door-addr=machine:7701", "--door-addr=10.77.42.3:7701")

	if diff := cmp.Diff([]string{"machine:7701", "10.77.42.3:7701"}, got.DoorAddrs); diff != "" {
		t.Errorf("door addresses (-want +got):\n%s", diff)
	}
}

func TestEdge_ServesTheAdminSocket(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	ready := make(chan component.Env, 1)
	pinger := &pingComponent{ready: ready}
	cmd := edge.NewServeCommand(testEnv(t), []component.Component{pinger})
	cmd.SetArgs([]string{"--inner-addr=10.77.42.2", "--admin-socket=" + path})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("edge stopped before starting its components: %v", err)
	}

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
	cmd := edge.NewServeCommand(testEnv(t), []component.Component{rec})
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

func TestEdge_PassesTheEgressDenyRanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want []netip.Prefix
	}{
		{"none by default", nil, nil},
		{"empty, as compose passes an unset setting", []string{"--egress-deny="}, nil},
		{"a list of ipv4 and ipv6 ranges", []string{"--egress-deny=203.0.113.7/32, 2001:db8::7"}, []netip.Prefix{
			netip.MustParsePrefix("203.0.113.7/32"), netip.MustParsePrefix("2001:db8::7/128"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{got: make(chan component.Env, 1)}

			got := serve(t, testEnv(t), rec, append([]string{"--inner-addr=10.77.42.2"}, tt.args...)...)

			if diff := cmp.Diff(tt.want, got.EgressDeny, cmp.Comparer(func(a, b netip.Prefix) bool { return a == b })); diff != "" {
				t.Errorf("egress deny ranges (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEdge_RefusesToStartWithABadEgressDenyRange(t *testing.T) {
	t.Parallel()
	rec := &recorder{got: make(chan component.Env, 1)}
	cmd := edge.NewServeCommand(testEnv(t), []component.Component{rec})
	cmd.SetArgs([]string{"--inner-addr=10.77.42.2", "--admin-socket=" + socketPath(t), "--egress-deny=203.0.113.7/32,host.example"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.ExecuteContext(t.Context())

	if err == nil || !strings.Contains(err.Error(), "--egress-deny") || !strings.Contains(err.Error(), `"host.example"`) {
		t.Errorf("error = %v, want one naming --egress-deny and the bad item", err)
	}
	if len(rec.got) != 0 {
		t.Error("a component started")
	}
}

func TestEdge_RefusesToStartWithoutAGoodInnerAddress(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{}, {"--inner-addr=edge"}, {"--inner-addr="}} {
		rec := &recorder{got: make(chan component.Env, 1)}
		cmd := edge.NewServeCommand(testEnv(t), []component.Component{rec})
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
	cmds := edge.Commands(testEnv(t))
	if len(cmds) != 1 || cmds[0].Name() != "edge" || cmds[0].Flags().Lookup("inner-addr") == nil {
		t.Fatalf("Commands() = %v, want edge with --inner-addr", cmds)
	}
}
