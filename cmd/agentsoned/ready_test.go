// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// messageLog is a slog handler that records messages and closes ready when it
// sees the readiness message.
type messageLog struct {
	slog.Handler
	mu       *sync.Mutex
	messages *[]string
	ready    chan struct{}
	once     *sync.Once
}

func newMessageLog() (*messageLog, func() []string) {
	var messages []string
	l := &messageLog{
		Handler:  slog.DiscardHandler,
		mu:       &sync.Mutex{},
		messages: &messages,
		ready:    make(chan struct{}),
		once:     &sync.Once{},
	}
	return l, func() []string {
		l.mu.Lock()
		defer l.mu.Unlock()
		return append([]string(nil), messages...)
	}
}

func (l *messageLog) Enabled(context.Context, slog.Level) bool { return true }

func (l *messageLog) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	*l.messages = append(*l.messages, r.Message)
	l.mu.Unlock()
	if r.Message == "daemon ready" || r.Message == readyMarker {
		l.once.Do(func() { close(l.ready) })
	}
	return nil
}

func TestDaemon_ReadyLog(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	deps.Config.RuntimeDir = t.TempDir()
	deps.Config.API.Addr = "127.0.0.1:0"
	logs, messages := newMessageLog()
	deps.Log = slog.New(logs)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveModules(ctx, deps, testModules(t, deps)) }()

	select {
	case <-logs.ready:
	case err := <-done:
		t.Fatalf("daemon stopped before it was ready: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, m := range messages() {
		if m == readyMarker {
			t.Fatalf("the marker %s is reserved for the readiness signal and must not be logged", readyMarker)
		}
		seen = seen || m == "daemon ready"
	}
	if !seen {
		t.Fatal("the daemon did not log \"daemon ready\"")
	}
}

// readyMarker is reserved for the readiness signal; nothing else may print it.
const readyMarker = "AGENTSONE-READY"
