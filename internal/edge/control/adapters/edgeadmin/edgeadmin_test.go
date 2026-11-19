// SPDX-License-Identifier: MIT

package edgeadmin_test

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/adapters/credstore"
	"github.com/SDK-E/agentsone/internal/edge/control/adapters/edgeadmin"
	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

// fakeEdge serves the documented admin routes over a real Unix socket. The credential routes are
// the real ones from credstore.
type fakeEdge struct {
	mu      sync.Mutex
	seen    []string
	bodies  []string
	answers map[string]func(w http.ResponseWriter, body string)
}

func startEdge(t *testing.T, answers map[string]func(w http.ResponseWriter, body string)) (*edgeadmin.Client, *fakeEdge) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao") // short: Unix socket paths are limited to about 100 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "admin.sock")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	fe := &fakeEdge{answers: answers}
	mux := http.NewServeMux()
	credstore.Register(mux, credstore.New(dir), slog.New(slog.DiscardHandler))
	mux.HandleFunc("/v1/net/", fe.handle)
	mux.HandleFunc("/v1/gateway/", fe.handle)
	mux.HandleFunc("/v1/freeze", fe.handle)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return edgeadmin.New(sock, 5*time.Second), fe
}

func (fe *fakeEdge) handle(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.EscapedPath()
	fe.mu.Lock()
	fe.seen = append(fe.seen, key)
	fe.bodies = append(fe.bodies, string(data))
	answer, ok := fe.answers[key]
	fe.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	answer(w, string(data))
}

func (fe *fakeEdge) last() (string, string) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.seen) == 0 {
		return "", ""
	}
	return fe.seen[len(fe.seen)-1], fe.bodies[len(fe.bodies)-1]
}

func reply(status int, body string) func(http.ResponseWriter, string) {
	return func(w http.ResponseWriter, _ string) {
		if strings.HasPrefix(body, `{"type"`) {
			w.Header().Set("Content-Type", "application/problem+json")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

const hash = "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA"

func TestCredential_RoundTripsThroughTheRealEdgeRoutes(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, nil)

	if _, err := c.Get(t.Context()); !errors.Is(err, app.ErrNoCredential) {
		t.Fatalf("Get() before create: error = %v, want %v", err, app.ErrNoCredential)
	}
	if err := c.Create(t.Context(), hash); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got, err := c.Get(t.Context()); err != nil || got != hash {
		t.Fatalf("Get() = %q, %v", got, err)
	}
	if err := c.Create(t.Context(), hash); !errors.Is(err, app.ErrCredentialSet) {
		t.Fatalf("second Create() error = %v, want %v", err, app.ErrCredentialSet)
	}
	if err := c.Reset(t.Context()); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if _, err := c.Get(t.Context()); !errors.Is(err, app.ErrNoCredential) {
		t.Fatalf("Get() after reset: error = %v", err)
	}
}

func TestClient_AnEdgeThatIsNotThereIsUnavailable(t *testing.T) {
	t.Parallel()
	c := edgeadmin.New(filepath.Join(t.TempDir(), "none.sock"), time.Second)

	if _, err := c.Get(t.Context()); !errors.Is(err, app.ErrEdgeUnavailable) {
		t.Fatalf("Get() error = %v, want %v", err, app.ErrEdgeUnavailable)
	}
}

func TestAllowList_ReadsAddsAndRemoves(t *testing.T) {
	t.Parallel()
	c, fe := startEdge(t, map[string]func(http.ResponseWriter, string){
		"GET /v1/net/allow-list":                 reply(200, `{"entries":[{"pattern":"*.example.org","why":"docs"}]}`),
		"POST /v1/net/allow-list":                reply(201, `{"pattern":"example.org","why":"docs"}`),
		"DELETE /v1/net/allow-list/%2A.test.org": reply(204, ``),
	})

	got, err := c.AllowList(t.Context())
	if err != nil || len(got) != 1 || got[0] != (app.AllowEntry{Pattern: "*.example.org", Why: "docs"}) {
		t.Fatalf("AllowList() = %+v, %v", got, err)
	}
	if e, err := c.Allow(t.Context(), "example.org", "docs"); err != nil || e.Pattern != "example.org" {
		t.Fatalf("Allow() = %+v, %v", e, err)
	}
	if _, body := fe.last(); body != `{"pattern":"example.org","why":"docs"}` {
		t.Fatalf("Allow() sent %s", body)
	}
	if err := c.Disallow(t.Context(), "*.test.org"); err != nil {
		t.Fatalf("Disallow() error = %v", err)
	}
}

func TestClient_AnEdgeRefusalCarriesTheEdgeReason(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, map[string]func(http.ResponseWriter, string){
		"POST /v1/net/allow-list": reply(400, `{"type":"urn:agentsone:problem:validation","title":"Bad Request","status":400,"detail":"pattern: not a host name"}`),
	})

	_, err := c.Allow(t.Context(), "bad name", "x")

	var refusal *app.Refusal
	if !errors.As(err, &refusal) || refusal.Detail != "pattern: not a host name" {
		t.Fatalf("Allow() error = %v, want the edge's refusal", err)
	}
}

func TestClient_AnEdgeFailureIsUnavailable(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, map[string]func(http.ResponseWriter, string){
		"GET /v1/freeze": reply(500, `{"type":"urn:agentsone:problem:internal","title":"x","status":500,"detail":"disk"}`),
	})

	if _, err := c.Status(t.Context()); !errors.Is(err, app.ErrEdgeUnavailable) {
		t.Fatalf("Status() error = %v, want %v", err, app.ErrEdgeUnavailable)
	}
}

