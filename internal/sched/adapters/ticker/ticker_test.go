// SPDX-License-Identifier: MIT

package ticker_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/sched/adapters/ticker"
)

// source counts ticks and tells the test about each one, so no test sleeps.
type source struct {
	n    atomic.Int32
	seen chan int32
	err  error
}

func newSource(err error) *source { return &source{seen: make(chan int32, 16), err: err} }

func (s *source) Tick(context.Context) (int, error) {
	s.seen <- s.n.Add(1)
	return 1, s.err
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStart_ChecksOnceBeforeItReturns(t *testing.T) {
	t.Parallel()
	src := newSource(nil)
	tk := ticker.NewWithBeat(src, quiet(), make(chan time.Time))
	tk.Start(context.Background())
	if got := <-src.seen; got != 1 {
		t.Fatalf("first tick = %d, want 1", got)
	}
	if err := tk.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBeat_ChecksAgain(t *testing.T) {
	t.Parallel()
	src := newSource(nil)
	beat := make(chan time.Time)
	tk := ticker.NewWithBeat(src, quiet(), beat)
	tk.Start(context.Background())
	<-src.seen
	beat <- time.Time{}
	if got := <-src.seen; got != 2 {
		t.Fatalf("tick after a beat = %d, want 2", got)
	}
	if err := tk.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTickError_IsLoggedAndTheLoopGoesOn(t *testing.T) {
	t.Parallel()
	src := newSource(errors.New("wake failed"))
	beat := make(chan time.Time)
	tk := ticker.NewWithBeat(src, quiet(), beat)
	tk.Start(context.Background())
	<-src.seen
	beat <- time.Time{}
	if got := <-src.seen; got != 2 {
		t.Fatalf("tick after an error = %d, want 2", got)
	}
	if err := tk.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStart_TwiceKeepsOneLoop(t *testing.T) {
	t.Parallel()
	src := newSource(nil)
	tk := ticker.NewWithBeat(src, quiet(), make(chan time.Time))
	tk.Start(context.Background())
	tk.Start(context.Background())
	<-src.seen
	if n := src.n.Load(); n != 1 {
		t.Fatalf("ticks after two starts = %d, want 1", n)
	}
	if err := tk.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStop_BeforeStartIsFine(t *testing.T) {
	t.Parallel()
	tk := ticker.New(newSource(nil), quiet(), 0)
	if err := tk.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStop_GivesUpWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	src := &blocking{release: release, entered: make(chan struct{})}
	beat := make(chan time.Time)
	tk := ticker.NewWithBeat(src, quiet(), beat)
	tk.Start(context.Background()) // first tick returns at once
	beat <- time.Time{}            // second tick blocks
	<-src.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tk.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop = %v, want context.Canceled", err)
	}
	close(release)
}

// blocking returns at once the first time and blocks on later calls.
type blocking struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (b *blocking) Tick(context.Context) (int, error) {
	if b.calls.Add(1) > 1 {
		close(b.entered)
		<-b.release
	}
	return 0, nil
}
