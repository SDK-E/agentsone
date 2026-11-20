// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// startCounter counts the starts that reach the machine's launcher, below
// the supervisor's network wall check.
type startCounter struct {
	app.Launcher
	mu sync.Mutex
	n  map[string]int
}

func (c *startCounter) Start(ctx context.Context, agent string) error {
	c.mu.Lock()
	c.n[agent]++
	c.mu.Unlock()
	return c.Launcher.Start(ctx, agent)
}

func (c *startCounter) starts(agent string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[agent]
}

var (
	errExitStatus3 = errors.New("exit status 3")
	errKilled      = errors.New("signal: killed")
)

// restartWorld is a world whose launcher starts are counted, with the
// always-on founder's runner up and idle. The tests play no heartbeats, so
// the heartbeat timeout is out of their way.
func restartWorld(t *testing.T) (*world, *startCounter) {
	t.Helper()
	var c *startCounter
	w := newWorld(t, func(d *app.Deps) {
		c = &startCounter{Launcher: d.Launcher, n: map[string]int{}}
		d.Launcher = c
		d.Config.HeartbeatTimeout = 1000 * time.Hour
	})
	if err := w.sup.Run(t.Context(), "founder"); err != nil {
		t.Fatal(err)
	}
	w.connect(t, "founder")
	return w, c
}

// exit plays the founder's runner process ending without being stopped:
// the launcher lets go of it and reports why.
func (w *world) exit(t *testing.T, err error) {
	t.Helper()
	_ = w.launcher.Stop(t.Context(), "founder")
	w.sup.RunnerExited(t.Context(), "founder", err)
}

// tickAfter moves the fake clock by d and runs Tick.
func (w *world) tickAfter(t *testing.T, d time.Duration) {
	t.Helper()
	w.fakes.Clock.Advance(d)
	if err := w.sup.Tick(t.Context()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
}

// restartAudits are the audit records of failing and recovering runners.
func (w *world) restartAudits() []string {
	var out []string
	for _, a := range w.actions() {
		if strings.HasPrefix(a, app.ActionRunnerFailing) || strings.HasPrefix(a, app.ActionRunnerRecovered) || strings.HasPrefix(a, app.ActionRunnerError) {
			out = append(out, a)
		}
	}
	return out
}

// lastHeard is the last thing the society heard about the founder.
func (w *world) lastHeard(t *testing.T) string {
	t.Helper()
	heard := slices.DeleteFunc(w.sink.heard(), func(h string) bool { return !strings.Contains(h, " founder") })
	if len(heard) == 0 {
		t.Fatal("the society heard nothing about the founder")
	}
	return heard[len(heard)-1]
}

func TestRunnerExited_AFailedRunnerStartsAgainAfterTwoSeconds(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)

	w.exit(t, errExitStatus3)
	w.tickAfter(t, 2*time.Second-time.Millisecond)
	if got := c.starts("founder"); got != 1 {
		t.Fatalf("starts before the wait is over = %d, want 1", got)
	}
	w.tickAfter(t, time.Millisecond)

	if got := c.starts("founder"); got != 2 {
		t.Fatalf("starts once the wait is over = %d, want 2", got)
	}
	if got := w.sup.SessionState("founder"); got != domain.StateStarting {
		t.Fatalf("session state = %s, want starting", got)
	}
}

func TestRunnerExited_EachFailureWaitsTwiceAsLongUpToFiveMinutes(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)
	s := time.Second
	waits := []time.Duration{2 * s, 4 * s, 8 * s, 16 * s, 32 * s, 64 * s, 128 * s, 256 * s, 5 * time.Minute, 5 * time.Minute}

	for i, wait := range waits {
		w.exit(t, errExitStatus3)
		w.tickAfter(t, wait-time.Millisecond)
		if got := c.starts("founder"); got != i+1 {
			t.Fatalf("failure %d: started before %v", i+1, wait)
		}
		w.tickAfter(t, time.Millisecond)
		if got := c.starts("founder"); got != i+2 {
			t.Fatalf("failure %d: not started after %v", i+1, wait)
		}
	}
}

