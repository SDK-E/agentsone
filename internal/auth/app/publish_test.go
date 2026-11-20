// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/adapters/memory"
	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// callerEvents records who publishes each event, as the events module
// takes the actor from the context's caller.
type callerEvents struct {
	mu      sync.Mutex
	callers map[string]core.Caller
	err     error
}

func (e *callerEvents) Publish(ctx context.Context, ev core.Event) (core.Event, error) {
	c, _ := core.CallerFrom(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.callers[ev.Type] = c
	return ev, e.err
}

func newPublishService(t *testing.T, events core.EventPublisher, log *slog.Logger) *app.Service {
	t.Helper()
	clock := coretest.NewClock(coretest.Epoch)
	store := memory.New()
	svc, err := app.New(app.Deps{
		Clock: clock, Rand: &authtest.Counter{}, Argon2: authtest.Cheap, Sessions: store, Tokens: store, Humans: store, Setup: store,
		Licence: authtest.DefaultLicence, Events: events, Audit: coretest.NewAudit(), Seed: coretest.NewSeed(), Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// An event that cannot be published is logged as an error with its type
// and the reason; the human is still created.
func TestCreateFirstHuman_LogsAnEventThatCannotBePublished(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	events := &callerEvents{callers: map[string]core.Caller{}, err: errors.New("bus.db is read-only")}
	svc := newPublishService(t, events, slog.New(slog.NewTextHandler(&logs, nil)))

	if _, err := svc.CreateFirstHuman(context.Background(), app.NewHuman{Name: "ada", Password: password}); err != nil {
		t.Fatalf("first human not created: %v", err)
	}

	got := logs.String()
	for _, want := range []string{"level=ERROR", "event not published", "agentsone.setup.first-human.created", "bus.db is read-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q lacks %q", got, want)
		}
	}
}

// The first human is created by a visitor nobody knows yet (the anonymous
// caller), whom the events module refuses as an actor. The daemon publishes
// the event itself, so it is stored and the society seeds itself.
func TestCreateFirstHuman_PublishesAsTheDaemonNotTheVisitor(t *testing.T) {
	t.Parallel()
	events := &callerEvents{callers: map[string]core.Caller{}}
	svc := newPublishService(t, events, nil)
	visitor := core.WithCaller(context.Background(), core.Anonymous)

	if _, err := svc.CreateFirstHuman(visitor, app.NewHuman{Name: "ada", Password: password}); err != nil {
		t.Fatal(err)
	}

	got := events.callers["agentsone.setup.first-human.created"]
	if got.Kind != core.CallerSystem || got.Name != "auth" {
		t.Fatalf("published as %+v, want system:auth", got)
	}
}