func TestClient_AMissingRouteIsUnavailable(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, nil)

	if _, err := c.Providers(t.Context()); !errors.Is(err, app.ErrEdgeUnavailable) {
		t.Fatalf("Providers() error = %v, want %v", err, app.ErrEdgeUnavailable)
	}
}

func TestClient_AnUnreadableAnswerIsUnavailable(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, map[string]func(http.ResponseWriter, string){
		"GET /v1/gateway/budget": reply(200, `not json`),
	})

	if _, err := c.Budget(t.Context()); !errors.Is(err, app.ErrEdgeUnavailable) {
		t.Fatalf("Budget() error = %v, want %v", err, app.ErrEdgeUnavailable)
	}
}

func TestProviders_ReadsKeyStateAndModels(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, map[string]func(http.ResponseWriter, string){
		"GET /v1/gateway/providers": reply(200, `{"providers":[{"id":"openai","enabled":true,"key_required":true,"key":{"set":true,"fingerprint":"ab12cd34"},"models":[{"id":"gpt-x","tier":"normal"}]}]}`),
	})

	got, err := c.Providers(t.Context())
	if err != nil {
		t.Fatalf("Providers() error = %v", err)
	}
	want := app.Provider{ID: "openai", Enabled: true, KeyRequired: true, Key: app.KeyState{Set: true, Fingerprint: "ab12cd34"}, Models: []app.Model{{ID: "gpt-x", Tier: "normal"}}}
	if len(got) != 1 || got[0].ID != want.ID || got[0].Key != want.Key || len(got[0].Models) != 1 || got[0].Models[0] != want.Models[0] || !got[0].Enabled || !got[0].KeyRequired {
		t.Fatalf("Providers() = %+v, want %+v", got, want)
	}
}

func TestProviders_EnableDisableAndKeys(t *testing.T) {
	t.Parallel()
	c, fe := startEdge(t, map[string]func(http.ResponseWriter, string){
		"PUT /v1/gateway/providers/openai":        reply(200, `{"id":"openai","enabled":true}`),
		"DELETE /v1/gateway/providers/openai":     reply(204, ``),
		"PUT /v1/gateway/providers/openai/key":    reply(200, `{"set":true,"fingerprint":"ab12cd34"}`),
		"DELETE /v1/gateway/providers/openai/key": reply(204, ``),
	})

	if err := c.Enable(t.Context(), "openai"); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if err := c.Disable(t.Context(), "openai"); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	ks, err := c.SetKey(t.Context(), "openai", "sk-test-123")
	if err != nil || ks != (app.KeyState{Set: true, Fingerprint: "ab12cd34"}) {
		t.Fatalf("SetKey() = %+v, %v", ks, err)
	}
	if _, body := fe.last(); body != `{"key":"sk-test-123"}` {
		t.Fatalf("SetKey() sent %s", body)
	}
	if err := c.ClearKey(t.Context(), "openai"); err != nil {
		t.Fatalf("ClearKey() error = %v", err)
	}
}