func TestRunnerExited_ARunnerUpFiveMinutesRecoversAndWaitsTwoSecondsAgain(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)
	w.exit(t, errExitStatus3)
	w.tickAfter(t, 2*time.Second)
	w.exit(t, errExitStatus3) // the second failure waits 4 s
	w.tickAfter(t, 4*time.Second)
	w.connect(t, "founder")

	w.tickAfter(t, domain.RestartStableAfter)
	w.exit(t, errExitStatus3)
	w.tickAfter(t, 2*time.Second)

	if got := c.starts("founder"); got != 4 {
		t.Fatalf("starts = %d, want 4: after the recovery the wait is 2 s again", got)
	}
}

func TestRunnerExited_OneFailingSpellWritesTwoAuditRecords(t *testing.T) {
	t.Parallel()
	w, _ := restartWorld(t)
	w.exit(t, errExitStatus3)
	w.tickAfter(t, 2*time.Second)
	w.connect(t, "founder") // up, but not for long
	w.exit(t, errKilled)
	w.tickAfter(t, 4*time.Second)
	w.exit(t, errExitStatus3)
	w.tickAfter(t, 8*time.Second)
	w.connect(t, "founder")
	w.tickAfter(t, domain.RestartStableAfter-time.Millisecond)
	if diff := cmp.Diff([]string{app.ActionRunnerFailing + " agent:founder"}, w.restartAudits()); diff != "" {
		t.Fatalf("audit records before the recovery (-want +got):\n%s", diff)
	}

	w.tickAfter(t, time.Millisecond)
	w.tickAfter(t, time.Hour)

	want := []string{app.ActionRunnerFailing + " agent:founder", app.ActionRunnerRecovered + " agent:founder"}
	if diff := cmp.Diff(want, w.restartAudits()); diff != "" {
		t.Fatalf("audit records of the spell (-want +got):\n%s", diff)
	}
}

func TestRunnerExited_TheAgentShowsTheRunnerFailingWithTheLastError(t *testing.T) {
	t.Parallel()
	w, _ := restartWorld(t)
	w.exit(t, errExitStatus3)
	if got, want := w.lastHeard(t), "failed founder: "+app.ReasonRunnerFailing+" Last error: exit status 3."; got != want {
		t.Fatalf("society heard %q, want %q", got, want)
	}
	w.tickAfter(t, 2*time.Second)

	w.exit(t, errors.New("signal: killed\nwith a second line"))

	if got, want := w.lastHeard(t), "failed founder: "+app.ReasonRunnerFailing+" Last error: signal: killed with a second line."; got != want {
		t.Fatalf("society heard %q, want %q", got, want)
	}
}

func TestRunnerExited_LogsOneLinePerBackoffStep(t *testing.T) {
	t.Parallel()
	w, _ := restartWorld(t)
	before := len(w.logs.all())
	for _, wait := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		w.exit(t, errExitStatus3)
		for range 3 { // ticks while the runner waits add no line
			w.tickAfter(t, wait/4)
		}
		w.tickAfter(t, wait/4)
	}
	w.connect(t, "founder")
	w.tickAfter(t, domain.RestartStableAfter)
	w.tickAfter(t, time.Hour)

	got := slices.DeleteFunc(w.logs.all()[before:], func(l string) bool { return strings.HasSuffix(l, " runner connected") })
	failed := "WARN " + app.LogRunnerFailing
	want := []string{failed, failed, failed, "INFO " + app.LogRunnerRecovered}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("log lines (-want +got):\n%s", diff)
	}
	attrs, _ := w.logs.attrsOf(failed)
	if attrs["agent"] != "founder" || attrs["err"] != "exit status 3" || attrs["wait"] != "2s" {
		t.Errorf("failure line attributes = %v, want agent founder, err exit status 3, wait 2s", attrs)
	}
}

