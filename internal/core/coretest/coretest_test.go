// SPDX-License-Identifier: MIT

package coretest_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestClock(t *testing.T) {
	c := coretest.NewClock(start)
	c.Advance(time.Minute)
	if got := c.Now(); !got.Equal(start.Add(time.Minute)) {
		t.Fatalf("after Advance: %v", got)
	}
	later := start.Add(time.Hour)
	c.Set(later)
	if got := c.Now(); !got.Equal(later) {
		t.Fatalf("after Set: %v", got)
	}
}

func TestNewIDsIsStable(t *testing.T) {
	a, b := coretest.NewIDs(), coretest.NewIDs()
	for range 3 {
		if x, y := a.New("agt"), b.New("agt"); x != y {
			t.Fatalf("%q != %q", x, y)
		}
	}
	if first, second := coretest.NewIDs().New("evt"), a.New("evt"); first == second {
		t.Fatalf("sequence did not advance: %q", first)
	}
}

func TestAgentsChart(t *testing.T) {
	ctx := context.Background()
	d := coretest.NewAgents()
	d.Add(core.Agent{Name: "founder", Kind: core.AgentRoot})
	d.Add(core.Agent{Name: "lead", Kind: core.AgentCustom, Manager: "founder"})
	d.Add(core.Agent{Name: "dev", Kind: core.AgentCustom, Manager: "lead"})

	got, err := d.GetAgent(ctx, "dev")
	if err != nil || got.Manager != "lead" {
		t.Fatalf("GetAgent: %+v %v", got, err)
	}
	all, err := d.ListAgents(ctx)
	if err != nil || len(all) != 3 || all[0].Name != "dev" {
		t.Fatalf("ListAgents sorted by name: %+v %v", all, err)
	}
	if m, _ := d.Manager(ctx, "lead"); m != "founder" {
		t.Fatalf("Manager: %q", m)
	}
	assertBelow(t, d, "dev", "founder", true)
	assertBelow(t, d, "founder", "dev", false)
	assertBelow(t, d, "dev", "dev", false)
	if root, _ := d.TeamRoot(ctx, "dev"); root != "founder" {
		t.Fatalf("TeamRoot: %q", root)
	}
	for name, call := range map[string]func() error{
		"GetAgent": func() error { _, err := d.GetAgent(ctx, "ghost"); return err },
		"Manager":  func() error { _, err := d.Manager(ctx, "ghost"); return err },
		"IsBelow":  func() error { _, err := d.IsBelow(ctx, "ghost", "founder"); return err },
		"TeamRoot": func() error { _, err := d.TeamRoot(ctx, "ghost"); return err },
	} {
		if err := call(); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("%s unknown agent: %v", name, err)
		}
	}
}

func assertBelow(t *testing.T, d *coretest.Agents, name, ancestor string, want bool) {
	t.Helper()
	got, err := d.IsBelow(context.Background(), name, ancestor)
	if err != nil || got != want {
		t.Errorf("IsBelow(%s, %s) = %v %v, want %v", name, ancestor, got, err, want)
	}
}

func TestWaker(t *testing.T) {
	w := coretest.NewWaker()
	payload := json.RawMessage(`{"task":"tsk_1"}`)
	if err := w.Wake(context.Background(), "dev", core.WakeTask, payload); err != nil {
		t.Fatal(err)
	}
	wakes := w.Wakes()
	if len(wakes) != 1 || wakes[0].Agent != "dev" || wakes[0].Reason != core.WakeTask || string(wakes[0].Payload) != string(payload) {
		t.Fatalf("wakes %+v", wakes)
	}
}

