// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	supdomain "github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/machine"
	"github.com/SDK-E/agentsone/internal/machine/machinetest"
)

// startDaemon runs serve on deps until the test ends and returns a client
// for its socket once the daemon logged that it is ready. The socket lives
// in the test's own temporary directory. Test names stay short so the
// socket path fits the macOS limit of 104 bytes.
func startDaemon(t *testing.T, deps core.Deps) *http.Client {
	t.Helper()
	return startDaemonWith(t, deps, softBoot)
}

// softBoot is a boot probe that found no cgroup v2, as on a host without it.
var softBoot = process.Boot{Mode: supdomain.LimitsSoft, Reason: process.ReasonNoCgroupV2}

// startDaemonWith is startDaemon with limits as the boot probe's answer. A
// test passes a Boot in place of probing the host.
func startDaemonWith(t *testing.T, deps core.Deps, limits process.Boot) *http.Client {
	t.Helper()
	deps.Config.RuntimeDir = t.TempDir()
	deps.Config.API.Addr = "127.0.0.1:0"
	logs, _ := newMessageLog()
	deps.Log = slog.New(logs)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveModules(ctx, deps, testModulesWith(t, deps, limits)) }()
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
	return testModulesWith(t, deps, softBoot)
}

// testModulesWith is testModules with limits as the boot probe's answer.
// The no-op machine reports the agent limits the real wiring gave the
// machine module.
func testModulesWith(t *testing.T, deps core.Deps, limits process.Boot) []core.Module {
	t.Helper()
	mods := modules(deps, limits)
	for i, m := range mods {
		if wired, ok := m.(*machine.Module); ok {
			mods[i] = machinetest.New(deps, machine.WithAgentLimits(wired.AgentLimits()))
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

// The daemon reports in its machine metrics how custom agents' limits hold,
// as the boot probe decided: hard without a reason, soft with plain words
// of 1 to 500 characters. The schema cannot say "a reason only when soft",
// so this test does.
func TestDaemon_Limits(t *testing.T) {
	for name, tc := range map[string]struct {
		boot   process.Boot
		want   api.AgentLimitsHoldMode
		reason bool
	}{
		"hard": {process.Boot{Mode: supdomain.LimitsHard, Reason: process.ReasonWritable, Root: t.TempDir()}, api.AgentLimitsHoldModeHard, false},
		"soft": {process.Boot{Mode: supdomain.LimitsSoft, Reason: process.ReasonReadOnly}, api.AgentLimitsHoldModeSoft, true},
	} {
		t.Run(name, func(t *testing.T) {
			deps, _ := coretest.NewDeps(t)
			client := startDaemonWith(t, deps, tc.boot)
			resp, err := client.Get("http://agentsoned/v1/machine/metrics")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }() // test response body
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /v1/machine/metrics status %d, want 200", resp.StatusCode)
			}
			var m api.MachineMetrics
			if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
				t.Fatal(err)
			}
			got := m.AgentLimits
			if got == nil || got.Mode != tc.want {
				t.Fatalf("agent_limits = %+v, want mode %s", got, tc.want)
			}
			if !tc.reason {
				if got.Reason != nil {
					t.Fatalf("hard limits came with a reason: %q", *got.Reason)
				}
				return
			}
			if got.Reason == nil {
				t.Fatal("soft limits came without a reason")
			}
			if n := utf8.RuneCountInString(*got.Reason); n < 1 || n > 500 {
				t.Fatalf("reason of %d characters, want 1 to 500: %q", n, *got.Reason)
			}
			if *got.Reason != process.ReasonReadOnly.Advice() {
				t.Fatalf("reason %q, want the read-only advice", *got.Reason)
			}
		})
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
