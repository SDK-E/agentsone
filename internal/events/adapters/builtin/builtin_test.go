// SPDX-License-Identifier: MIT

package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
	"github.com/SDK-E/agentsone/internal/emitkit/emitkittest"
	"github.com/SDK-E/agentsone/internal/events/adapters/builtin"
	evdomain "github.com/SDK-E/agentsone/internal/events/domain"
)

type bus struct {
	mu     sync.Mutex
	events []core.Event
	err    error
}

func (b *bus) Publish(_ context.Context, e core.Event) (core.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return core.Event{}, b.err
	}
	b.events = append(b.events, e)
	return e, nil
}

func (b *bus) all() []core.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]core.Event(nil), b.events...)
}

func tick(t *testing.T) domain.Draft {
	t.Helper()
	d, err := domain.NewDraft("agentsone.clock.tick", "sub", map[string]string{"minute": "2026-11-20T00:01:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPublisher_PublishesInProcessAsTheEmitter(t *testing.T) {
	t.Parallel()
	b := &bus{}

	if err := builtin.NewPublisher(b, "clock").Publish(context.Background(), tick(t)); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	got := b.all()
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	e := got[0]
	if e.Source != "/emitters/clock" || e.Type != "agentsone.clock.tick" || e.Subject != "sub" {
		t.Fatalf("event = %+v", e)
	}
	var data map[string]string
	if err := json.Unmarshal(e.Data, &data); err != nil || data["minute"] != "2026-11-20T00:01:00Z" {
		t.Fatalf("data = %s, %v", e.Data, err)
	}
	if e.ChartScope != "" || e.Actor != "" || e.ID != "" {
		t.Fatalf("event = %+v, the bus fills id, actor and chart scope", e)
	}
}

func TestPublisher_ADisabledEmitterIsNotAnError(t *testing.T) {
	t.Parallel()
	b := &bus{err: evdomain.ErrEmitterDisabled}
	if err := builtin.NewPublisher(b, "clock").Publish(context.Background(), tick(t)); err != nil {
		t.Fatalf("Publish error = %v, want nil: turning an emitter off silences it", err)
	}
}

func TestPublisher_OtherFailuresAreReturned(t *testing.T) {
	t.Parallel()
	boom := errors.New("disk full")
	err := builtin.NewPublisher(&bus{err: boom}, "clock").Publish(context.Background(), tick(t))
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the bus error", err)
	}
}

// source is a source whose Run the test controls.
type source struct {
	started chan struct{}
	err     error
}

func (s *source) Run(ctx context.Context, _ app.Deps) error {
	close(s.started)
	if s.err != nil {
		return s.err
	}
	<-ctx.Done()
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newRunner(sources ...builtin.Named) *builtin.Runner {
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	return builtin.NewRunner(quiet(), clk, &emitkittest.Sleeper{Clock: clk}, &bus{}, sources)
}

func TestRunner_StartRunsEverySourceAndStopEndsThem(t *testing.T) {
	t.Parallel()
	a := &source{started: make(chan struct{})}
	b := &source{started: make(chan struct{})}
	r := newRunner(builtin.Named{Name: "a", Source: a}, builtin.Named{Name: "b", Source: b})

	r.Start(context.Background())
	<-a.started
	<-b.started
	done := make(chan struct{})
	go func() { r.Stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return: a source ignored the cancel")
	}
}

func TestRunner_StartTwiceRunsEachSourceOnce(t *testing.T) {
	t.Parallel()
	starts := 0
	var mu sync.Mutex
	counting := countingSource(func() { mu.Lock(); starts++; mu.Unlock() })
	r := newRunner(builtin.Named{Name: "a", Source: counting})

	r.Start(context.Background())
	r.Start(context.Background())
	r.Stop()

	mu.Lock()
	defer mu.Unlock()
	if starts != 1 {
		t.Fatalf("source started %d times, want 1", starts)
	}
}

type countingSource func()

func (c countingSource) Run(ctx context.Context, _ app.Deps) error {
	c()
	<-ctx.Done()
	return nil
}

func TestRunner_ASourceThatFailsDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()
	bad := &source{started: make(chan struct{}), err: errors.New("cannot read /proc")}
	good := &source{started: make(chan struct{})}
	r := newRunner(builtin.Named{Name: "bad", Source: bad}, builtin.Named{Name: "good", Source: good})

	r.Start(context.Background())
	<-bad.started
	<-good.started
	r.Stop()
}

func TestRunner_StopWithoutStartIsSafe(t *testing.T) {
	t.Parallel()
	newRunner().Stop()
}

func TestRunner_CanStartAgainAfterStop(t *testing.T) {
	t.Parallel()
	starts := 0
	var mu sync.Mutex
	counting := countingSource(func() { mu.Lock(); starts++; mu.Unlock() })
	r := newRunner(builtin.Named{Name: "a", Source: counting})

	r.Start(context.Background())
	r.Stop()
	r.Start(context.Background())
	r.Stop()

	mu.Lock()
	defer mu.Unlock()
	if starts != 2 {
		t.Fatalf("source started %d times, want 2", starts)
	}
}
