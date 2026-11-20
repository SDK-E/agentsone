// SPDX-License-Identifier: MIT

package app_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// brokenWall is the machine after a deploy in Coolify's normal mode.
var brokenWall = domain.JudgeNetWall([]string{"lo", "eth0", "eth1"}, []domain.Route{
	{Iface: "eth0", Desc: "10.77.42.0/27 dev eth0"},
	{Iface: "eth1", Out: true, Desc: "0.0.0.0/0 via 10.0.1.1 dev eth1"},
})

var holdingWall = domain.NetWall{Holds: true, Interfaces: []string{"eth0"}}

const (
	holdsLine  = "INFO network wall holds: no default route, interfaces: eth0"
	brokenLine = "ERROR network wall broken: no agent will wake"
)

func wake(t *testing.T, w *world, agent string) error {
	t.Helper()
	return w.sup.Wake(t.Context(), agent, core.WakeChat, json.RawMessage(`{}`))
}

func (w *world) wallAudits() []string {
	var out []string
	for _, a := range w.actions() {
		if strings.HasPrefix(a, "net.wall.") {
			out = append(out, a)
		}
	}
	return out
}

func TestCheckWall_HoldsLogsOneInfoLineAndWritesNoAudit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if diff := cmp.Diff([]string{holdsLine}, w.logs.all()); diff != "" {
		t.Errorf("log (-want +got):\n%s", diff)
	}
	if got := w.wallAudits(); len(got) != 0 {
		t.Errorf("audit = %v, want none for a wall that holds from the start", got)
	}
	if err := wake(t, w, "mira"); err != nil || !w.launcher.isRunning("mira") {
		t.Fatalf("wake: %v, running %v", err, w.launcher.isRunning("mira"))
	}
}

func TestCheckWall_NoNetworkSaysSoInTheLine(t *testing.T) {
	t.Parallel()
	w := newUncheckedWorld(t)
	w.wall.set(domain.NetWall{Holds: true})
	w.sup.CheckWall(t.Context())
	if diff := cmp.Diff([]string{"INFO network wall holds: no default route, interfaces: none"}, w.logs.all()); diff != "" {
		t.Errorf("log (-want +got):\n%s", diff)
	}
}

func TestWake_RefusedUntilTheWallIsChecked(t *testing.T) {
	t.Parallel()
	w := newUncheckedWorld(t)
	err := wake(t, w, "mira")
	if !errors.Is(err, app.ErrNetworkWall) || !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Wake() error = %v, want ErrNetworkWall and core.ErrUnavailable", err)
	}
	if w.launcher.isRunning("mira") {
		t.Fatal("the runner started before the wall was checked")
	}
	want := "failed mira: The network wall does not hold, so no agent may wake: the network wall has not been checked yet. Restart the machine."
	if diff := cmp.Diff([]string{want}, w.sink.heard()); diff != "" {
		t.Errorf("society heard (-want +got):\n%s", diff)
	}
}

