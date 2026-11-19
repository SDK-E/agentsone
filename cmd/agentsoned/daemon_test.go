// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func TestDaemon_ServesItsModulesOnTheSocket(t *testing.T) {
	short, err := os.MkdirTemp("", "a1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	deps, _ := coretest.NewDeps(t)
	deps.Config.RuntimeDir = short
	deps.Config.API.Addr = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, deps) }()

	client := unixClient(deps.Config.SocketPath())
	// An operation a module implements answers 2xx.
	resp := waitFor(t, client, "http://agentsoned/v1/agents")
	_ = resp.Body.Close() // test response body
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("GET /v1/agents status %d, want 2xx", resp.StatusCode)
	}
	// A path outside the API is not found.
	resp = waitFor(t, client, "http://agentsoned/v1/no-such-resource")
	_ = resp.Body.Close() // test response body
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /v1/no-such-resource status %d, want 404", resp.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleStopsStartedModulesInReverse(t *testing.T) {
	var order []string
	mods := []core.Module{
		&recorder{name: "a", order: &order},
		&recorder{name: "b", order: &order},
		&recorder{name: "c", order: &order, failStart: true},
	}
	l := &lifecycle{mods: mods, log: slog.New(slog.DiscardHandler)}
	if err := l.start(context.Background(), core.NewPorts()); err == nil {
		t.Fatal("start error swallowed")
	}
	l.stop(context.Background())
	want := []string{"provide a", "provide b", "provide c", "migrate a", "migrate b", "migrate c",
		"start a", "start b", "start c", "stop b", "stop a"}
	if len(order) != len(want) {
		t.Fatalf("order %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
}

func TestOpenStoresCreatesTheFourDatabases(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.StateDir = t.TempDir()
	stores, closeAll, err := openStores(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
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

type recorder struct {
	core.Base
	name      string
	order     *[]string
	failStart bool
}

func (r *recorder) Name() string { return r.name }
func (r *recorder) Provide(*core.Ports) {
	*r.order = append(*r.order, "provide "+r.name)
}
func (r *recorder) Migrate(context.Context) error {
	*r.order = append(*r.order, "migrate "+r.name)
	return nil
}
func (r *recorder) Start(context.Context) error {
	*r.order = append(*r.order, "start "+r.name)
	if r.failStart {
		return errors.New("boom")
	}
	return nil
}
func (r *recorder) Stop(context.Context) error {
	*r.order = append(*r.order, "stop "+r.name)
	return nil
}

func unixClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

// waitFor retries until the daemon accepts connections, using a deadline
// instead of sleeping.
func waitFor(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := c.Get(url)
		if err == nil {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never answered: %v", err)
		}
		<-time.After(20 * time.Millisecond)
	}
}
