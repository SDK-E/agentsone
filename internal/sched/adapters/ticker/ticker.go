// SPDX-License-Identifier: MIT

// Package ticker drives the schedule clock. It checks for due timers once at
// start, so runs missed while the daemon was down fire after a restart, and
// then on every beat.
package ticker

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultInterval is how often due timers are looked for. A timer fires at
// most this late.
const DefaultInterval = 10 * time.Second

// Source is what the ticker calls on every beat. app.Schedules satisfies it.
type Source interface {
	Tick(ctx context.Context) (int, error)
}

// Ticker runs a Source in the background.
type Ticker struct {
	src  Source
	log  *slog.Logger
	beat <-chan time.Time
	stop func()

	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
}

// New returns a ticker that beats every interval. A zero interval means
// DefaultInterval.
func New(src Source, log *slog.Logger, interval time.Duration) *Ticker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	return &Ticker{src: src, log: log, beat: t.C, stop: t.Stop}
}

// NewWithBeat returns a ticker driven by beat instead of a timer, so a test
// decides when time passes.
func NewWithBeat(src Source, log *slog.Logger, beat <-chan time.Time) *Ticker {
	return &Ticker{src: src, log: log, beat: beat, stop: func() {}}
}

// Start makes the first check now and starts the loop. It returns when the
// first check is done. A second Start does nothing.
func (t *Ticker) Start(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t.cancel, t.done = cancel, make(chan struct{})
	t.tick(loopCtx)
	go t.loop(loopCtx, t.done)
}

// Stop ends the loop and waits for the check in progress, or until ctx ends.
func (t *Ticker) Stop(ctx context.Context) error {
	t.mu.Lock()
	cancel, done := t.cancel, t.done
	t.cancel = nil
	t.mu.Unlock()
	t.stop()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Ticker) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.beat:
			t.tick(ctx)
		}
	}
}

func (t *Ticker) tick(ctx context.Context) {
	fired, err := t.src.Tick(ctx)
	if err != nil {
		t.log.Warn("schedule tick", "fired", fired, "err", err)
	}
}
