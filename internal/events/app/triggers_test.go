// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func wakeSpec(agent string) domain.TriggerSpec {
	return domain.TriggerSpec{
		Name:    "disk alarm",
		Filter:  domain.Filter{Types: []string{"agentsone.disk.*"}},
		Action:  domain.Action{Kind: domain.ActionWake, Agent: agent, Message: "Disk: {{type}} at {{subject}}"},
		Rate:    domain.TriggerRate{Max: 2, PerSeconds: 60},
		Enabled: true,
	}
}

func publishDisk(t *testing.T, f *fixture) {
	t.Helper()
	if _, err := f.svc.PublishDraft(asSystem(), diskEvent()); err != nil {
		t.Fatal(err)
	}
}

func createTrigger(t *testing.T, f *fixture, ctx context.Context, spec domain.TriggerSpec) domain.Trigger {
	t.Helper()
	tr, err := f.svc.CreateTrigger(ctx, spec)
	if err != nil {
		t.Fatalf("CreateTrigger() error = %v", err)
	}
	return tr
}

func TestCreateTrigger_Ownership(t *testing.T) {
	f := newFixture(t)

	own := createTrigger(t, f, asAgent("builder"), wakeSpec("builder"))
	below := createTrigger(t, f, asAgent("builder"), wakeSpec("intern"))
	byHuman := createTrigger(t, f, asHuman("hsaddek"), wakeSpec("outsider"))

	if own.Owner != "builder" || below.Owner != "builder" || byHuman.Owner != "outsider" {
		t.Fatalf("owners = %q %q %q", own.Owner, below.Owner, byHuman.Owner)
	}
	if _, err := f.svc.CreateTrigger(asAgent("builder"), wakeSpec("outsider")); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("trigger on a peer team error = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.CreateTrigger(asAgent("intern"), wakeSpec("builder")); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("trigger on a manager error = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.CreateTrigger(asHuman("h"), wakeSpec("ghost")); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown agent error = %v, want ErrInvalid", err)
	}
	bad := wakeSpec("builder")
	bad.Filter = domain.Filter{}
	if _, err := f.svc.CreateTrigger(asAgent("builder"), bad); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty filter error = %v, want ErrInvalid", err)
	}
}

func TestTrigger_WakesTheAgentWithAReasonOfEvent(t *testing.T) {
	f := newFixture(t)
	tr := createTrigger(t, f, asAgent("builder"), wakeSpec("builder"))

	publishDisk(t, f)

	wakes := f.wakes()
	if len(wakes) != 1 || wakes[0].Agent != "builder" || wakes[0].Reason != core.WakeEvent {
		t.Fatalf("wakes = %+v", wakes)
	}
	got, _ := f.svc.GetTrigger(asHuman("h"), tr.ID)
	if got.FiredCount != 1 || got.LastFiredAt == nil {
		t.Fatalf("trigger after fire = %+v", got)
	}
	if actions := f.auditActions(); !slices.Contains(actions, "trigger.fire") {
		t.Fatalf("audit actions = %v", actions)
	}
}

// TESTING B1: at the limit allowed, one over refused, allowed after the
// window, and the window edge.
func TestTrigger_WakeRateLimit(t *testing.T) {
	f := newFixture(t)
	tr := createTrigger(t, f, asAgent("builder"), wakeSpec("builder")) // 2 per 60 s

	publishDisk(t, f)
	f.clock.Advance(window / 2)
	publishDisk(t, f) // at the limit: allowed
	if got := len(f.wakes()); got != 2 {
		t.Fatalf("wakes at the limit = %d, want 2", got)
	}
	publishDisk(t, f) // one over: refused
	if got := len(f.wakes()); got != 2 {
		t.Fatalf("wakes one over the limit = %d, want 2", got)
	}
	got, _ := f.svc.GetTrigger(asHuman("h"), tr.ID)
	if got.DroppedCount != 1 || got.FiredCount != 2 {
		t.Fatalf("counters = fired %d dropped %d", got.FiredCount, got.DroppedCount)
	}

	f.clock.Advance(window / 2) // the first fire is exactly one window old: it no longer counts
	publishDisk(t, f)
	if got := len(f.wakes()); got != 3 {
		t.Fatalf("wakes at the window edge = %d, want 3", got)
	}
	f.clock.Advance(window)
	publishDisk(t, f)
	if got := len(f.wakes()); got != 4 {
		t.Fatalf("wakes after the window = %d, want 4", got)
	}
}

func TestTrigger_NeverWakesAFrozenAgent(t *testing.T) {
	f := newFixture(t)
	tr := createTrigger(t, f, asAgent("builder"), wakeSpec("builder"))
	f.waker.err = core.ErrFrozen

	publishDisk(t, f)

	if len(f.wakes()) != 0 {
		t.Fatal("a frozen agent was woken")
	}
	got, _ := f.svc.GetTrigger(asHuman("h"), tr.ID)
	if got.FiredCount != 0 {
		t.Fatalf("a skipped wake counted as a fire: %+v", got)
	}
}

