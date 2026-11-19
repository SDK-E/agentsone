// SPDX-License-Identifier: MIT

package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/events"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := events.New(deps)
	if m.Name() != "events" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "events", "triggers")
}

// boot runs the lifecycle steps the daemon runs, up to Start.
func boot(t *testing.T) (*events.Module, core.Deps) {
	t.Helper()
	deps, _ := coretest.NewDeps(t)
	m := events.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return m, deps
}

func TestModule_SeedsTheBuiltinEmittersAndServesThem(t *testing.T) {
	m, _ := boot(t)
	if err := m.Start(t.Context()); err != nil {
		t.Fatalf("second Start() error = %v, want it to be safe to run again", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/emitters", nil)
	rec := httptest.NewRecorder()

	moduletest.Mount(m).ServeHTTP(rec, req)

	var list api.EmitterList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list.Items) != 9 {
		t.Fatalf("status %d, %d emitters, %v: %s", rec.Code, len(list.Items), err, rec.Body)
	}
}

func TestModule_ProvidesThePublishAndSubscribePorts(t *testing.T) {
	_, deps := boot(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	live, err := deps.Ports.Subscribe.Subscribe(ctx, core.EventFilter{Types: []string{"agentsone.custom."}})
	if err != nil {
		t.Fatal(err)
	}

	stored, err := deps.Ports.Events.Publish(ctx, core.Event{
		Type: "agentsone.custom.thing", Source: "/daemon", Data: json.RawMessage(`{"n":1}`),
	})

	if err != nil || stored.Sequence == "" || stored.Actor != "system:daemon" {
		t.Fatalf("Publish() = %+v, %v", stored, err)
	}
	if got := <-live; got.ID != stored.ID {
		t.Fatalf("subscriber got %s, want %s", got.ID, stored.ID)
	}
}