// A broken wall at start: one error line, one audit record, every active
// agent shows why, and no wake starts a runner.
func TestCheckWall_BrokenRefusesEveryWake(t *testing.T) {
	t.Parallel()
	w := newUncheckedWorld(t)
	w.wall.set(brokenWall)
	got, changed := w.sup.CheckWall(t.Context())
	if got.Holds || !changed {
		t.Fatalf("CheckWall() = %+v, %v; want a broken wall, changed", got, changed)
	}
	if diff := cmp.Diff([]string{brokenLine}, w.logs.all()); diff != "" {
		t.Errorf("log (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"net.wall.broken network:wall"}, w.wallAudits()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	reason := brokenWall.StateReason()
	want := []string{"failed founder: " + reason, "failed mira: " + reason, "failed watcher: " + reason}
	if diff := cmp.Diff(want, w.sink.heard()); diff != "" {
		t.Errorf("society heard (-want +got):\n%s", diff)
	}

	for _, agent := range []string{"founder", "mira", "watcher"} {
		err := wake(t, w, agent)
		if !errors.Is(err, app.ErrNetworkWall) || !errors.Is(err, core.ErrUnavailable) {
			t.Fatalf("Wake(%s) error = %v, want ErrNetworkWall and core.ErrUnavailable", agent, err)
		}
		if !strings.Contains(err.Error(), "direct route to the internet") {
			t.Errorf("Wake(%s) error = %q, want the reason in plain words", agent, err)
		}
		if w.launcher.isRunning(agent) {
			t.Fatalf("%s started through a broken wall", agent)
		}
	}
	if n := len(w.wallAudits()); n != 1 {
		t.Errorf("%d wall audit records, want 1: refused wakes do not add records", n)
	}
	if diff := cmp.Diff(want, w.sink.heard()); diff != "" {
		t.Errorf("refused wakes told the society again (-want +got):\n%s", diff)
	}
	// An agent hired after the check hears why at its first wake.
	w.fakes.Agents.Add(core.Agent{Name: "nova", Kind: core.AgentChat, Manager: "founder", Runner: "echo", UID: 200003})
	_ = wake(t, w, "nova")
	if got := w.sink.heard(); got[len(got)-1] != "failed nova: "+reason {
		t.Errorf("society heard %v, want nova's reason last", got)
	}
}

// The wall is checked again every five minutes. A line and an audit record
// come only when the answer changes; once the wall holds again, the
// agents' failure clears and waking works.
func TestTick_RechecksTheWallEveryFiveMinutes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := t.Context()
	tick := func(d time.Duration) {
		t.Helper()
		w.fakes.Clock.Advance(d)
		if err := w.sup.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}

	tick(4*time.Minute + 59*time.Second)
	if n := w.wall.count(); n != 1 {
		t.Fatalf("%d checks before five minutes passed, want 1 (the start)", n)
	}
	tick(time.Second)
	if n := w.wall.count(); n != 2 {
		t.Fatalf("%d checks after five minutes, want 2", n)
	}
	if diff := cmp.Diff([]string{holdsLine}, w.logs.all()); diff != "" {
		t.Errorf("an unchanged wall logged again (-want +got):\n%s", diff)
	}

	w.wall.set(brokenWall)
	tick(5 * time.Minute)
	tick(5 * time.Minute)
	if diff := cmp.Diff([]string{holdsLine, brokenLine}, w.logs.all()); diff != "" {
		t.Errorf("log after the wall broke (-want +got):\n%s", diff)
	}
	if err := wake(t, w, "mira"); !errors.Is(err, app.ErrNetworkWall) {
		t.Fatalf("Wake() through a broken wall = %v, want ErrNetworkWall", err)
	}

	w.wall.set(holdingWall)
	tick(5 * time.Minute)
	if diff := cmp.Diff([]string{holdsLine, brokenLine, holdsLine}, w.logs.all()); diff != "" {
		t.Errorf("log after the wall came back (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"net.wall.broken network:wall", "net.wall.restored network:wall"}, w.wallAudits()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	heard := w.sink.heard()
	for _, agent := range []string{"founder", "mira", "watcher"} {
		if !slices.Contains(heard, "cleared "+agent) {
			t.Errorf("society did not hear that %s may wake again; heard %v", agent, heard)
		}
	}
	if err := wake(t, w, "mira"); err != nil || !w.launcher.isRunning("mira") {
		t.Fatalf("wake after the wall came back: %v, running %v", err, w.launcher.isRunning("mira"))
	}
}

// Another way of breaking is a change too: the line names what breaks it now.
func TestCheckWall_ADifferentBreakIsLoggedAgain(t *testing.T) {
	t.Parallel()
	w := newUncheckedWorld(t)
	w.wall.set(brokenWall)
	w.sup.CheckWall(t.Context())
	w.wall.set(domain.JudgeNetWall([]string{"lo", "eth0", "eth1"}, []domain.Route{{Iface: "eth0"}, {Iface: "eth1"}}))
	if _, changed := w.sup.CheckWall(t.Context()); !changed {
		t.Fatal("a second network instead of a route out is a change")
	}
	if diff := cmp.Diff([]string{brokenLine, brokenLine}, w.logs.all()); diff != "" {
		t.Errorf("log (-want +got):\n%s", diff)
	}
}

// Without a wall check wired, the wall cannot be proven and nothing wakes.
func TestCheckWall_NotWiredFailsClosed(t *testing.T) {
	t.Parallel()
	w := newUncheckedWorld(t)
	sup := app.New(app.Deps{Clock: w.fakes.Clock, IDs: w.fakes.IDs, Ports: w.ports, Launcher: w.launcher, Config: app.DefaultConfig()})
	if got, _ := sup.CheckWall(t.Context()); got.Holds {
		t.Fatalf("CheckWall() without a check = %+v, want a wall that does not hold", got)
	}
	if err := sup.Wake(t.Context(), "mira", core.WakeChat, json.RawMessage(`{}`)); !errors.Is(err, app.ErrNetworkWall) {
		t.Fatalf("Wake() = %v, want ErrNetworkWall", err)
	}
}
