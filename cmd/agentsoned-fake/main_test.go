// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
)

func TestParseFlagsDefaults(t *testing.T) {
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.addr != "127.0.0.1:7700" || cfg.fixtures != "contract/fixtures/api" || len(cfg.origins) != 2 || cfg.origins[0] != viteOrigin {
		t.Fatalf("defaults %+v", cfg)
	}
}

func TestRunFailsWithoutFixtures(t *testing.T) {
	cfg := config{addr: "127.0.0.1:0", fixtures: filepath.Join(t.TempDir(), "absent")}
	if err := run(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("ran without a fixtures folder")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// firstReadRoute returns a GET operation of the embedded contract and a
// concrete path for it.
func firstReadRoute(t *testing.T) (api.Route, string) {
	t.Helper()
	for _, r := range api.Routes {
		if r.Method == http.MethodGet && !r.Stream {
			return r, pathParam.ReplaceAllString(r.Path, "x")
		}
	}
	t.Skip("the contract has no GET operation")
	return api.Route{}, ""
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

func TestRunServesUntilCancelled(t *testing.T) {
	route, path := firstReadRoute(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, route.OperationID+".json"), []byte(`{"status":200,"body":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{addr: freeAddr(t), fixtures: dir, streamInterval: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	waitForStatus(t, "http://"+cfg.addr+path, http.StatusOK)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(shutdownGrace + time.Second):
		t.Fatal("run did not stop after cancel")
	}
}

func waitForStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(url) // #nosec G107 -- loopback test server
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == want {
				return
			}
			t.Fatalf("status %d, want %d", res.StatusCode, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never answered", url)
}
