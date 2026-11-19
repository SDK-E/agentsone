// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func listening(t *testing.T, edge string) (*api.Running, core.Deps) {
	t.Helper()
	short, err := os.MkdirTemp("", "a2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	deps, _ := coretest.NewDeps(t)
	cfg, err := core.ParseConfig([]byte("[edge]\ntrusted_proxies = [" + edge + "]\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeDir, cfg.StateDir, cfg.LibDir, cfg.API.Addr = short, deps.Config.StateDir, t.TempDir(), "127.0.0.1:0"
	deps.Config = &cfg
	srv, err := api.NewServer([]core.Module{setupModule()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	errs := run.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := run.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		for range 2 {
			if err := <-errs; !errors.Is(err, http.ErrServerClosed) {
				t.Error(err)
			}
		}
	})
	return run, deps
}

func TestServer_SocketIsOpenToEveryLocalUser(t *testing.T) {
	t.Parallel()
	_, deps := listening(t, `"10.0.0.1"`)
	st, err := os.Stat(deps.Config.SocketPath())
	if err != nil || st.Mode().Perm() != 0o666 {
		t.Fatalf("socket mode %v err %v", st.Mode(), err)
	}
}

func TestServer_ServesOnTheSocketAndOnTCP(t *testing.T) {
	t.Parallel()
	run, deps := listening(t, `"172.18.0.2"`)
	sock := deps.Config.SocketPath()
	unixc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	resp, err := unixc.Get("http://agentsoned/v1/setup")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // test response body
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("socket status %d", resp.StatusCode)
	}
	tcp := run.Addrs()[1].String()
	resp, err = http.Get("http://" + tcp + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // test response body
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tcp without a session: %d", resp.StatusCode)
	}
	resp, err = http.Get("http://" + tcp + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // test response body
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("no CSP on the interface: %v", resp.Header)
	}
}

func TestNewServer_RejectsAnEdgeThatIsNotOneAddress(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"not-an-ip", "172.18.0.0/16"} {
		deps, _ := coretest.NewDeps(t)
		cfg, err := core.ParseConfig([]byte("[edge]\ntrusted_proxies = [\"" + bad + "\"]\n"))
		if err != nil {
			t.Fatal(err)
		}
		deps.Config = &cfg
		if _, err := api.NewServer(nil, deps); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestServer_HandlerIsTheComposedModulesWithoutIdentityRules(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	srv, err := api.NewServer([]core.Module{setupModule()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if rec := serve(t, srv.Handler(), http.MethodGet, "/v1/extra"); rec.Code != http.StatusNoContent {
		t.Fatalf("got %d", rec.Code)
	}
	if rec := serve(t, srv.SocketHandler(), http.MethodGet, "/v1/extra"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("socket chain without peer: %d", rec.Code)
	}
	if rec := serve(t, srv.TCPHandler(), http.MethodGet, "/v1/extra"); rec.Code != http.StatusForbidden {
		t.Fatalf("tcp chain from a non-loopback test address: %d", rec.Code)
	}
}
