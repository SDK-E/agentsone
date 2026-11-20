// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/machine/machinetest"
)

// startDaemon runs serve on deps until the test ends and returns a client
// for its socket once the daemon logged that it is ready. The socket lives
// in the test's own temporary directory. Test names stay short so the
// socket path fits the macOS limit of 104 bytes.
func startDaemon(t *testing.T, deps core.Deps) *http.Client {
	t.Helper()
	deps.Config.RuntimeDir = t.TempDir()
	deps.Config.API.Addr = "127.0.0.1:0"
	logs, _ := newMessageLog()
	deps.Log = slog.New(logs)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveModules(ctx, deps, testModules(t, deps)) }()
	select {
	case <-logs.ready:
	case err := <-done:
		cancel()
		t.Fatalf("daemon stopped before it was ready: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return unixClient(deps.Config.SocketPath())
}

// testModules are the daemon's real modules with the machine on a no-op
// system, so tests never read or change the host's packages and files.
func testModules(t *testing.T, deps core.Deps) []core.Module {
	t.Helper()
	mods := modules(deps)
	for i, m := range mods {
		if m.Name() == "machine" {
			mods[i] = machinetest.New(deps)
			return mods
		}
	}
	t.Fatal("the daemon has no machine module")
	return nil
}

func get(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // test response body
	return resp.StatusCode
}

func TestDaemon_Serves(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	client := startDaemon(t, deps)
	// An operation a module implements answers 2xx.
	if code := get(t, client, "http://agentsoned/v1/agents"); code < 200 || code > 299 {
		t.Fatalf("GET /v1/agents status %d, want 2xx", code)
	}
	// A path outside the API is not found.
	if code := get(t, client, "http://agentsoned/v1/no-such-resource"); code != http.StatusNotFound {
		t.Fatalf("GET /v1/no-such-resource status %d, want 404", code)
	}
}

// The agents module runs agents through the runner supervisor: a lever
// that only a wired runtime can pull (freeze all) works, where without it
// the daemon answered "runner supervisor not wired".
func TestDaemon_Runtime(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	startDaemon(t, deps)
	host := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHost, Name: "agentsone", UID: -1})
	if err := deps.Ports.Freezer.Freeze(host, core.FreezeScope{Kind: core.FreezeAll}); err != nil {
		t.Fatalf("freeze all: %v", err)
	}
	if err := deps.Ports.Freezer.Release(host, core.FreezeScope{Kind: core.FreezeAll}); err != nil {
		t.Fatalf("release all: %v", err)
	}
}

func TestOpenStoresCreatesTheFourDatabases(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.StateDir = t.TempDir()
	stores, closeAll, err := openStores(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeAll(); err != nil {
			t.Errorf("close stores: %v", err)
		}
	})
	for _, name := range []string{"core", "bus", "memory", "audit"} {
		if _, err := os.Stat(filepath.Join(cfg.StateDir, name+".db")); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if stores.Core == nil || stores.Bus == nil || stores.Memory == nil || stores.Audit == nil {
		t.Fatal("missing store")
	}
}

func TestNewLoggerRejectsUnknownFormat(t *testing.T) {
	if _, err := newLogger(core.LogConfig{Level: "info", Format: "xml"}); err == nil {
		t.Fatal("xml accepted")
	}
	if _, err := newLogger(core.LogConfig{Level: "loud", Format: "text"}); err == nil {
		t.Fatal("bad level accepted")
	}
	if _, err := newLogger(core.LogConfig{Level: "debug", Format: "json"}); err != nil {
		t.Fatal(err)
	}
}

func unixClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}
