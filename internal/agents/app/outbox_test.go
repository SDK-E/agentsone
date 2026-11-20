// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/adapters/memory"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var errBusDown = errors.New("events store unreachable")

// flakyBus is the events port when the events store is in trouble. With
// reach set the event reaches the store and only the answer is lost, as
// when the daemon stops between the publish and marking the event sent.
type flakyBus struct {
	next  core.EventPublisher
	reach bool
}

func (b flakyBus) Publish(ctx context.Context, e core.Event) (core.Event, error) {
	if b.reach {
		if _, err := b.next.Publish(ctx, e); err != nil {
			return core.Event{}, err
		}
	}
	return core.Event{}, errBusDown
}

func (e env) hiredEvents() []core.Event {
	var out []core.Event
	for _, ev := range e.fakes.Events.Published() {
		if ev.Type == "agentsone.agents.hired" {
			out = append(out, ev)
		}
	}
	return out
}

// Agents and events live in different stores. The hire stores the agent
// with its event in one transaction, so a bus that fails after the commit
// does not undo the hire: the event waits in the outbox and the next flush
// sends it, once.
func TestHire_KeepsTheHireWhenTheBusFailsAndSendsTheEventOnceLater(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	bus := e.ports.Events
	e.ports.Events = flakyBus{next: bus}

	if _, err := e.society.Hire(asAgent("lead"), spec("mira")); err != nil {
		t.Fatalf("Hire() error = %v, want the hire kept", err)
	}

	if _, err := e.repo.GetAgent(context.Background(), "mira"); err != nil {
		t.Fatalf("agent not stored: %v", err)
	}
	if got := e.hiredEvents(); len(got) != 0 {
		t.Fatalf("published %d hired events while the bus was down", len(got))
	}
	out := e.pending(t)
	if len(out) != 1 || out[0].Type != "agentsone.agents.hired" {
		t.Fatalf("outbox = %v, want the hired event", out)
	}

	e.ports.Events = bus
	for range 2 {
		if err := e.society.Flush(context.Background()); err != nil {
			t.Fatalf("Flush() error = %v", err)
		}
	}

	got := e.hiredEvents()
	if len(got) != 1 {
		t.Fatalf("published %d hired events after two flushes, want 1", len(got))
	}
	if got[0].ID != out[0].ID || got[0].Actor != "agent:lead" || got[0].Source != "/agents/lead" || got[0].ChartScope != "team:lead" {
		t.Errorf("event %+v, want id %s from lead", got[0], out[0].ID)
	}
	if p := e.pending(t); len(p) != 0 {
		t.Errorf("outbox still holds %v", p)
	}
}

// The event reached the store but the daemon stopped before it marked the
// event sent. The resend names the same id, which the bus takes as already
// published: one event, no error.
func TestFlush_AnEventAlreadyStoredIsTakenAsSent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	bus := e.ports.Events
	e.ports.Events = flakyBus{next: bus, reach: true}
	if _, err := e.society.Hire(asAgent("lead"), spec("mira")); err != nil {
		t.Fatal(err)
	}
	if n := len(e.pending(t)); n != 1 {
		t.Fatalf("outbox holds %d events, want the unconfirmed one", n)
	}

	e.ports.Events = bus
	if err := e.society.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v, want a duplicate taken as sent", err)
	}

	if got := e.hiredEvents(); len(got) != 1 {
		t.Fatalf("published %d hired events, want 1", len(got))
	}
	if p := e.pending(t); len(p) != 0 {
		t.Errorf("outbox still holds %v", p)
	}
}

func TestFlush_ReportsABusFailureAndKeepsTheEvent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.ports.Events = flakyBus{next: e.ports.Events}
	if _, err := e.society.Hire(asAgent("lead"), spec("mira")); err != nil {
		t.Fatal(err)
	}

	err := e.society.Flush(context.Background())

	if !errors.Is(err, errBusDown) {
		t.Fatalf("Flush() error = %v, want %v", err, errBusDown)
	}
	if diff := cmp.Diff(1, len(e.pending(t))); diff != "" {
		t.Errorf("pending (-want +got):\n%s", diff)
	}
}

// brokenLive is the router's live list when it cannot store a line, so a
// template's seed fails after the hire committed.
type brokenLive struct{ core.LiveList }

func (brokenLive) UpdateLine(context.Context, string, string) error { return errBusDown }

// The template's seed runs after the hire committed. When it fails the
// hirer hears of it, but the hire stands, and it stands on the record: the
// agent, one hired event and the audit record.
func TestHire_ASeedThatFailsAfterTheCommitLeavesTheHireAuditedAndAnnounced(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	addTemplate(e, "small-seed/docs-writer", false)
	e.ports.Live = brokenLive{e.ports.Live}
	s := spec("scribe")
	s.Template = "small-seed/docs-writer"

	_, err := e.society.Hire(asAgent("lead"), s)

	if !errors.Is(err, errBusDown) {
		t.Fatalf("Hire() error = %v, want the seed failure", err)
	}
	if _, err := e.repo.GetAgent(context.Background(), "scribe"); err != nil {
		t.Fatalf("agent not stored: %v", err)
	}
	if got := e.hiredEvents(); len(got) != 1 {
		t.Fatalf("published %d hired events, want 1", len(got))
	}
	if diff := cmp.Diff([]string{"agent.hire"}, e.auditActions()); diff != "" {
		t.Fatalf("audit (-want +got):\n%s", diff)
	}
}

func TestFound_ASeedThatFailsAfterTheCommitLeavesTheFounderAudited(t *testing.T) {
	t.Parallel()
	fakes, ports := coretest.NewFakes()
	e := env{fakes: fakes, ports: ports, repo: memory.New(), rt: agentstest.NewRuntime()}
	e.society = app.New(e.repo, e.rt, ports, fakes.Clock, fakes.IDs)
	addTemplate(e, "small-seed/docs-writer", true)
	ports.Live = brokenLive{ports.Live}
	sys := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "seed"})
	founder := spec("founder")
	founder.Template = "small-seed/docs-writer"

	_, err := e.society.Found(sys, founder)

	if !errors.Is(err, errBusDown) {
		t.Fatalf("Found() error = %v, want the seed failure", err)
	}
	if got := e.hiredEvents(); len(got) != 1 {
		t.Fatalf("published %d hired events, want 1", len(got))
	}
	if diff := cmp.Diff([]string{"agent.found"}, e.auditActions()); diff != "" {
		t.Fatalf("audit (-want +got):\n%s", diff)
	}
}

func TestFound_StoresTheFounderWithItsEvent(t *testing.T) {
	t.Parallel()
	fakes, ports := coretest.NewFakes()
	e := env{fakes: fakes, ports: ports, repo: memory.New(), rt: agentstest.NewRuntime()}
	e.society = app.New(e.repo, e.rt, ports, fakes.Clock, fakes.IDs)
	ports.Events = flakyBus{next: ports.Events}
	sys := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "seed"})
	founder := spec("founder")
	founder.Kind = domain.KindAlwaysOn

	if _, err := e.society.Found(sys, founder); err != nil {
		t.Fatalf("Found() error = %v, want the founder kept", err)
	}

	if p := e.pending(t); len(p) != 1 || p[0].Actor != "system:seed" {
		t.Fatalf("outbox = %v, want the founder's hired event", p)
	}
}
