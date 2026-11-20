// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

func TestPublishDraft_StoresWithActorFromTheCaller(t *testing.T) {
	f := newFixture(t)

	e, err := f.svc.PublishDraft(asAgent("builder"), custom("agentsone.builder.done"))

	if err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}
	if e.Actor != "agent:builder" || e.Source != "/agents/builder" || e.Sequence != "00000000000000000001" {
		t.Fatalf("event = actor %q source %q seq %q", e.Actor, e.Source, e.Sequence)
	}
	if e.ChartScope != "team:founder" || e.CorrelationID != e.ID || e.Time == nil || !e.Time.Equal(f.clock.Now()) {
		t.Fatalf("event = scope %q correlation %q time %v", e.ChartScope, e.CorrelationID, e.Time)
	}
}

func TestPublishDraft_AuditsTheCall(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.PublishDraft(asAgent("builder"), custom("agentsone.builder.done")); err != nil {
		t.Fatal(err)
	}

	if got := f.auditActions(); len(got) != 1 || got[0] != "event.publish" {
		t.Fatalf("audit actions = %v", got)
	}
}

func TestPublishDraft_Refusals(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() (draft domain.Draft, who string)
		want error
	}{
		{"another agent's source", func() (domain.Draft, string) {
			d := custom("agentsone.builder.done")
			d.Source = "/agents/intern"
			return d, "builder"
		}, domain.ErrSourceForbidden},
		{"a built-in type from an agent", func() (domain.Draft, string) { return custom("agentsone.disk.full"), "builder" }, domain.ErrTypeReserved},
		{"a built-in emitter source the agent does not own", func() (domain.Draft, string) { return diskEvent(), "builder" }, domain.ErrSourceForbidden},
		{"an invalid type", func() (domain.Draft, string) { return custom("Nope"), "builder" }, domain.ErrInvalidType},
		{"unknown emitter", func() (domain.Draft, string) {
			d := custom("agentsone.zed.up")
			d.Source = "/emitters/zed"
			return d, "builder"
		}, core.ErrNotFound},
		{"data the catalog rejects", func() (domain.Draft, string) {
			d := diskEvent()
			d.Data = json.RawMessage(`{"bad":1}`)
			return d, ""
		}, core.ErrInvalid},
		{"data over the limit", func() (domain.Draft, string) {
			d := custom("agentsone.builder.done")
			d.Data = json.RawMessage(`{"a":"` + string(make([]byte, domain.MaxDataBytes)) + `"}`)
			return d, "builder"
		}, domain.ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			d, who := tt.ctx()

			ctx := asSystem()
			if who != "" {
				ctx = asAgent(who)
			}

			_, err := f.svc.PublishDraft(ctx, d)

			if !errors.Is(err, tt.want) {
				t.Fatalf("PublishDraft() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPublishDraft_EmitterRules(t *testing.T) {
	f := newFixture(t)
	reg, err := f.svc.RegisterEmitter(asAgent("builder"), domain.Registration{
		Name: "builds", Kind: domain.KindWebhook, Description: "Build hooks.", EventTypes: []string{"agentsone.builds.*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.Draft{Type: "agentsone.builds.done", Source: "/emitters/builds", Data: json.RawMessage(`{}`)}

	if _, err := f.svc.PublishDraft(asAgent("builder"), draft); err != nil {
		t.Fatalf("owner publish error = %v", err)
	}
	if _, err := f.svc.PublishDraft(asAgent("intern"), draft); !errors.Is(err, domain.ErrSourceForbidden) {
		t.Fatalf("stranger publish error = %v, want ErrSourceForbidden", err)
	}
	other := draft
	other.Type = "agentsone.other.done"
	if _, err := f.svc.PublishDraft(asAgent("builder"), other); !errors.Is(err, domain.ErrTypeNotAllowed) {
		t.Fatalf("type outside the emitter error = %v, want ErrTypeNotAllowed", err)
	}
	if _, err := f.svc.DisableEmitter(asAgent("builder"), reg.Emitter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.PublishDraft(asAgent("builder"), draft); !errors.Is(err, domain.ErrEmitterDisabled) {
		t.Fatalf("disabled emitter error = %v, want ErrEmitterDisabled", err)
	}
}

func TestPublishDraft_SystemMayPublishABuiltInTypeAndTouchesTheEmitter(t *testing.T) {
	f := newFixture(t)

	e, err := f.svc.PublishDraft(asSystem(), diskEvent())

	if err != nil || e.Actor != "system:daemon" {
		t.Fatalf("PublishDraft() = %+v, %v", e, err)
	}
	disk, err := f.emitters.ByName(t.Context(), "disk")
	if err != nil || disk.LastEventAt == nil || !disk.LastEventAt.Equal(*e.Time) {
		t.Fatalf("emitter after publish = %+v, %v", disk, err)
	}
}

func TestPublishDraft_ChartScope(t *testing.T) {
	tests := []struct {
		name string
		d    domain.Draft
		want string
	}{
		{"machine type", diskEvent(), "machine"},
		{"agent type", domain.Draft{Type: "agentsone.agents.woke", Source: "/emitters/agents", Data: json.RawMessage(`{"agent":"intern"}`)}, "agent:intern"},
		{"team type", domain.Draft{Type: "agentsone.agents.hired", Source: "/emitters/agents", Data: json.RawMessage(`{"agent":"intern"}`)}, "team:founder"},
		{"team type for an agent that is gone", domain.Draft{Type: "agentsone.agents.hired", Source: "/emitters/agents", Data: json.RawMessage(`{"agent":"ghost"}`)}, "machine"},
		{"agent type with no agent", domain.Draft{Type: "agentsone.agents.woke", Source: "/emitters/agents", Data: json.RawMessage(`{}`)}, "machine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)

			e, err := f.svc.PublishDraft(asSystem(), tt.d)

			if err != nil || e.ChartScope != tt.want {
				t.Fatalf("scope = %q, %v, want %q", e.ChartScope, err, tt.want)
			}
		})
	}
}

func TestPublishDraft_HumanDefaultsToTheDaemonSourceAndPluginHasNone(t *testing.T) {
	f := newFixture(t)

	e, err := f.svc.PublishDraft(asHuman("hsaddek"), custom("agentsone.note.added"))
	if err != nil || e.Source != "/daemon" || e.Actor != "human:hsaddek" || e.ChartScope != "machine" {
		t.Fatalf("human publish = %+v, %v", e, err)
	}
	plugin := core.WithCaller(t.Context(), core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD8X3Q5Y6Z7A8B9C0D1E2F60"})
	if _, err := f.svc.PublishDraft(plugin, custom("agentsone.note.added")); !errors.Is(err, domain.ErrSourceForbidden) {
		t.Fatalf("plugin publish error = %v, want ErrSourceForbidden", err)
	}
}

func TestPublishDraft_RateLimitsAPICallers(t *testing.T) {
	f := newFixture(t)
	ctx := asAgent("builder")
	for i := range 600 {
		if _, err := f.svc.PublishDraft(ctx, custom("agentsone.builder.done")); err != nil {
			t.Fatalf("publish %d error = %v", i, err)
		}
	}

	_, err := f.svc.PublishDraft(ctx, custom("agentsone.builder.done"))

	var problem *core.Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusTooManyRequests {
		t.Fatalf("601st publish error = %v, want 429", err)
	}
	f.clock.Advance(window)
	if _, err := f.svc.PublishDraft(ctx, custom("agentsone.builder.done")); err != nil {
		t.Fatalf("publish after the window error = %v", err)
	}
}

func TestPublish_PortKeepsAGivenIDAndAssignsOtherwise(t *testing.T) {
	f := newFixture(t)
	given := core.Event{ID: "evt_01JD8X3Q5Y6Z7A8B9C0D1E2F60", Type: "agentsone.agents.woke", Source: "/emitters/agents",
		Data: json.RawMessage(`{"agent":"builder"}`), ChartScope: "agent:builder"}

	kept, err := f.svc.Publish(asSystem(), given)
	if err != nil || kept.ID != given.ID || kept.Actor != "system:daemon" || kept.ChartScope != "agent:builder" {
		t.Fatalf("Publish() = %+v, %v", kept, err)
	}
	given.ID, given.Type = "", "agentsone.agents.woke"
	assigned, err := f.svc.Publish(asSystem(), given)
	if err != nil || assigned.ID == "" || assigned.ID == kept.ID {
		t.Fatalf("Publish() without id = %+v, %v", assigned, err)
	}
}

// A module that fixed an event's id before publishing (an outbox) resends
// it after a crash. The id names one event, so the resend is taken as
// already published: no second event is stored or streamed, and no error.
func TestPublish_PortTakesAStoredIDAsAlreadyPublished(t *testing.T) {
	f := newFixture(t)
	given := core.Event{ID: "evt_01JD8X3Q5Y6Z7A8B9C0D1E2F60", Type: "agentsone.agents.woke", Source: "/emitters/agents",
		Data: json.RawMessage(`{"agent":"builder"}`), ChartScope: "agent:builder"}
	if _, err := f.svc.Publish(asSystem(), given); err != nil {
		t.Fatal(err)
	}

	again, err := f.svc.Publish(asSystem(), given)

	if err != nil || again.ID != given.ID {
		t.Fatalf("second Publish() = %+v, %v, want the same id and no error", again, err)
	}
	stored, err := f.events.After(t.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored %d events, want 1", len(stored))
	}
}

// The port takes the actor from the caller, so an agent that names the
// system as the actor still publishes as itself.
func TestPublish_PortNeverLetsAnAgentPublishAsTheSystem(t *testing.T) {
	callers := map[string]context.Context{"agent:builder": asAgent("builder"), "agent:founder": asRootAgent("founder")}
	for want, ctx := range callers {
		t.Run(want, func(t *testing.T) {
			f := newFixture(t)

			e, err := f.svc.Publish(ctx, core.Event{
				Type: "agentsone.custom.thing", Data: json.RawMessage(`{}`), Actor: "system:auth", Source: "/daemon",
			})

			if err == nil && e.Actor != want {
				t.Fatalf("Publish() actor = %q, want %s or a refusal", e.Actor, want)
			}
		})
	}
}

func hired(agent, manager string) core.Event {
	return core.Event{
		Type: "agentsone.agents.hired", Source: "/agents/" + manager, ChartScope: "team:founder",
		Data: json.RawMessage(`{"agent":"` + agent + `","manager":"` + manager + `","kind":"chat","is_root":false,"template":""}`),
	}
}

// A daemon module announces a fact it has checked as itself and names the
// agent that caused it: the hire is the founder's, the event the daemon's.
func TestPublish_PortWritesTheActorAModuleNames(t *testing.T) {
	f := newFixture(t)
	module := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "agents", UID: -1})
	ev := hired("builder", "founder")
	ev.Actor = "agent:founder"

	e, err := f.svc.Publish(module, ev)

	if err != nil || e.Actor != "agent:founder" || e.Source != "/agents/founder" || e.ChartScope != "team:founder" {
		t.Fatalf("Publish() = actor %q source %q scope %q, %v", e.Actor, e.Source, e.ChartScope, err)
	}
	ev.Actor = "agent:Founder!"
	if _, err := f.svc.Publish(module, ev); !errors.Is(err, domain.ErrInvalidActor) {
		t.Fatalf("Publish() with a malformed actor error = %v, want ErrInvalidActor", err)
	}
}

// An agent that asks the bus itself for a built-in type is still refused,
// root or not by token, whatever actor it names: only the module that owns
// the fact announces it.
func TestPublish_PortRefusesABuiltInTypeFromAnAgentWithAToken(t *testing.T) {
	f := newFixture(t)
	token := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "founder", UID: -1, Assurance: core.AssuranceSoft})
	ev := hired("builder", "founder")
	ev.Actor = "system:agents"

	_, err := f.svc.Publish(token, ev)

	if !errors.Is(err, domain.ErrTypeReserved) {
		t.Fatalf("Publish() error = %v, want ErrTypeReserved", err)
	}
	if page, err := f.svc.List(asSystem(), app.Query{Limit: 10}); err != nil || len(page.Events) != 0 {
		t.Fatalf("stored %v, %v, want nothing", page.Events, err)
	}
}

func TestPublish_PortIgnoresAChartScopeFromUnprivilegedCallers(t *testing.T) {
	f := newFixture(t)

	e, err := f.svc.Publish(asAgent("builder"), core.Event{Type: "agentsone.builder.done", Data: json.RawMessage(`{}`), ChartScope: "machine"})

	if err != nil || e.ChartScope != "team:founder" {
		t.Fatalf("Publish() scope = %q, %v", e.ChartScope, err)
	}
}
