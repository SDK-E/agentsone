// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// pauseTurn plays a runner whose turn the budget paused (rule R1): the
// runner reports idle with the paused turn.
func pauseTurn(t *testing.T, h *app.Handler, turn common.TurnID) {
	t.Helper()
	if err := h.ProfileUpdate(t.Context(), protocol.ProfileUpdateParams{TurnID: turn, Line: "Goal: g. Now: n."}); err != nil {
		t.Fatal(err)
	}
	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle", PausedTurnID: &turn}); err != nil {
		t.Fatal(err)
	}
}

// eventually yields until ok, or fails after a deadline: the wake that
// resumes an agent goes out on its own goroutine and has no fake clock to move.
func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		runtime.Gosched()
	}
}

func (w *world) setBudget(agent string, remaining int64) {
	w.fakes.Budget.Set(core.Budget{Agent: agent, AllocatedMicros: 1000, RemainingMicros: remaining})
}

func (w *world) restoredEvents() []eventdata.AgentsBudgetRestored {
	var out []eventdata.AgentsBudgetRestored
	for _, e := range w.fakes.Events.Published() {
		if e.Type != eventdata.TypeAgentsBudgetRestored {
			continue
		}
		var d eventdata.AgentsBudgetRestored
		if err := json.Unmarshal(e.Data, &d); err != nil {
			panic(err)
		}
		out = append(out, d)
	}
	return out
}

func (w *world) resumes() []core.AuditEntry {
	var out []core.AuditEntry
	for _, e := range w.fakes.Audit.Entries() {
		if e.Action == app.ActionBudgetResume {
			out = append(out, e)
		}
	}
	return out
}

func TestTick_RestoredBudgetResumesThePausedAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, turn := startTurn(t, w, "founder")
	pauseTurn(t, h, turn)

	w.setBudget("founder", 0)
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(w.restoredEvents()) + len(w.resumes()); n != 0 {
		t.Fatalf("an agent with no budget left was resumed: %d records", n)
	}

	w.setBudget("founder", 500)
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}

	got := w.restoredEvents()
	if len(got) != 1 || got[0].Agent != "founder" || got[0].PausedTurnID == nil || *got[0].PausedTurnID != string(turn) {
		t.Fatalf("events = %+v, want one budget.restored for founder naming turn %s", got, turn)
	}
	if n := len(w.resumes()); n != 1 {
		t.Fatalf("%d audit records, want one per resumed agent", n)
	}
	if e := w.resumes()[0]; e.Target != "agent:founder" || e.Actor != "system:supervisor" {
		t.Errorf("audit = %+v, want agent:founder by the supervisor", e)
	}

	sent := conn.next(t)
	var p struct {
		Reason  string          `json:"reason"`
		TurnID  common.TurnID   `json:"turnId"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(sent.params, &p); err != nil {
		t.Fatal(err)
	}
	if sent.method != "wake" || p.Reason != "event" || p.TurnID != turn {
		t.Fatalf("request = %s %s, want an event wake that resumes turn %s", sent.method, sent.params, turn)
	}
	if err := contracttest.ValidateWakePayload(p.Reason, p.Payload); err != nil {
		t.Errorf("wake payload does not match the runner contract: %v", err)
	}
	var ev struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(p.Payload, &ev)
	if ev.Type != app.BudgetRestored {
		t.Errorf("wake payload type = %q, want %q", ev.Type, app.BudgetRestored)
	}
}

func TestTick_ARestoredBudgetResumesOncePerPause(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, turn := startTurn(t, w, "founder")
	pauseTurn(t, h, turn)
	w.setBudget("founder", 500)
	for range 3 {
		if err := w.sup.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	conn.next(t)
	if n := len(w.restoredEvents()); n != 1 {
		t.Errorf("%d events for one restore, want 1", n)
	}
	if n := len(w.resumes()); n != 1 {
		t.Errorf("%d audit records for one restore, want 1", n)
	}
}

// The runner resumes the turn, spends the budget and pauses again: the next
// restore resumes it again.
func TestTick_APausedAgainIsResumedAgainOnTheNextRestore(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, turn := startTurn(t, w, "founder")
	pauseTurn(t, h, turn)
	w.setBudget("founder", 500)
	_ = w.sup.Tick(t.Context())
	conn.next(t)

	if err := h.Status(t.Context(), protocol.StatusParams{State: "working", TurnID: &turn}); err != nil {
		t.Fatal(err)
	}
	pauseTurn(t, h, turn)
	w.setBudget("founder", 0)
	_ = w.sup.Tick(t.Context())
	w.setBudget("founder", 300)
	_ = w.sup.Tick(t.Context())

	if n := len(w.resumes()); n != 2 {
		t.Errorf("%d resumes after a second pause and restore, want 2", n)
	}
}

func TestTick_AgentsThatAreNotPausedAreNotWoken(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, conn, _ := startTurn(t, w, "founder") // working, not paused
	w.setBudget("founder", 500)
	w.setBudget("mira", 500) // never started
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(w.restoredEvents()) + len(w.resumes()); n != 0 {
		t.Fatalf("%d records for agents that are not paused, want none", n)
	}
	select {
	case c := <-conn.calls:
		t.Fatalf("runner got %s %s, want nothing", c.method, c.params)
	default:
	}
	if w.launcher.isRunning("mira") {
		t.Error("an agent that was not paused was started")
	}
}

// A paused turn survives sleep: the budget coming back starts the runner and
// the wake carries the paused turn.
func TestTick_RestoredBudgetResumesASleepingAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, turn := startTurn(t, w, "founder")
	pauseTurn(t, h, turn)
	done := make(chan error, 1)
	go func() { done <- w.sup.Sleep(t.Context(), "founder", false) }()
	conn.next(t) // shutdown
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if w.launcher.isRunning("founder") {
		t.Fatal("founder still runs after sleep")
	}

	w.setBudget("founder", 500)
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return w.launcher.isRunning("founder") }) // the wake goes out on its own goroutine
	if n := len(w.resumes()); n != 1 {
		t.Errorf("%d resumes, want 1", n)
	}
}

// A wake the runner did not take must not strand the paused turn: the resume
// is tried again, but after a wait, not on every tick, and once it works it is
// not repeated.
func TestTick_AResumeWakeThatFailedIsRetriedAfterAWait(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, conn, turn := startTurn(t, w, "founder")
	pauseTurn(t, h, turn)
	done := make(chan error, 1)
	go func() { done <- w.sup.Sleep(t.Context(), "founder", false) }()
	conn.next(t) // shutdown
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	const failed = "WARN supervisor: the agent did not take the wake that resumes its paused turn; trying again later"
	w.launcher.mu.Lock()
	w.launcher.startErr = errors.New("cannot start the runner")
	w.launcher.mu.Unlock()
	w.setBudget("founder", 500)

	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { _, ok := w.logs.attrsOf(failed); return ok })
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(w.restoredEvents()); n != 1 {
		t.Fatalf("%d resume attempts on the tick right after a failure, want 1", n)
	}

	w.launcher.mu.Lock()
	w.launcher.startErr = nil
	w.launcher.mu.Unlock()
	w.fakes.Clock.Advance(61 * time.Second)
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return w.launcher.isRunning("founder") })
	if n := len(w.restoredEvents()); n != 2 {
		t.Fatalf("%d resume attempts after the wait, want 2", n)
	}

	w.fakes.Clock.Advance(11 * time.Minute)
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(w.restoredEvents()); n != 2 {
		t.Errorf("%d resume attempts after a success, want still 2", n)
	}
}

// refusingBus is an event bus that cannot publish.
type refusingBus struct{ *coretest.Events }

func (refusingBus) Publish(context.Context, core.Event) (core.Event, error) {
	return core.Event{}, errors.New("bus is down")
}

// An agent whose "budget is back" event could not be published was not
// resumed: the next tick tries again.
func TestTick_AResumeThatCouldNotBePublishedIsTriedAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _, turn := startTurn(t, w, "founder")
	pauseTurn(t, h, turn)
	w.setBudget("founder", 500)
	working := w.ports.Events
	w.ports.Events = refusingBus{w.fakes.Events}
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.logs.attrsOf("WARN supervisor: cannot publish that the budget is back"); !ok {
		t.Fatal("no warning that the budget event could not be published")
	}
	w.ports.Events = working
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}

	if n := len(w.restoredEvents()); n != 1 {
		t.Errorf("%d restored events after the bus came back, want 1", n)
	}
}
