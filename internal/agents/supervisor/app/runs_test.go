// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// runWatch records what the society would hear about a runner, as
// "<agent> <run state>" and "<agent> turn ended <time>".
type runWatch struct {
	mu     sync.Mutex
	events []string
}

func (r *runWatch) RunnerState(_ context.Context, agent string, st app.RunState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, agent+" "+string(st))
	return nil
}

func (r *runWatch) TurnEnded(_ context.Context, agent string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, agent+" turn ended "+at.UTC().Format(time.RFC3339))
	return nil
}

func (r *runWatch) heard() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func newWatchedWorld(t *testing.T) (*world, *runWatch) {
	t.Helper()
	rw := &runWatch{}
	return newWorld(t, func(d *app.Deps) { d.Runs = rw }), rw
}

func TestRuns_ATurnShowsWorkingThenIdleAndEndsWithItsTime(t *testing.T) {
	t.Parallel()
	w, rw := newWatchedWorld(t)
	h, _, turn := startTurn(t, w, "mira")
	w.fakes.Clock.Advance(time.Minute)
	end := w.fakes.Clock.Now().UTC().Format(time.RFC3339)

	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle", TurnID: &turn}); err != nil {
		t.Fatal(err)
	}

	got := rw.heard()
	for _, want := range []string{"mira working", "mira idle", "mira turn ended " + end} {
		if !slices.Contains(got, want) {
			t.Errorf("society heard %v, want it to include %q", got, want)
		}
	}
}

func TestRuns_AnIdleRunnerWithoutATurnEndsNoTurn(t *testing.T) {
	t.Parallel()
	w, rw := newWatchedWorld(t)
	if err := w.sup.Run(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	h, _, _ := w.connect(t, "founder")
	h.Heard()

	got := rw.heard()
	if !slices.Contains(got, "founder idle") {
		t.Fatalf("society heard %v, want founder idle", got)
	}
	for _, e := range got {
		if strings.HasPrefix(e, "founder turn ended") {
			t.Fatalf("society heard %q for an agent that ran no turn", e)
		}
	}
}

func TestRuns_AStartingRunnerShowsStartingAndASleepingOneStopped(t *testing.T) {
	t.Parallel()
	w, rw := newWatchedWorld(t)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	if got := rw.heard(); !slices.Contains(got, "mira starting") {
		t.Fatalf("society heard %v, want mira starting", got)
	}
	_, conn, _ := w.connect(t, "mira")
	conn.next(t) // the wake

	if err := w.sup.Sleep(t.Context(), "mira", false); err != nil {
		t.Fatal(err)
	}
	got := rw.heard()
	if last := got[len(got)-1]; last != "mira stopped" {
		t.Fatalf("society heard %v, want the last to be mira stopped", got)
	}
}

func TestRuns_AClosedConnectionShowsTheRunnerStopped(t *testing.T) {
	t.Parallel()
	w, rw := newWatchedWorld(t)
	h, _, _ := startTurn(t, w, "mira")

	h.Closed(nil)

	got := rw.heard()
	if diff := cmp.Diff("mira stopped", got[len(got)-1]); diff != "" {
		t.Fatalf("society heard %v (-want +got):\n%s", got, diff)
	}
}
