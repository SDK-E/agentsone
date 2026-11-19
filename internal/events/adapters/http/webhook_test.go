// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	httpadapter "github.com/SDK-E/agentsone/internal/events/adapters/http"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// hookCall sends one anonymous webhook request, as the public route does,
// and checks the answer against the contract.
func hookCall(t *testing.T, e *env, id string, body []byte, signature string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(httpadapter.SignatureHeader, signature)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

func TestReceiveWebhook_AcceptsASignedBodyAndPublishesAnEvent(t *testing.T) {
	e := newEnv(t)
	hook := registerWebhook(t, e, "github-push")
	body := []byte(`{"ref":"main"}`)

	rec := hookCall(t, e, hook.Emitter.Id, body, domain.Sign(*hook.WebhookSecret, body))

	accepted := decode[api.WebhookAccepted](t, rec, http.StatusAccepted)
	list := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?type=agentsone.webhooks.received", nil), http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].Id != accepted.EventId || list.Items[0].Subject == nil || *list.Items[0].Subject != "github-push" {
		t.Fatalf("events = %+v, accepted %+v", list.Items, accepted)
	}
	if list.Items[0].Data["body_bytes"] != float64(len(body)) {
		t.Fatalf("event data = %v", list.Items[0].Data)
	}
}

func TestReceiveWebhook_RefusesAWrongOrMissingSecret(t *testing.T) {
	e := newEnv(t)
	hook := registerWebhook(t, e, "github-push")
	body := []byte(`{}`)

	wrong := hookCall(t, e, hook.Emitter.Id, body, domain.Sign("another-secret", body))
	missing := hookCall(t, e, hook.Emitter.Id, body, "")

	if wrong.Code != http.StatusUnauthorized || missing.Code != http.StatusUnauthorized {
		t.Fatalf("wrong = %d, missing = %d, want 401 both", wrong.Code, missing.Code)
	}
	list := decode[api.EventList](t, e.do(t, human, http.MethodGet, "/v1/events?type=agentsone.webhooks.received", nil), http.StatusOK)
	if len(list.Items) != 0 {
		t.Fatalf("a refused webhook published %d events", len(list.Items))
	}
	var rejects int
	for _, a := range e.fakes.Audit.Entries() {
		if a.Action == "webhook.reject" && strings.Contains(string(a.Detail), "192.0.2.1") {
			rejects++
		}
	}
	if rejects != 2 {
		t.Fatalf("audited rejects with the sender address = %d, want 2", rejects)
	}
}

func TestReceiveWebhook_RefusesAnOversizeBody(t *testing.T) {
	e := newEnv(t)
	hook := registerWebhook(t, e, "github-push")
	body := []byte(`{"pad":"` + strings.Repeat("a", app.MaxWebhookBody) + `"}`)

	rec := hookCall(t, e, hook.Emitter.Id, body, domain.Sign(*hook.WebhookSecret, body))

	if got := problemType(t, rec, http.StatusRequestEntityTooLarge); !strings.HasSuffix(got, "payload-too-large") {
		t.Fatalf("problem type = %q", got)
	}
}

func TestReceiveWebhook_RateLimitsASender(t *testing.T) {
	e := newEnv(t)
	hook := registerWebhook(t, e, "github-push")
	body := []byte(`{}`)
	sig := domain.Sign(*hook.WebhookSecret, body)
	for range 60 {
		if rec := hookCall(t, e, hook.Emitter.Id, body, sig); rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
	}

	rec := hookCall(t, e, hook.Emitter.Id, body, sig)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("61st request status = %d, want 429", rec.Code)
	}
	e.clock.Advance(domainWindow)
	if rec := hookCall(t, e, hook.Emitter.Id, body, sig); rec.Code != http.StatusAccepted {
		t.Fatalf("request after the window status = %d, want 202", rec.Code)
	}
}

func TestReceiveWebhook_UnknownAndDisabledHooks(t *testing.T) {
	e := newEnv(t)
	hook := registerWebhook(t, e, "github-push")
	body := []byte(`{}`)
	sig := domain.Sign(*hook.WebhookSecret, body)

	if rec := hookCall(t, e, "emt_missing", body, sig); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown hook status = %d, want 404", rec.Code)
	}
	e.do(t, builder, http.MethodPost, "/v1/emitters/"+hook.Emitter.Id+"/disable", nil)
	if rec := hookCall(t, e, hook.Emitter.Id, body, sig); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled hook status = %d, want 404", rec.Code)
	}
}
