// SPDX-License-Identifier: MIT

package app_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

type hook struct {
	id     string
	secret string
}

func registerHook(t *testing.T, f *fixture) hook {
	t.Helper()
	got, err := f.svc.RegisterEmitter(asAgent("builder"), hookReg("github-push"))
	if err != nil {
		t.Fatal(err)
	}
	return hook{id: got.Emitter.ID, secret: got.Secret}
}

func (h hook) request(body string) app.Webhook {
	return app.Webhook{
		EmitterID: h.id, Method: http.MethodPost, ContentType: "application/json",
		Body: []byte(body), Signature: domain.Sign(h.secret, []byte(body)), Remote: "203.0.113.9:4711",
	}
}

func TestReceiveWebhook_PublishesAWebhooksReceivedEvent(t *testing.T) {
	f := newFixture(t)
	h := registerHook(t, f)

	e, err := f.svc.ReceiveWebhook(t.Context(), h.request(`{"ref":"main"}`))

	if err != nil {
		t.Fatalf("ReceiveWebhook() error = %v", err)
	}
	if e.Type != "agentsone.webhooks.received" || e.Source != "/emitters/webhooks" || e.Subject != "github-push" || e.Actor != "system:daemon" {
		t.Fatalf("event = %+v", e)
	}
	var data struct {
		Hook      string          `json:"hook"`
		Method    string          `json:"method"`
		SHA       string          `json:"body_sha256"`
		BodyBytes int             `json:"body_bytes"`
		Body      json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Hook != "github-push" || data.Method != "POST" || len(data.SHA) != 64 || data.BodyBytes != 14 || string(data.Body) != `{"ref":"main"}` {
		t.Fatalf("data = %s", e.Data)
	}
	if actions := f.auditActions(); !slices.Contains(actions, "webhook.receive") {
		t.Fatalf("audit actions = %v", actions)
	}
	stored, _ := f.emitters.Get(t.Context(), h.id)
	if stored.LastEventAt == nil {
		t.Fatal("the webhook emitter was not touched")
	}
}

func TestReceiveWebhook_LeavesOutABodyThatIsNotAJSONObject(t *testing.T) {
	f := newFixture(t)
	h := registerHook(t, f)
	r := h.request("plain text")
	r.ContentType = "text/plain"

	e, err := f.svc.ReceiveWebhook(t.Context(), r)

	if err != nil || strings.Contains(string(e.Data), `"body":`) {
		t.Fatalf("event data = %s, %v", e.Data, err)
	}
}

func TestReceiveWebhook_RefusesAWrongOrMissingSignatureAndAuditsTheSender(t *testing.T) {
	f := newFixture(t)
	h := registerHook(t, f)
	wrong := h.request(`{}`)
	wrong.Signature = domain.Sign("another-secret", wrong.Body)
	missing := h.request(`{}`)
	missing.Signature = ""

	for name, r := range map[string]app.Webhook{"wrong": wrong, "missing": missing} {
		if _, err := f.svc.ReceiveWebhook(t.Context(), r); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatalf("%s signature error = %v, want ErrUnauthorized", name, err)
		}
	}

	var rejects int
	for _, a := range f.fakes.Audit.Entries() {
		if a.Action == "webhook.reject" {
			rejects++
			if !strings.Contains(string(a.Detail), "203.0.113.9:4711") {
				t.Fatalf("reject detail %s has no sender address", a.Detail)
			}
		}
	}
	if rejects != 2 {
		t.Fatalf("rejects audited = %d, want 2", rejects)
	}
	if list, _ := f.svc.List(asHuman("h"), app.Query{Limit: 10}); len(list.Events) != 0 {
		t.Fatalf("a refused webhook published %d events", len(list.Events))
	}
}

func TestReceiveWebhook_RefusesAnOversizeBodyAndAuditsTheSender(t *testing.T) {
	f := newFixture(t)
	h := registerHook(t, f)
	atLimit := h.request(strings.Repeat("a", app.MaxWebhookBody))
	over := h.request(strings.Repeat("a", app.MaxWebhookBody+1))

	if _, err := f.svc.ReceiveWebhook(t.Context(), over); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("oversize error = %v, want ErrTooLarge", err)
	}
	if _, err := f.svc.ReceiveWebhook(t.Context(), atLimit); errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("a body at the limit was refused as too large: %v", err)
	}
	var found bool
	for _, a := range f.fakes.Audit.Entries() {
		if a.Action == "webhook.reject" && strings.Contains(string(a.Detail), "203.0.113.9:4711") && strings.Contains(string(a.Detail), "too large") {
			found = true
		}
	}
	if !found {
		t.Fatal("the oversize body was not audited with the sender address")
	}
}

func TestReceiveWebhook_UnknownDisabledAndNonWebhookEmitters(t *testing.T) {
	f := newFixture(t)
	h := registerHook(t, f)
	disk, _ := f.emitters.ByName(t.Context(), "disk")

	if _, err := f.svc.ReceiveWebhook(t.Context(), app.Webhook{EmitterID: "emt_missing", Body: []byte(`{}`)}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown hook error = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.ReceiveWebhook(t.Context(), app.Webhook{EmitterID: disk.ID, Body: []byte(`{}`)}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("built-in emitter as a hook error = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.DisableEmitter(asAgent("builder"), h.id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReceiveWebhook(t.Context(), h.request(`{}`)); !errors.Is(err, domain.ErrEmitterDisabled) {
		t.Fatalf("disabled hook error = %v, want ErrEmitterDisabled", err)
	}
}

func TestReceiveWebhook_RateLimitsEachSenderBeforeCheckingTheSignature(t *testing.T) {
	f := newFixture(t)
	h := registerHook(t, f)
	for range 60 {
		if _, err := f.svc.ReceiveWebhook(t.Context(), h.request(`{}`)); err != nil {
			t.Fatal(err)
		}
	}

	_, err := f.svc.ReceiveWebhook(t.Context(), h.request(`{}`))

	var problem *core.Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusTooManyRequests {
		t.Fatalf("61st request error = %v, want 429", err)
	}
	other := h.request(`{}`)
	other.Remote = "198.51.100.7:1"
	if _, err := f.svc.ReceiveWebhook(t.Context(), other); err != nil {
		t.Fatalf("another sender error = %v, want it allowed", err)
	}
	f.clock.Advance(window)
	if _, err := f.svc.ReceiveWebhook(t.Context(), h.request(`{}`)); err != nil {
		t.Fatalf("request after the window error = %v", err)
	}
}