func TestRunnerExited_ARunnerThatExitsCleanlyStaysStopped(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)

	w.exit(t, nil)
	w.tickAfter(t, time.Hour)

	if got := c.starts("founder"); got != 1 {
		t.Fatalf("starts = %d, want 1: a clean exit is not restarted", got)
	}
	if got := w.sup.SessionState("founder"); got != domain.StateStopped {
		t.Fatalf("session state = %s, want stopped", got)
	}
	if got := w.restartAudits(); len(got) != 0 {
		t.Fatalf("audit records = %v, want none", got)
	}
}

// The wall broke after the last check and the runner failed: the restart
// checks the wall afresh, so the runner stays down and the agent shows the
// wall's reason. Once the wall holds the waiting runner starts.
func TestRunnerExited_NoRestartThroughAWallThatBrokeSinceTheLastCheck(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)
	w.wall.set(brokenWall)
	w.exit(t, errExitStatus3)

	w.tickAfter(t, 2*time.Second)
	w.tickAfter(t, time.Minute)

	if got := c.starts("founder"); got != 1 {
		t.Fatalf("starts = %d, want 1: no restart through a broken wall", got)
	}
	if got, want := w.lastHeard(t), "failed founder: "+brokenWall.StateReason(); got != want {
		t.Fatalf("society heard %q, want %q", got, want)
	}
	if diff := cmp.Diff([]string{"net.wall.broken network:wall"}, w.wallAudits()); diff != "" {
		t.Fatalf("wall audit records (-want +got):\n%s", diff)
	}

	w.wall.set(holdingWall)
	w.tickAfter(t, time.Second)

	if got := c.starts("founder"); got != 2 {
		t.Fatalf("starts once the wall holds = %d, want 2", got)
	}
}

// A runner that fails while the supervisor already knows the wall is
// broken waits: a later wall check that finds it holding starts it.
func TestRunnerExited_ARestartWaitsForABrokenWallToHold(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)
	w.exit(t, errExitStatus3)
	w.wall.set(brokenWall)
	w.sup.CheckWall(t.Context())

	w.tickAfter(t, time.Hour)
	if got := c.starts("founder"); got != 1 {
		t.Fatalf("starts behind the broken wall = %d, want 1", got)
	}
	w.wall.set(holdingWall)
	w.tickAfter(t, time.Second)

	if got := c.starts("founder"); got != 2 {
		t.Fatalf("starts once the wall holds = %d, want 2", got)
	}
}

// A wake while the runner waits out its backoff does not start it early:
// the restart delivers it.
func TestRunnerExited_AWakeDuringTheWaitGoesWithTheRestart(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)
	w.exit(t, errExitStatus3)

	if err := w.sup.Wake(t.Context(), "founder", core.WakeMessage, json.RawMessage(`{"messageId":"msg_1"}`)); err != nil {
		t.Fatalf("Wake() error = %v", err)
	}
	if got := c.starts("founder"); got != 1 {
		t.Fatalf("starts after the wake = %d, want 1: the wake waits for the restart", got)
	}
	w.tickAfter(t, 2*time.Second)
	_, conn, _ := w.connect(t, "founder")

	if got := conn.next(t); got.method != protocol.MethodWake {
		t.Fatalf("first request after the restart = %s, want %s", got.method, protocol.MethodWake)
	}
}

func TestRunnerExited_NoRestartAfterTheAgentWasPutToSleep(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)
	w.exit(t, errExitStatus3)

	if err := w.sup.Sleep(t.Context(), "founder", false); err != nil {
		t.Fatal(err)
	}
	w.tickAfter(t, time.Hour)

	if got := c.starts("founder"); got != 1 {
		t.Fatalf("starts = %d, want 1: a sleeping agent is not restarted", got)
	}
}

func TestRunnerExited_NoRestartOfAFrozenAgent(t *testing.T) {
	t.Parallel()
	w, c := restartWorld(t)
	w.exit(t, errExitStatus3)

	if err := w.sup.Freeze(t.Context(), core.FreezeScope{Kind: core.FreezeAgent, Target: "founder"}); err != nil {
		t.Fatal(err)
	}
	w.tickAfter(t, time.Hour)

	if got := c.starts("founder"); got != 1 {
		t.Fatalf("starts = %d, want 1: a frozen agent is not restarted", got)
	}
}