func TestEventsPublishAssignsIdentity(t *testing.T) {
	clock := coretest.NewClock(start)
	ev := coretest.NewEvents(clock, coretest.NewIDs())
	first, err := ev.Publish(context.Background(), core.Event{Type: "agents.one.git.push"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || !strings.HasPrefix(first.ID, "evt_") || !first.Time.Equal(start) || first.SpecVersion != "1.0" {
		t.Fatalf("first %+v", first)
	}
	second, _ := ev.Publish(context.Background(), core.Event{ID: "mine", Type: "x"})
	if second.Sequence != 2 || second.ID != "mine" {
		t.Fatalf("second %+v", second)
	}
	if got := ev.Published(); len(got) != 2 {
		t.Fatalf("published %d", len(got))
	}
}

func TestEventsSubscribeReplaysThenStreams(t *testing.T) {
	ev := coretest.NewEvents(coretest.NewClock(start), coretest.NewIDs())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	publish(t, ev, core.Event{Type: "agents.one.git.push", Source: "git"})
	publish(t, ev, core.Event{Type: "agents.one.clock.tick", Source: "clock"})
	publish(t, ev, core.Event{Type: "agents.one.git.tag", Source: "git"})

	ch, err := ev.Subscribe(ctx, core.EventFilter{Types: []string{"agents.one.git."}, AfterSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := receive(t, ch); got.Sequence != 3 {
		t.Fatalf("replay got sequence %d", got.Sequence)
	}
	publish(t, ev, core.Event{Type: "agents.one.clock.tick", Source: "clock"})
	publish(t, ev, core.Event{Type: "agents.one.git.push", Source: "git"})
	if got := receive(t, ch); got.Sequence != 5 {
		t.Fatalf("live got sequence %d", got.Sequence)
	}
	cancel()
	if _, open := <-ch; open {
		t.Fatal("channel open after cancel")
	}
}

func TestEventsSubscribeBySource(t *testing.T) {
	ev := coretest.NewEvents(coretest.NewClock(start), coretest.NewIDs())
	publish(t, ev, core.Event{Type: "a", Source: "git"})
	publish(t, ev, core.Event{Type: "a", Source: "clock"})
	ch, err := ev.Subscribe(context.Background(), core.EventFilter{Source: "clock"})
	if err != nil {
		t.Fatal(err)
	}
	if got := receive(t, ch); got.Source != "clock" {
		t.Fatalf("got %+v", got)
	}
}

func publish(t *testing.T, ev *coretest.Events, e core.Event) {
	t.Helper()
	if _, err := ev.Publish(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

func receive(t *testing.T, ch <-chan core.Event) core.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2s")
		return core.Event{}
	}
}

func TestTasks(t *testing.T) {
	ts := coretest.NewTasks(coretest.NewClock(start), coretest.NewIDs())
	task, err := ts.CreateTask(context.Background(), core.NewTask{Title: "Fix login", Assignee: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(task.ID, "tsk_") || task.Status != "open" || task.Assignee != "dev" || !task.Created.Equal(start) {
		t.Fatalf("task %+v", task)
	}
	if got := ts.Created(); len(got) != 1 || got[0].Title != "Fix login" {
		t.Fatalf("created %+v", got)
	}
}

func TestAudit(t *testing.T) {
	a := coretest.NewAudit()
	if err := a.Append(context.Background(), core.AuditEntry{Actor: "founder", Action: "agent.hire", Target: "dev"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Entries(); len(got) != 1 || got[0].Action != "agent.hire" {
		t.Fatalf("entries %+v", got)
	}
}

func TestLive(t *testing.T) {
	ctx := context.Background()
	l := coretest.NewLive(coretest.NewClock(start))
	if err := l.UpdateLine(ctx, "dev", "Fixing the login bug"); err != nil {
		t.Fatal(err)
	}
	if err := l.UpdateLine(ctx, "dev", strings.Repeat("x", core.MaxLiveLine+1)); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("long line: %v", err)
	}
	lines, err := l.Lines(ctx)
	if err != nil || len(lines) != 1 || lines[0].Line != "Fixing the login bug" || !lines[0].Updated.Equal(start) {
		t.Fatalf("lines %+v %v", lines, err)
	}
}

func TestBudget(t *testing.T) {
	b := coretest.NewBudget()
	b.Set(core.Budget{Agent: "dev", AllocatedMicros: 100, RemainingMicros: 40})
	got, err := b.Allocation(context.Background(), "dev")
	if err != nil || got.RemainingMicros != 40 {
		t.Fatalf("allocation %+v %v", got, err)
	}
	if _, err := b.Allocation(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

type echoAdapter struct{}

func (echoAdapter) Info() core.RunnerInfo { return core.RunnerInfo{Name: "echo", Source: "builtin"} }
func (echoAdapter) Exec(context.Context, core.Agent, string) (core.ExecSpec, error) {
	return core.ExecSpec{Path: "/usr/lib/agentsone/runners/echo"}, nil
}

func TestRunners(t *testing.T) {
	ctx := context.Background()
	r := coretest.NewRunners()
	r.Register(echoAdapter{})
	got, err := r.Runner(ctx, "echo")
	if err != nil || got.Info().Name != "echo" {
		t.Fatalf("runner %v %v", got, err)
	}
	infos, err := r.Runners(ctx)
	if err != nil || len(infos) != 1 {
		t.Fatalf("runners %+v %v", infos, err)
	}
	if _, err := r.Runner(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestNewFakesFillsEveryPort(t *testing.T) {
	f, p := coretest.NewFakes()
	if p.Agents != f.Agents || p.Waker != f.Waker || p.Events != f.Events || p.Subscribe != f.Events ||
		p.Tasks != f.Tasks || p.Audit != f.Audit || p.Live != f.Live || p.Budget != f.Budget || p.Runners != f.Runners {
		t.Fatal("ports and fakes differ")
	}
	if coretest.NewPorts().Agents == nil {
		t.Fatal("NewPorts left a port empty")
	}
}

func TestNewDeps(t *testing.T) {
	deps, fakes := coretest.NewDeps(t)
	if deps.Ports.Audit != fakes.Audit || deps.Log == nil || deps.Clock == nil || deps.IDs == nil {
		t.Fatalf("deps %+v", deps)
	}
	for name, db := range map[string]interface{ Path() string }{
		"core": deps.Stores.Core, "bus": deps.Stores.Bus, "memory": deps.Stores.Memory, "audit": deps.Stores.Audit,
	} {
		if want := deps.Config.DBPath(name); db.Path() != want {
			t.Errorf("%s store at %s, want %s", name, db.Path(), want)
		}
	}
	if err := deps.Stores.Core.SQL().Ping(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{deps.Config.StateDir, deps.Config.RuntimeDir, deps.Config.AgentsHome} {
		if !strings.HasPrefix(dir, os.TempDir()) {
			t.Errorf("%s is outside the temp folder", dir)
		}
	}
}