func TestTrigger_ActionFailureIsNotCountedAndDoesNotFailThePublish(t *testing.T) {
	f := newFixture(t)
	tr := createTrigger(t, f, asAgent("builder"), wakeSpec("builder"))
	f.waker.err = errors.New("boom")

	publishDisk(t, f)

	got, _ := f.svc.GetTrigger(asHuman("h"), tr.ID)
	if got.FiredCount != 0 {
		t.Fatalf("a failed wake counted as a fire: %+v", got)
	}
}

func TestTrigger_CreatesATaskWithTheRenderedTitle(t *testing.T) {
	f := newFixture(t)
	spec := wakeSpec("builder")
	spec.Action = domain.Action{Kind: domain.ActionCreateTask, Agent: "builder", Title: "Clean {{subject}}", Priority: "high"}
	createTrigger(t, f, asAgent("builder"), spec)

	publishDisk(t, f)

	if len(f.tasks.new) != 1 {
		t.Fatalf("tasks = %+v", f.tasks.new)
	}
	got := f.tasks.new[0]
	if got.Title != "Clean /var" || got.Assignee != "builder" || got.Priority != "high" || got.CreatedBy != "builder" || got.CauseEventID == "" {
		t.Fatalf("task = %+v", got)
	}
	if len(f.wakes()) != 0 {
		t.Fatal("a create-task trigger woke the agent")
	}
}

func TestTrigger_SkipsDisabledTriggersAndEventsTheOwnerCannotSee(t *testing.T) {
	f := newFixture(t)
	off := wakeSpec("builder")
	off.Enabled = false
	createTrigger(t, f, asAgent("builder"), off)
	hired := wakeSpec("outsider")
	hired.Filter = domain.Filter{Types: []string{"agentsone.agents.hired"}}
	createTrigger(t, f, asHuman("h"), hired)

	publishDisk(t, f)
	// A hire in the founder team is out of sight for the outsider's trigger.
	if _, err := f.svc.PublishDraft(asSystem(), domain.Draft{Type: "agentsone.agents.hired", Source: "/emitters/agents",
		Data: []byte(`{"agent":"builder"}`)}); err != nil {
		t.Fatal(err)
	}

	if len(f.wakes()) != 0 {
		t.Fatalf("wakes = %+v, want none", f.wakes())
	}
}

func TestListTriggers_ShowsTheCallerOwnAndBelow(t *testing.T) {
	f := newFixture(t)
	createTrigger(t, f, asHuman("h"), wakeSpec("builder"))
	createTrigger(t, f, asHuman("h"), wakeSpec("intern"))
	createTrigger(t, f, asHuman("h"), wakeSpec("outsider"))

	all, more, err := f.svc.ListTriggers(asHuman("h"), "", 10)
	if err != nil || len(all) != 3 || more {
		t.Fatalf("human list = %d, more %v, %v", len(all), more, err)
	}
	mine, _, err := f.svc.ListTriggers(asAgent("builder"), "", 10)
	if err != nil || len(mine) != 2 {
		t.Fatalf("builder list = %d, %v, want 2", len(mine), err)
	}
	first, more, err := f.svc.ListTriggers(asHuman("h"), "", 2)
	if err != nil || len(first) != 2 || !more {
		t.Fatalf("page = %d, more %v, %v", len(first), more, err)
	}
	if _, err := f.svc.GetTrigger(asAgent("builder"), all[2].ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("a trigger out of sight error = %v, want ErrNotFound", err)
	}
}

func TestUpdateAndDeleteTrigger(t *testing.T) {
	f := newFixture(t)
	tr := createTrigger(t, f, asAgent("intern"), wakeSpec("intern"))
	name := "renamed"

	if _, err := f.svc.UpdateTrigger(asAgent("outsider"), tr.ID, domain.TriggerPatch{Name: &name}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("stranger update error = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.UpdateTrigger(asAgent("builder"), tr.ID, domain.TriggerPatch{Name: &name}); err != nil {
		t.Fatalf("manager update error = %v", err)
	}
	got, err := f.svc.UpdateTrigger(asAgent("intern"), tr.ID, domain.TriggerPatch{Name: &name})
	if err != nil || got.Name != "renamed" {
		t.Fatalf("owner update = %+v, %v", got, err)
	}
	zero := domain.TriggerRate{}
	if _, err := f.svc.UpdateTrigger(asAgent("intern"), tr.ID, domain.TriggerPatch{Rate: &zero}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("invalid patch error = %v, want ErrInvalid", err)
	}
	away := domain.Action{Kind: domain.ActionWake, Agent: "outsider", Message: "x"}
	if _, err := f.svc.UpdateTrigger(asAgent("intern"), tr.ID, domain.TriggerPatch{Action: &away}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("retarget outside the chart error = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.UpdateTrigger(asHuman("h"), "trg_missing", domain.TriggerPatch{Name: &name}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown trigger error = %v, want ErrNotFound", err)
	}

	if err := f.svc.DeleteTrigger(asAgent("builder"), tr.ID); err != nil {
		t.Fatalf("DeleteTrigger() error = %v", err)
	}
	if err := f.svc.DeleteTrigger(asAgent("builder"), tr.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second delete error = %v, want ErrNotFound", err)
	}
}