func TestSetKey_ErrorNeverHoldsTheKey(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, map[string]func(http.ResponseWriter, string){
		"PUT /v1/gateway/providers/openai/key": reply(500, `{"type":"urn:agentsone:problem:internal","status":500}`),
	})

	_, err := c.SetKey(t.Context(), "openai", "sk-test-SECRET-123")

	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("SetKey() error = %v, want an error without the key", err)
	}
}

func TestBudget_ReadsAndChanges(t *testing.T) {
	t.Parallel()
	answer := `{"currency":"USD","total_micros":25000000,"spent_micros":5000000,"reserved_micros":0,"remaining_micros":20000000,"exhausted":false,"period":"week","period_start":"2026-11-16T00:00:00Z","period_end":"2026-11-23T00:00:00Z","defence":{"enabled":true,"share_pct":20,"reserved_micros":5000000,"remaining_micros":4000000}}`
	c, fe := startEdge(t, map[string]func(http.ResponseWriter, string){
		"GET /v1/gateway/budget":           reply(200, answer),
		"PATCH /v1/gateway/budget":         reply(200, answer),
		"PATCH /v1/gateway/budget/defence": reply(200, answer),
	})

	b, err := c.Budget(t.Context())
	if err != nil || b.TotalMicros != 25_000_000 || b.Period != domain.PeriodWeek || !b.Defence.Enabled || b.Defence.SharePct != 20 || b.PeriodEnd.IsZero() {
		t.Fatalf("Budget() = %+v, %v", b, err)
	}
	total, week := int64(25_000_000), domain.PeriodWeek
	if _, err := c.SetBudget(t.Context(), app.BudgetChange{TotalMicros: &total, Period: &week}); err != nil {
		t.Fatalf("SetBudget() error = %v", err)
	}
	if _, body := fe.last(); body != `{"total_micros":25000000,"period":"week"}` {
		t.Fatalf("SetBudget() sent %s", body)
	}
	on, share := true, 20
	if _, err := c.SetDefence(t.Context(), app.DefenceChange{Enabled: &on, SharePct: &share}); err != nil {
		t.Fatalf("SetDefence() error = %v", err)
	}
	if _, body := fe.last(); body != `{"enabled":true,"share_pct":20}` {
		t.Fatalf("SetDefence() sent %s", body)
	}
}

func TestFreeze_ReadsAndFlipsTheSwitch(t *testing.T) {
	t.Parallel()
	c, fe := startEdge(t, map[string]func(http.ResponseWriter, string){
		"GET /v1/freeze":    reply(200, `{"frozen":true,"since":"2026-11-19T09:00:00Z"}`),
		"POST /v1/freeze":   reply(200, `{"frozen":true}`),
		"DELETE /v1/freeze": reply(200, `{"frozen":false}`),
	})

	s, err := c.Status(t.Context())
	if err != nil || !s.Frozen || !s.Since.Equal(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("Status() = %+v, %v", s, err)
	}
	if err := c.Freeze(t.Context()); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	if got, _ := fe.last(); got != "POST /v1/freeze" {
		t.Fatalf("Freeze() sent %s", got)
	}
	if err := c.Release(t.Context()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if got, _ := fe.last(); got != "DELETE /v1/freeze" {
		t.Fatalf("Release() sent %s", got)
	}
}

func TestFreeze_NeverChangedHasNoSince(t *testing.T) {
	t.Parallel()
	c, _ := startEdge(t, map[string]func(http.ResponseWriter, string){
		"GET /v1/freeze": reply(200, `{"frozen":false}`),
	})
	s, err := c.Status(t.Context())
	if err != nil || s.Frozen || !s.Since.IsZero() {
		t.Fatalf("Status() = %+v, %v", s, err)
	}
}
