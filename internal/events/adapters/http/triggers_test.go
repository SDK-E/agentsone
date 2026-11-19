// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
)

func wakeBody(agent string) obj {
	return obj{
		"name":      "disk alarm",
		"filter":    obj{"types": []string{"agentsone.disk.*"}},
		"action":    obj{"kind": "wake", "agent": agent, "message": "Disk: {{type}}"},
		"wake_rate": obj{"max": 2, "per_seconds": 60},
	}
}

func createTrigger(t *testing.T, e *env, body obj) api.Trigger {
	t.Helper()
	return decode[api.Trigger](t, e.do(t, builder, http.MethodPost, "/v1/triggers", body), http.StatusCreated)
}

func TestCreateTrigger_WakeAndCreateTaskActions(t *testing.T) {
	e := newEnv(t)

	wake := createTrigger(t, e, wakeBody("builder"))
	task := createTrigger(t, e, obj{
		"name": "clean up", "enabled": false,
		"filter":    obj{"types": []string{"agentsone.disk.*"}, "sources": []string{"/emitters/disk"}, "subject_prefix": "/var", "actors": []string{"system:daemon"}},
		"action":    obj{"kind": "create-task", "assignee": "builder", "title": "Clean {{subject}}", "priority": "high"},
		"wake_rate": obj{"max": 1, "per_seconds": 600},
	})

	if !wake.Enabled || wake.Owner != "builder" || wake.WakeRate.Max != 2 {
		t.Fatalf("wake trigger = %+v", wake)
	}
	a, err := wake.Action.AsWakeAction()
	if err != nil || a.Agent != "builder" || a.Message != "Disk: {{type}}" {
		t.Fatalf("wake action = %+v, %v", a, err)
	}
	c, err := task.Action.AsCreateTaskAction()
	if err != nil || task.Enabled || c.Title != "Clean {{subject}}" || c.Priority != api.TaskPriorityHigh || c.Assignee != "builder" {
		t.Fatalf("create-task trigger = %+v, action %+v, %v", task, c, err)
	}
	if task.Filter.Sources == nil || task.Filter.SubjectPrefix == nil || task.Filter.Actors == nil {
		t.Fatalf("filter = %+v", task.Filter)
	}
}

func TestCreateTrigger_Refusals(t *testing.T) {
	e := newEnv(t)
	noFilter := wakeBody("builder")
	noFilter["filter"] = obj{}
	foreign := wakeBody("founder")
	ghost := wakeBody("ghost")
	badKind := wakeBody("builder")
	badKind["action"] = obj{"kind": "explode"}

	cases := map[string]struct {
		body   obj
		status int
	}{
		"empty filter":      {noFilter, http.StatusBadRequest},
		"unknown agent":     {ghost, http.StatusBadRequest},
		"unknown kind":      {badKind, http.StatusBadRequest},
		"above the creator": {foreign, http.StatusForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := e.do(t, builder, http.MethodPost, "/v1/triggers", c.body); rec.Code != c.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}

func TestTriggers_GetListUpdateDelete(t *testing.T) {
	e := newEnv(t)
	one := createTrigger(t, e, wakeBody("builder"))
	two := createTrigger(t, e, wakeBody("builder"))

	got := decode[api.Trigger](t, e.do(t, human, http.MethodGet, "/v1/triggers/"+one.Id, nil), http.StatusOK)
	if got.Id != one.Id {
		t.Fatalf("got = %+v", got)
	}
	all := decode[api.TriggerList](t, e.do(t, human, http.MethodGet, "/v1/triggers", nil), http.StatusOK)
	if len(all.Items) != 2 || all.NextCursor != nil {
		t.Fatalf("list = %d, cursor %v", len(all.Items), all.NextCursor)
	}
	page := decode[api.TriggerList](t, e.do(t, human, http.MethodGet, "/v1/triggers?limit=1&owner=builder", nil), http.StatusOK)
	if len(page.Items) != 1 || page.NextCursor == nil || *page.NextCursor != one.Id {
		t.Fatalf("page = %d, cursor %v", len(page.Items), page.NextCursor)
	}
	none := decode[api.TriggerList](t, e.do(t, human, http.MethodGet, "/v1/triggers?owner=founder", nil), http.StatusOK)
	if len(none.Items) != 0 {
		t.Fatalf("owner filter = %d, want 0", len(none.Items))
	}

	patched := decode[api.Trigger](t, e.do(t, builder, http.MethodPatch, "/v1/triggers/"+one.Id, obj{
		"name": "renamed", "enabled": false,
		"filter":    obj{"types": []string{"agentsone.load.*"}},
		"action":    obj{"kind": "create-task", "assignee": "builder", "title": "Look", "priority": "low"},
		"wake_rate": obj{"max": 5, "per_seconds": 30},
	}), http.StatusOK)
	if patched.Name != "renamed" || patched.Enabled || patched.WakeRate.Max != 5 || (*patched.Filter.Types)[0] != "agentsone.load.*" {
		t.Fatalf("patched = %+v", patched)
	}
	if rec := e.do(t, builder, http.MethodPatch, "/v1/triggers/"+one.Id, obj{"action": obj{"kind": "explode"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("patch with a bad action status = %d, want 400", rec.Code)
	}

	if rec := e.do(t, builder, http.MethodDelete, "/v1/triggers/"+two.Id, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.Code)
	}
	if rec := e.do(t, builder, http.MethodGet, "/v1/triggers/"+two.Id, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", rec.Code)
	}
}

func TestTrigger_FiresWhenAMatchingEventIsPublished(t *testing.T) {
	e := newEnv(t)
	createTrigger(t, e, wakeBody("builder"))

	e.do(t, founder, http.MethodPost, "/v1/events", obj{
		"type": "agentsone.disk.usage.high", "subject": "/var",
		"data": obj{"mount": "/var", "used_percent": 91, "threshold_percent": 90, "free_bytes": 1000},
	})

	list := decode[api.TriggerList](t, e.do(t, human, http.MethodGet, "/v1/triggers", nil), http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].FiredCount != 1 || list.Items[0].LastFiredAt == nil {
		t.Fatalf("triggers after a matching event = %+v", list.Items)
	}
	if got := len(e.fakes.Waker.Wakes()); got != 1 {
		t.Fatalf("wakes = %d, want 1", got)
	}
}
