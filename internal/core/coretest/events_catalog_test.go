// SPDX-License-Identifier: MIT

package coretest_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// The fake bus refuses what the events module refuses when a daemon module
// publishes, so a module test that publishes through it proves the event
// would be stored: the caller can be named as the actor, the envelope is one
// the event schema allows, the type is in the contract event catalog, the
// chart scope is the kind the catalog gives it, and the data matches the
// type's schema.
func TestEventsPublishRefusesWhatTheEventsModuleRefuses(t *testing.T) {
	t.Parallel()
	hired := func(data string) core.Event {
		return core.Event{
			Type: "agentsone.agents.hired", Source: "/daemon", Data: json.RawMessage(data),
			Actor: "system:small-seed", ChartScope: "team:founder",
		}
	}
	valid := `{"agent":"founder","manager":"founder","kind":"always-on","is_root":true,"template":"small-seed/founder"}`
	daemon := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "small-seed", UID: -1, IsRoot: true})
	tests := []struct {
		name string
		ctx  context.Context
		e    func() core.Event
	}{
		{"data key the schema does not name", daemon, func() core.Event {
			return hired(`{"agent":"founder","manager":"founder","kind":"always-on","isRoot":true,"template":"small-seed/founder"}`)
		}},
		{"data without a required key", daemon, func() core.Event {
			return hired(`{"agent":"founder","manager":"founder","kind":"always-on","template":"small-seed/founder"}`)
		}},
		{"data of the wrong type", daemon, func() core.Event {
			return hired(`{"agent":"founder","manager":"founder","kind":"always-on","is_root":"yes","template":"small-seed/founder"}`)
		}},
		{"no data", daemon, func() core.Event { e := hired(valid); e.Data = nil; return e }},
		{"type outside the catalog", daemon, func() core.Event { e := hired(valid); e.Type = "agentsone.agents.promoted"; return e }},
		{"chart scope of another kind", daemon, func() core.Event { e := hired(valid); e.ChartScope = "agent:founder"; return e }},
		{"source the event schema refuses", daemon, func() core.Event { e := hired(valid); e.Source = "/auth"; return e }},
		{"caller with no usable name", core.WithCaller(context.Background(), core.Anonymous), func() core.Event { return hired(valid) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := coretest.NewEvents(coretest.NewClock(start), coretest.NewIDs())
			if _, err := ev.Publish(tt.ctx, tt.e()); !errors.Is(err, core.ErrInvalid) {
				t.Errorf("publish: %v, want core.ErrInvalid", err)
			}
			if got := ev.Published(); len(got) != 0 {
				t.Errorf("stored %d refused events", len(got))
			}
		})
	}

	ev := coretest.NewEvents(coretest.NewClock(start), coretest.NewIDs())
	if _, err := ev.Publish(daemon, hired(valid)); err != nil {
		t.Fatalf("valid event refused: %v", err)
	}
}
