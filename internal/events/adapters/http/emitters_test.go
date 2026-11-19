// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
)

func registerWebhook(t *testing.T, e *env, name string) api.EmitterRegistered {
	t.Helper()
	rec := e.do(t, builder, http.MethodPost, "/v1/emitters", obj{
		"name": name, "kind": "webhook", "description": "A hook.", "event_types": []string{"agentsone." + name + ".*"},
	})
	return decode[api.EmitterRegistered](t, rec, http.StatusCreated)
}

func TestListEmitters_ShowsTheNineBuiltinsAndPages(t *testing.T) {
	e := newEnv(t)

	all := decode[api.EmitterList](t, e.do(t, human, http.MethodGet, "/v1/emitters", nil), http.StatusOK)
	if len(all.Items) != 9 || all.NextCursor != nil {
		t.Fatalf("list = %d emitters, cursor %v", len(all.Items), all.NextCursor)
	}
	page := decode[api.EmitterList](t, e.do(t, human, http.MethodGet, "/v1/emitters?limit=4", nil), http.StatusOK)
	if len(page.Items) != 4 || page.NextCursor == nil || *page.NextCursor != page.Items[3].Id {
		t.Fatalf("first page = %d, cursor %v", len(page.Items), page.NextCursor)
	}
	rest := decode[api.EmitterList](t, e.do(t, human, http.MethodGet, "/v1/emitters?limit=50&cursor="+*page.NextCursor, nil), http.StatusOK)
	if len(rest.Items) != 5 {
		t.Fatalf("rest = %d, want 5", len(rest.Items))
	}
}

func TestRegisterEmitter_AWebhookShowsItsSecretOnce(t *testing.T) {
	e := newEnv(t)

	got := registerWebhook(t, e, "github-push")

	if got.WebhookSecret == nil || *got.WebhookSecret == "" || got.Emitter.WebhookPath == nil ||
		*got.Emitter.WebhookPath != "/v1/webhooks/"+got.Emitter.Id || got.Emitter.Owner != "agent:builder" {
		t.Fatalf("registered = %+v", got)
	}
	fetched := decode[api.Emitter](t, e.do(t, human, http.MethodGet, "/v1/emitters/"+got.Emitter.Id, nil), http.StatusOK)
	if fetched.Name != "github-push" || fetched.Unit != nil {
		t.Fatalf("fetched = %+v", fetched)
	}
}

func TestRegisterEmitter_Refusals(t *testing.T) {
	e := newEnv(t)
	process := obj{"name": "watcher", "kind": "process", "description": "Runs.", "event_types": []string{"agentsone.watcher.*"}, "command": []string{"/bin/true"}}
	registerWebhook(t, e, "hook")

	if rec := e.do(t, builder, http.MethodPost, "/v1/emitters", process); rec.Code != http.StatusForbidden {
		t.Fatalf("agent process emitter status = %d, want 403", rec.Code)
	}
	if rec := e.do(t, founder, http.MethodPost, "/v1/emitters", process); rec.Code != http.StatusBadRequest {
		t.Fatalf("process emitter without systemd status = %d, want 400: %s", rec.Code, rec.Body)
	}
	dup := obj{"name": "hook", "kind": "webhook", "description": "Again.", "event_types": []string{"agentsone.hook.*"}}
	if rec := e.do(t, builder, http.MethodPost, "/v1/emitters", dup); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d, want 409", rec.Code)
	}
	if rec := e.do(t, human, http.MethodGet, "/v1/emitters/emt_missing", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown emitter status = %d, want 404", rec.Code)
	}
}

func TestEnableAndDisableEmitter(t *testing.T) {
	e := newEnv(t)
	hook := registerWebhook(t, e, "hook")
	base := "/v1/emitters/" + hook.Emitter.Id

	off := decode[api.Emitter](t, e.do(t, builder, http.MethodPost, base+"/disable", nil), http.StatusOK)
	on := decode[api.Emitter](t, e.do(t, builder, http.MethodPost, base+"/enable", nil), http.StatusOK)

	if off.Enabled || !on.Enabled {
		t.Fatalf("disable = %v, enable = %v", off.Enabled, on.Enabled)
	}
	if rec := e.do(t, founder, http.MethodPost, "/v1/emitters/"+hook.Emitter.Id+"/disable", nil); rec.Code != http.StatusOK {
		t.Fatalf("privileged disable status = %d, want 200", rec.Code)
	}
}
