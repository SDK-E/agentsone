// SPDX-License-Identifier: MIT

package app_test

import (
	"encoding/json"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

const refusedLine = "ERROR runner refused a wake"

var invalidParams = &runnerRefusal{code: -32602, message: "Invalid params"}

const invalidParamsReason = "The runner refused a chat wake: error -32602, Invalid params."

// readyFounder starts founder and finishes its handshake, so the next wake
// goes straight to the runner.
func readyFounder(t *testing.T, w *world) (*app.Handler, *fakeConn) {
	t.Helper()
	if err := w.sup.Wake(t.Context(), "founder", core.WakeTimer, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	h, conn, _ := w.connect(t, "founder")
	conn.next(t)
	return h, conn
}

func (w *world) refusals() []core.AuditEntry {
	var out []core.AuditEntry
	for _, e := range w.fakes.Audit.Entries() {
		if e.Action == app.ActionWakeRefused {
			out = append(out, e)
		}
	}
	return out
}

func countOf(lines []string, line string) int {
	n := 0
	for _, l := range lines {
		if l == line {
			n++
		}
	}
	return n
}

// waitFor yields until ok, or fails after a deadline: the code under test
// finishes on its own goroutine and has no fake clock to move.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		runtime.Gosched()
	}
}

func wakeChat(t *testing.T, w *world, agent string) error {
	t.Helper()
	return w.sup.Wake(t.Context(), agent, core.WakeChat, json.RawMessage(`{}`))
}

func TestWake_RefusedByTheRunnerIsAuditedLoggedAndShownOnTheAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, conn := readyFounder(t, w)
	conn.refuseWakes(invalidParams)

	if err := wakeChat(t, w, "founder"); err == nil {
		t.Fatal("Wake() = nil, want the runner's refusal")
	}

	got := w.refusals()
	if len(got) != 1 || got[0].Target != "agent:founder" || got[0].Actor != "system:supervisor" {
		t.Fatalf("audit = %+v, want one %s on agent:founder", got, app.ActionWakeRefused)
	}
	var detail map[string]any
	if err := json.Unmarshal(got[0].Detail, &detail); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"reason": "chat", "code": float64(-32602), "message": "Invalid params"}
	if diff := cmp.Diff(want, detail); diff != "" {
		t.Errorf("audit detail (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"running founder", "failed founder: " + invalidParamsReason}, w.sink.heard()); diff != "" {
		t.Errorf("society heard (-want +got):\n%s", diff)
	}
	if n := countOf(w.logs.all(), refusedLine); n != 1 {
		t.Errorf("%d error lines %q, want 1; log %v", n, refusedLine, w.logs.all())
	}
	attrs, _ := w.logs.attrsOf(refusedLine)
	if attrs["agent"] != "founder" || attrs["reason"] != "chat" || attrs["code"] != "-32602" || attrs["message"] != "Invalid params" {
		t.Errorf("log attrs = %v, want agent, reason, code and message", attrs)
	}
}

func TestWake_RepeatedRefusalOfTheSameKindIsRecordedOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, conn := readyFounder(t, w)
	conn.refuseWakes(invalidParams)

	for range 5 {
		_ = wakeChat(t, w, "founder")
	}
	if n := len(w.refusals()); n != 1 {
		t.Errorf("%d audit records for five identical refusals, want 1", n)
	}
	if n := countOf(w.logs.all(), refusedLine); n != 1 {
		t.Errorf("%d error lines for five identical refusals, want 1", n)
	}
	if n := len(w.sink.heard()); n != 2 { // the start, then the refusal once
		t.Errorf("society heard %d times, want 2: %v", n, w.sink.heard())
	}
}

func TestWake_ADifferentRefusalIsAChange(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, conn := readyFounder(t, w)
	conn.refuseWakes(invalidParams)
	_ = wakeChat(t, w, "founder")

	// Another code, another message, another wake reason: each is a change.
	conn.refuseWakes(&runnerRefusal{code: -32603, message: "Internal error"})
	_ = wakeChat(t, w, "founder")
	_ = w.sup.Wake(t.Context(), "founder", core.WakeTask, json.RawMessage(`{}`))
	conn.refuseWakes(&runnerRefusal{code: -32603, message: "Internal error, again"})
	_ = w.sup.Wake(t.Context(), "founder", core.WakeTask, json.RawMessage(`{}`))

	if n := len(w.refusals()); n != 4 {
		t.Errorf("%d audit records, want 4", n)
	}
	if n := countOf(w.logs.all(), refusedLine); n != 4 {
		t.Errorf("%d error lines, want 4", n)
	}
}

func TestWake_AcceptedWakeClearsTheRefusal(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, conn := readyFounder(t, w)
	conn.refuseWakes(invalidParams)
	_ = wakeChat(t, w, "founder")

	conn.refuseWakes(nil)
	if err := wakeChat(t, w, "founder"); err != nil {
		t.Fatalf("Wake() = %v, want accepted", err)
	}
	conn.next(t)
	want := []string{"running founder", "failed founder: " + invalidParamsReason, "running founder"}
	if diff := cmp.Diff(want, w.sink.heard()); diff != "" {
		t.Errorf("society heard (-want +got):\n%s", diff)
	}

	// The same refusal after it cleared is a change again.
	conn.refuseWakes(invalidParams)
	_ = wakeChat(t, w, "founder")
	if n := len(w.refusals()); n != 2 {
		t.Errorf("%d audit records, want 2: one per refusal after a clear", n)
	}
}

func TestWake_AcceptedWakeWithoutARefusalSaysNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, conn := readyFounder(t, w)
	for range 3 {
		if err := wakeChat(t, w, "founder"); err != nil {
			t.Fatal(err)
		}
		conn.next(t)
	}
	if got := w.sink.heard(); slices.ContainsFunc(got, func(s string) bool { return s != "running founder" }) {
		t.Errorf("society heard %v, want nothing but the start", got)
	}
}

func TestWake_RefusalIsPerAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, founder := readyFounder(t, w)
	_ = w.sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`))
	_, mira, _ := w.connect(t, "mira")
	mira.next(t)

	founder.refuseWakes(invalidParams)
	mira.refuseWakes(invalidParams)
	_ = wakeChat(t, w, "founder")
	_ = wakeChat(t, w, "mira")
	if n := len(w.refusals()); n != 2 {
		t.Errorf("%d audit records, want one per agent", n)
	}
}

// A held wake is sent when the runner's first status arrives; a refusal there
// is just as loud, and does not stop the wakes held behind it.
func TestStatus_HeldWakeRefusedIsAuditedAndTheNextIsStillSent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_ = w.sup.Wake(t.Context(), "founder", core.WakeChat, json.RawMessage(`{}`))
	_ = w.sup.Wake(t.Context(), "founder", core.WakeChat, json.RawMessage(`{}`))
	_ = w.sup.Wake(t.Context(), "founder", core.WakeTask, json.RawMessage(`{}`))

	h := w.sup.Connect("founder")
	conn := newFakeConn()
	conn.refuseWakes(invalidParams)
	h.Attach(conn)
	if _, err := h.Hello(t.Context(), "1.0", helloParams); err != nil {
		t.Fatal(err)
	}
	if err := h.Status(t.Context(), protocol.StatusParams{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	conn.markReady()

	// The flush runs on its own goroutine: wait for the audit record, then
	// for the third wake to be tried, which the audit of a task refusal shows.
	waitFor(t, func() bool { return len(w.refusals()) == 2 })
	if n := countOf(w.logs.all(), refusedLine); n != 2 {
		t.Errorf("%d error lines, want 2 (chat once, task once); log %v", n, w.logs.all())
	}
}
