// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

func publish(t *testing.T, e *env, typ string, data obj) api.Event {
	t.Helper()
	rec := e.do(t, human, http.MethodPost, "/v1/events", obj{"type": typ, "subject": "/var", "data": data})
	return decode[api.Event](t, rec, http.StatusCreated)
}

func TestPublishEvent_StoresAnEventWithTheCallerAsActor(t *testing.T) {
	e := newEnv(t)

	got := publish(t, e, "agentsone.custom.thing", obj{"n": 1})

	if got.Actor != "human:hsaddek" || got.Source != "/daemon" || got.Type != "agentsone.custom.thing" {
		t.Fatalf("event = %+v", got)
	}
	if got.Sequence == "" || len(got.Sequence) != 20 || got.Specversion != "1.0" || got.Chartscope != "machine" {
		t.Fatalf("event envelope = %+v", got)
	}
	if got.Subject == nil || *got.Subject != "/var" {
		t.Fatalf("subject = %v", got.Subject)
	}
}

func TestPublishEvent_TheActorComesFromTheCallerNotTheBody(t *testing.T) {
	e := newEnv(t)

	rec := e.do(t, builder, http.MethodPost, "/v1/events",
		obj{"type": "agentsone.custom.thing", "data": obj{}, "actor": "agent:founder"})

	got := decode[api.Event](t, rec, http.StatusCreated)
	if got.Actor != "agent:builder" || got.Source != "/agents/builder" {
		t.Fatalf("event = %+v, want the caller as actor", got)
	}
}

// The system actor is set only inside the daemon. No agent, the root agent
// included, publishes as the system by naming it in the body or a header.
func TestPublishEvent_AnAgentNeverPublishesAsTheSystem(t *testing.T) {
	for _, c := range []core.Caller{builder, founder} {
		for _, actor := range []string{"system:auth", "system:daemon", "system"} {
			t.Run(c.Name+"/"+actor, func(t *testing.T) {
				e := newEnv(t)
				raw := `{"type":"agentsone.custom.thing","data":{},"actor":"` + actor + `"}`
				req := httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Auth-Actor", actor)
				req.Header.Set("X-Remote-User", actor)
				rec := httptest.NewRecorder()

				e.as(c).ServeHTTP(rec, req)

				got := decode[api.Event](t, rec, http.StatusCreated)
				if got.Actor != "agent:"+c.Name {
					t.Fatalf("actor = %q, want agent:%s", got.Actor, c.Name)
				}
			})
		}
	}
}

func TestPublishEvent_Refusals(t *testing.T) {
	e := newEnv(t)
	cases := map[string]struct {
		caller core.Caller
		body   obj
		status int
	}{
		"bad catalog data":  {founder, obj{"type": "agentsone.disk.usage.high", "data": obj{"mount": 5}}, http.StatusBadRequest},
		"bad type":          {human, obj{"type": "nope", "data": obj{}}, http.StatusBadRequest},
		"too much data":     {human, obj{"type": "agentsone.custom.big", "data": obj{"x": strings.Repeat("a", 70<<10)}}, http.StatusRequestEntityTooLarge},
		"foreign source":    {builder, obj{"type": "agentsone.custom.thing", "source": "/agents/founder", "data": obj{}}, http.StatusForbidden},
		"built-in by agent": {builder, obj{"type": "agentsone.disk.usage.high", "data": obj{"mount": "/"}}, http.StatusForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := e.do(t, c.caller, http.MethodPost, "/v1/events", c.body)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}

func TestListEvents_FiltersAndPages(t *testing.T) {
	e := newEnv(t)
	first := publish(t, e, "agentsone.custom.a", obj{})
	publish(t, e, "agentsone.custom.b", obj{})
	publish(t, e, "agentsone.custom.a", obj{})

	all := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events", nil), http.StatusOK)
	if len(all.Items) != 3 || all.NextCursor != nil {
		t.Fatalf("list = %d items, cursor %v", len(all.Items), all.NextCursor)
	}
	byType := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?type=agentsone.custom.a", nil), http.StatusOK)
	if len(byType.Items) != 2 {
		t.Fatalf("by type = %d, want 2", len(byType.Items))
	}
	byWildcard := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?type=agentsone.custom.*&actor=human:hsaddek&subject=/v&source=/daemon", nil), http.StatusOK)
	if len(byWildcard.Items) != 3 {
		t.Fatalf("by wildcard and actor = %d, want 3", len(byWildcard.Items))
	}
	after := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?after_sequence=1", nil), http.StatusOK)
	if len(after.Items) != 2 {
		t.Fatalf("after_sequence = %d, want 2", len(after.Items))
	}
	window := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?since=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z", nil), http.StatusOK)
	if len(window.Items) != 3 {
		t.Fatalf("time window = %d, want 3", len(window.Items))
	}

	page := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?limit=2", nil), http.StatusOK)
	if len(page.Items) != 2 || page.NextCursor == nil || page.Items[0].Id != first.Id {
		t.Fatalf("first page = %d items, cursor %v", len(page.Items), page.NextCursor)
	}
	rest := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?limit=2&cursor="+*page.NextCursor, nil), http.StatusOK)
	if len(rest.Items) != 1 || rest.NextCursor != nil {
		t.Fatalf("second page = %d items, cursor %v", len(rest.Items), rest.NextCursor)
	}
}

func TestListEvents_RefusesABadCursorOrNegativeSequence(t *testing.T) {
	e := newEnv(t)

	for _, target := range []string{"/v1/events?cursor=abc", "/v1/events?after_sequence=-1"} {
		if rec := e.do(t, human, http.MethodGet, target, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", target, rec.Code)
		}
	}
}

func TestListEvents_AnAgentSeesOnlyWhatItsPlaceInTheChartAllows(t *testing.T) {
	e := newEnv(t)
	publish(t, e, "agentsone.custom.a", obj{})

	got := decode[api.EventList](t, e.do(t, builder, http.MethodGet, "/v1/events", nil), http.StatusOK)

	if len(got.Items) != 1 || got.Items[0].Chartscope != "machine" {
		t.Fatalf("builder list = %+v", got.Items)
	}
}
