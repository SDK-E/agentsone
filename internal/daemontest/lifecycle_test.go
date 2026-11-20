// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// Integration tests: what an agent's record says about its life. Each reads
// the record the way a human does, through the API, after the real
// supervisor, socket and runner have done the work.

// pollEvery is how often waitFor looks again. Only Timeout ends the wait.
const pollEvery = 10 * time.Millisecond

// waitFor reads what until ok accepts it, and fails with the last value
// seen when daemontest.Timeout passes first.
func waitFor[T any](t *testing.T, what string, read func() T, ok func(T) bool) T {
	t.Helper()
	deadline := time.NewTimer(daemontest.Timeout)
	defer deadline.Stop()
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		v := read()
		if ok(v) {
			return v
		}
		select {
		case <-deadline.C:
			t.Fatalf("no %s within %s; last seen %+v", what, daemontest.Timeout, v)
		case <-tick.C:
		}
	}
}

func liveEntry(h *daemontest.Client, agent string) api.LiveEntry {
	var list api.LiveList
	h.Do(http.MethodGet, "/router/live", nil, &list)
	for _, e := range list.Items {
		if e.Agent == agent {
			return e
		}
	}
	return api.LiveEntry{}
}

// SDK-2129: the founder is always on. Once its runner is up and idle the
// record says awake, in the agent's record and in the live list, and never
// sleeping.
func TestAgentRecord_AnAlwaysOnAgentThatIsUpAndIdleIsAwake(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")

	got := waitFor(t, "the founder awake", func() api.Agent { return alice.Agent("founder") },
		func(a api.Agent) bool { return a.State == api.AgentStateAwake })

	// The founder passes through waking on its way up, so only awake ends the wait.
	if got.State != api.AgentStateAwake {
		t.Fatalf("founder state = %s, want %s", got.State, api.AgentStateAwake)
	}
	if e := liveEntry(alice, "founder"); e.State != api.AgentStateAwake {
		t.Fatalf("founder state in the live list = %s, want %s", e.State, api.AgentStateAwake)
	}
}

// SDK-2129: a chat agent has no runner until it is woken, so it sleeps; it
// is working while its turn runs and shows awake or sleeping again after.
func TestAgentRecord_AChatAgentSleepsUntilItsFirstWake(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireEcho("echo")

	if got := alice.Agent("echo").State; got != api.AgentStateSleeping {
		t.Fatalf("echo state before any wake = %s, want %s", got, api.AgentStateSleeping)
	}

	alice.Chat("echo", "hello")

	waitFor(t, "echo awake after its turn", func() api.Agent { return alice.Agent("echo") },
		func(a api.Agent) bool { return a.State == api.AgentStateAwake })
}

// SDK-2129: last_turn_at is set when a turn ends, and it is still there
// after the daemon restarts.
func TestAgentRecord_LastTurnAtIsSetWhenATurnEndsAndSurvivesARestart(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireEcho("echo")
	if got := alice.Agent("echo").LastTurnAt; got != nil {
		t.Fatalf("last_turn_at before any turn = %v, want null", got)
	}
	d.Clock.Advance(time.Hour)
	turnAt := d.Clock.Now()

	alice.Chat("echo", "hello")

	got := waitFor(t, "last_turn_at set", func() api.Agent { return alice.Agent("echo") },
		func(a api.Agent) bool { return a.LastTurnAt != nil })
	if !time.Time(*got.LastTurnAt).Equal(turnAt) {
		t.Fatalf("last_turn_at = %v, want the time the turn ended, %v", time.Time(*got.LastTurnAt), turnAt)
	}

	d = d.Restart()
	alice = d.LogIn("alice")

	after := alice.Agent("echo")
	if after.LastTurnAt == nil || !time.Time(*after.LastTurnAt).Equal(turnAt) {
		t.Fatalf("last_turn_at after the restart = %v, want %v", after.LastTurnAt, turnAt)
	}
}

// SDK-2131: a newly hired agent's one line is its role until its first
// turn, in the agent's record and in the live list, and the agent's own
// first line replaces it.
func TestAgentRecord_ANewAgentsOneLineIsItsRoleUntilItsFirstTurn(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	hired := d.AsAgent("founder").HireEcho("scribe")
	role := hired.Role

	record := alice.Agent("scribe")
	live := liveEntry(alice, "scribe")

	if record.OneLine == nil || record.OneLine.Text != role {
		t.Fatalf("one_line in the agent record = %+v, want the role %q", record.OneLine, role)
	}
	if live.OneLine == nil || live.OneLine.Text != role {
		t.Fatalf("one_line in the live list = %+v, want the role %q", live.OneLine, role)
	}

	alice.Chat("scribe", "hello")

	own := waitFor(t, "the agent's own line", func() api.Agent { return alice.Agent("scribe") },
		func(a api.Agent) bool { return a.OneLine != nil && a.OneLine.Text != role })
	if !strings.HasPrefix(own.OneLine.Text, "Goal: echo what I am told.") {
		t.Fatalf("one_line after the first turn = %q, want the agent's own line", own.OneLine.Text)
	}
	if e := liveEntry(alice, "scribe"); e.OneLine == nil || e.OneLine.Text != own.OneLine.Text {
		t.Fatalf("one_line in the live list = %+v, want %q like the record", e.OneLine, own.OneLine.Text)
	}
}
