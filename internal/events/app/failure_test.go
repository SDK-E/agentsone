// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

var errStore = errors.New("store is down")

// flakyEvents fails the calls whose flag is set.
type flakyEvents struct {
	domain.EventRepository
	appendFails, lastFails, afterFails bool
}

func (r *flakyEvents) Append(ctx context.Context, e core.Event) (core.Event, error) {
	if r.appendFails {
		return core.Event{}, errStore
	}
	return r.EventRepository.Append(ctx, e)
}

func (r *flakyEvents) Last(ctx context.Context) (uint64, error) {
	if r.lastFails {
		return 0, errStore
	}
	return r.EventRepository.Last(ctx)
}

func (r *flakyEvents) After(ctx context.Context, after uint64, limit int) ([]core.Event, error) {
	if r.afterFails {
		return nil, errStore
	}
	return r.EventRepository.After(ctx, after, limit)
}

type flakyTriggers struct {
	domain.TriggerRepository
	enabledFails, countersFail bool
}

func (r *flakyTriggers) Enabled(ctx context.Context) ([]domain.Trigger, error) {
	if r.enabledFails {
		return nil, errStore
	}
	return r.TriggerRepository.Enabled(ctx)
}

func (r *flakyTriggers) RecordDrop(ctx context.Context, id string) error {
	if r.countersFail {
		return errStore
	}
	return r.TriggerRepository.RecordDrop(ctx, id)
}

func (r *flakyTriggers) RecordFire(ctx context.Context, id string, at time.Time) error {
	if r.countersFail {
		return errStore
	}
	return r.TriggerRepository.RecordFire(ctx, id, at)
}

func withTriggers(r *flakyTriggers) func(*app.Deps) {
	return func(d *app.Deps) {
		r.TriggerRepository = d.Triggers
		d.Triggers = r
	}
}

type flakyEmitters struct {
	domain.EmitterRepository
	touchFails bool
}

func (r flakyEmitters) Touch(ctx context.Context, name string, at time.Time) error {
	if r.touchFails {
		return errStore
	}
	return r.EmitterRepository.Touch(ctx, name, at)
}

func withEvents(r *flakyEvents) func(*app.Deps) {
	return func(d *app.Deps) {
		r.EventRepository = d.Events
		d.Events = r
	}
}

func TestPublish_AFailingStoreFailsThePublish(t *testing.T) {
	repo := &flakyEvents{appendFails: true}
	f := newFixtureWith(t, withEvents(repo))

	if _, err := f.svc.PublishDraft(asSystem(), diskEvent()); !errors.Is(err, errStore) {
		t.Fatalf("PublishDraft() error = %v, want the store error", err)
	}
}

func TestStream_StoreFailures(t *testing.T) {
	repo := &flakyEvents{lastFails: true}
	f := newFixtureWith(t, withEvents(repo))
	if _, err := f.svc.Stream(asHuman("h"), domain.Filter{}, nil); !errors.Is(err, errStore) {
		t.Fatalf("Stream() error = %v, want the store error", err)
	}

	repo.lastFails = false
	publishDisk(t, f)
	repo.afterFails = true
	if _, err := f.svc.List(asHuman("h"), app.Query{Limit: 5}); !errors.Is(err, errStore) {
		t.Fatalf("List() error = %v, want the store error", err)
	}
	after := uint64(0)
	ch, err := f.svc.Stream(asHuman("h"), domain.Filter{}, &after)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("a stream whose replay failed delivered an event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stream whose replay failed stayed open")
	}
}

func TestPublish_AFailingTriggerStoreOrEmitterTouchDoesNotFailIt(t *testing.T) {
	triggers := &flakyTriggers{enabledFails: true}
	f := newFixtureWith(t, func(d *app.Deps) {
		withTriggers(triggers)(d)
		d.Emitters = flakyEmitters{EmitterRepository: d.Emitters, touchFails: true}
	})

	if _, err := f.svc.PublishDraft(asSystem(), diskEvent()); err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}
}

func TestTrigger_FailingCountersDoNotStopAWakeOrTheRateLimit(t *testing.T) {
	triggers := &flakyTriggers{}
	f := newFixtureWith(t, withTriggers(triggers))
	spec := wakeSpec("builder")
	spec.Rate = domain.TriggerRate{Max: 1, PerSeconds: 60}
	createTrigger(asAgent("builder"), t, f, spec)
	triggers.countersFail = true

	publishDisk(t, f)
	publishDisk(t, f)

	if got := len(f.wakes()); got != 1 {
		t.Fatalf("wakes = %d, want 1: the second is over the limit", got)
	}
}

func TestAuditFailureNeverFailsAnAction(t *testing.T) {
	f := newFixtureWith(t, func(d *app.Deps) { d.Ports = core.NewPorts() })

	if _, err := f.svc.RegisterEmitter(asAgent("builder"), hookReg("hook")); !errors.Is(err, core.ErrUnavailable) && err != nil {
		t.Fatalf("RegisterEmitter() error = %v", err)
	}
}

func TestRegisterEmitter_AFailingEntropySourceFails(t *testing.T) {
	f := newFixtureWith(t, func(d *app.Deps) { d.Entropy = strings.NewReader("") })

	if _, err := f.svc.RegisterEmitter(asAgent("builder"), hookReg("hook")); err == nil {
		t.Fatal("RegisterEmitter() succeeded without entropy")
	}
}

func TestViewer_PluginsSeeMachineEventsOnly(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)
	plugin := core.WithCaller(t.Context(), core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD8X3Q5Y6Z7A8B9C0D1E2F60"})

	page, err := f.svc.List(plugin, app.Query{Limit: 10})

	if err != nil || len(page.Events) != 1 || page.Events[0].ChartScope != "machine" {
		t.Fatalf("plugin list = %d events, %v", len(page.Events), err)
	}
}

func TestViewer_RootAgentsSeeEverything(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)

	page, err := f.svc.List(asRootAgent("founder"), app.Query{Limit: 10})

	if err != nil || len(page.Events) != 4 {
		t.Fatalf("root list = %d events, %v", len(page.Events), err)
	}
}

func TestReceiveWebhook_ALongContentTypeIsCut(t *testing.T) {
	f := newFixture(t)
	h := registerHook(t, f)
	r := h.request(`{}`)
	r.ContentType = "application/json; x=" + strings.Repeat("a", 200)

	e, err := f.svc.ReceiveWebhook(t.Context(), r)

	if err != nil || !strings.Contains(string(e.Data), `"content_type":"application/json; x=`) {
		t.Fatalf("event = %s, %v", e.Data, err)
	}
}
