// SPDX-License-Identifier: MIT

package process_test

import (
	"bytes"
	"context"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/adapters/process"
	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

type oneShot struct{ interval *time.Duration }

func (o oneShot) Run(ctx context.Context, d app.Deps) error {
	dr, err := domain.NewDraft("agentsone.clock.tick", "", map[string]string{"minute": "2026-11-20T00:01:00Z"})
	if err != nil {
		return err
	}
	if err := d.Publisher.Publish(ctx, dr); err != nil {
		return err
	}
	d.Log.InfoContext(ctx, "published", "interval", o.interval.String())
	return nil
}

func socketServer(t *testing.T, hits *atomic.Int32) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "emk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}

func configure(fs *flag.FlagSet) app.Source {
	iv := fs.Duration("interval", time.Minute, "how often to sample")
	return &lazy{interval: iv}
}

// lazy reads the flag value after parsing.
type lazy struct{ interval *time.Duration }

func (l *lazy) Run(ctx context.Context, d app.Deps) error {
	return oneShot{interval: l.interval}.Run(ctx, d)
}

func TestRun_WiresTheSourceToTheDaemon(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	sock := socketServer(t, &hits)
	var stderr bytes.Buffer

	code := process.Run(context.Background(), "clock", []string{"--socket", sock, "--interval", "5s"}, &stderr, configure)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}
	if hits.Load() != 1 {
		t.Fatalf("daemon got %d events, want 1", hits.Load())
	}
	if !strings.Contains(stderr.String(), `"interval":"5s"`) {
		t.Fatalf("log = %q, want JSON log lines carrying the parsed flag", stderr.String())
	}
}

func TestRun_BadFlagsExitTwo(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := process.Run(context.Background(), "clock", []string{"--nope"}, &stderr, configure); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("no usage message on stderr")
	}
}

func TestRun_HelpExitsZero(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := process.Run(context.Background(), "clock", []string{"-h"}, &stderr, configure); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

type failing struct{}

func (failing) Run(context.Context, app.Deps) error { return context.DeadlineExceeded }

func TestRun_SourceFailureExitsOne(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	code := process.Run(context.Background(), "clock", nil, &stderr, func(*flag.FlagSet) app.Source { return failing{} })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "source stopped") {
		t.Fatalf("log = %q, want a line saying the source stopped", stderr.String())
	}
}

func TestSleeper_ReturnsWhenCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (process.Timer{}).Sleep(ctx, time.Hour); err == nil {
		t.Fatal("Sleep on a cancelled context returned nil")
	}
}

func TestSleeper_WaitsForTheDuration(t *testing.T) {
	t.Parallel()
	if err := (process.Timer{}).Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("Sleep error = %v", err)
	}
}
