// SPDX-License-Identifier: MIT

package adminapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
)

// seen is one request the fake edge got.
type seen struct {
	method, path, body string
}

// fakeEdge answers every route with a canned body and records the requests.
func fakeEdge(t *testing.T, status int, answer string) (*adminapi.Client, *[]seen) {
	t.Helper()
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, seen{r.Method, r.URL.EscapedPath(), string(body)})
		if status >= 400 {
			w.Header().Set("Content-Type", "application/problem+json")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return adminapi.NewHTTPClient(srv.URL, srv.Client()), &got
}

func TestClient_SendsEachCallToItsRoute(t *testing.T) {
	t.Parallel()
	total, period, on, share := int64(25_000_000), "week", true, 20
	tests := []struct {
		name   string
		status int
		answer string
		call   func(c *adminapi.Client) error
		want   seen
	}{
		{"providers", 200, `{"providers":[]}`, func(c *adminapi.Client) error { _, err := c.Providers(t.Context()); return err },
			seen{"GET", "/v1/gateway/providers", ""}},
		{"add provider", 200, `{"id":"openai"}`, func(c *adminapi.Client) error { _, err := c.AddProvider(t.Context(), "openai"); return err },
			seen{"PUT", "/v1/gateway/providers/openai", ""}},
		{"remove provider", 204, ``, func(c *adminapi.Client) error { return c.RemoveProvider(t.Context(), "openai") },
			seen{"DELETE", "/v1/gateway/providers/openai", ""}},
		{"set key", 200, `{"set":true,"fingerprint":"ab12cd34"}`, func(c *adminapi.Client) error {
			_, err := c.SetKey(t.Context(), "openai", "sk-test")
			return err
		}, seen{"PUT", "/v1/gateway/providers/openai/key", `{"key":"sk-test"}`}},
		{"clear key", 204, ``, func(c *adminapi.Client) error { return c.ClearKey(t.Context(), "openai") },
			seen{"DELETE", "/v1/gateway/providers/openai/key", ""}},
		{"budget", 200, `{}`, func(c *adminapi.Client) error { _, err := c.Budget(t.Context()); return err },
			seen{"GET", "/v1/gateway/budget", ""}},
		{"set budget", 200, `{}`, func(c *adminapi.Client) error {
			_, err := c.SetBudget(t.Context(), adminapi.SetBudgetRequest{TotalMicros: &total, Period: &period})
			return err
		}, seen{"PATCH", "/v1/gateway/budget", `{"total_micros":25000000,"period":"week"}`}},
		{"set defence", 200, `{}`, func(c *adminapi.Client) error {
			_, err := c.SetDefence(t.Context(), adminapi.SetDefenceRequest{Enabled: &on, SharePct: &share})
			return err
		}, seen{"PATCH", "/v1/gateway/budget/defence", `{"enabled":true,"share_pct":20}`}},
		{"allow list", 200, `{"entries":[]}`, func(c *adminapi.Client) error { _, err := c.AllowList(t.Context()); return err },
			seen{"GET", "/v1/net/allow-list", ""}},
		{"add allowed", 201, `{"pattern":"*.example.com","why":"Docs."}`, func(c *adminapi.Client) error {
			_, err := c.AddAllowed(t.Context(), "*.example.com", "Docs.")
			return err
		}, seen{"POST", "/v1/net/allow-list", `{"pattern":"*.example.com","why":"Docs."}`}},
		{"remove allowed", 204, ``, func(c *adminapi.Client) error { return c.RemoveAllowed(t.Context(), "*.example.com") },
			seen{"DELETE", "/v1/net/allow-list/%2A.example.com", ""}},
		{"egress frozen", 200, `{"frozen":true}`, func(c *adminapi.Client) error { _, err := c.EgressFrozen(t.Context()); return err },
			seen{"GET", "/v1/net/freeze", ""}},
	}
	for _, tc := range tests {
		c, got := fakeEdge(t, tc.status, tc.answer)
		if err := tc.call(c); err != nil {
			t.Errorf("%s: error = %v", tc.name, err)
			continue
		}
		if len(*got) != 1 {
			t.Errorf("%s: %d requests, want 1", tc.name, len(*got))
			continue
		}
		g := (*got)[0]
		g.body = strings.TrimSpace(g.body)
		if diff := cmp.Diff(tc.want, g, cmp.AllowUnexported(seen{})); diff != "" {
			t.Errorf("%s: request (-want +got):\n%s", tc.name, diff)
		}
	}
}

func TestClient_DecodesAnswers(t *testing.T) {
	t.Parallel()
	c, _ := fakeEdge(t, 200, `{"currency":"USD","total_micros":5,"period":"day","period_start":"2026-11-19T00:00:00Z",
		"period_end":"2026-11-20T00:00:00Z","defence":{"enabled":true,"share_pct":10}}`)

	got, err := c.Budget(t.Context())
	if err != nil {
		t.Fatalf("Budget() error = %v", err)
	}

	want := adminapi.Budget{Currency: "USD", TotalMicros: 5, Period: adminapi.PeriodDay,
		PeriodStart: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		Defence: adminapi.Defence{Enabled: true, SharePct: 10}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Budget() (-want +got):\n%s", diff)
	}
}

func TestClient_ReturnsTheProblemTheEdgeSent(t *testing.T) {
	t.Parallel()
	c, _ := fakeEdge(t, 404, `{"type":"urn:agentsone:problem:not-found","title":"Not Found","status":404,"detail":"no provider x"}`)

	err := c.RemoveProvider(t.Context(), "x")

	if !adminapi.IsProblem(err, adminapi.SlugNotFound) || !strings.Contains(err.Error(), "no provider x") {
		t.Fatalf("error = %v, want the not-found problem", err)
	}
}

func TestClient_TurnsAnAnswerThatIsNotAProblemIntoOne(t *testing.T) {
	t.Parallel()
	c, _ := fakeEdge(t, 502, "bad gateway")

	_, err := c.Budget(t.Context())

	var p *adminapi.Problem
	if !errors.As(err, &p) || p.Status != 502 || p.Detail != "bad gateway" {
		t.Fatalf("error = %#v", err)
	}
}

func TestClient_ReportsAnAnswerItCannotRead(t *testing.T) {
	t.Parallel()
	c, _ := fakeEdge(t, 200, `{"providers":`)

	if _, err := c.Providers(t.Context()); err == nil {
		t.Fatal("Providers() with a broken answer succeeded")
	}
}

func TestClient_TalksOverTheAdminSocket(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "aa")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "admin.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		adminapi.WriteJSON(w, http.StatusOK, adminapi.EgressFreeze{Frozen: true})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	frozen, err := adminapi.NewClient(path).EgressFrozen(t.Context())

	if err != nil || !frozen {
		t.Fatalf("EgressFrozen() = %v, %v", frozen, err)
	}
}

func TestClient_SaysWhenTheEdgeIsNotThere(t *testing.T) {
	t.Parallel()
	_, err := adminapi.NewClient(filepath.Join(t.TempDir(), "none.sock")).Budget(context.Background())
	if err == nil || !strings.Contains(err.Error(), "is the edge running") {
		t.Fatalf("error = %v", err)
	}
}

func TestFingerprint_IsShortAndStable(t *testing.T) {
	t.Parallel()
	a, b := adminapi.Fingerprint("sk-test-1"), adminapi.Fingerprint("sk-test-2")
	if len(a) != 8 || a == b || a != adminapi.Fingerprint("sk-test-1") || strings.Contains(a, "sk") {
		t.Fatalf("fingerprints %q %q", a, b)
	}
}

func TestDecode_TakesOneKnownObject(t *testing.T) {
	t.Parallel()
	for body, ok := range map[string]bool{
		`{"key":"x"}`:             true,
		`{"key":"x","other":1}`:   false,
		`{"key":"x"} {"key":"y"}`: false,
		`{`:                       false,
		`{"key":"` + strings.Repeat("a", adminapi.MaxRequestBytes) + `"}`: false,
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/", strings.NewReader(body))
		var in adminapi.SetKeyRequest
		p := adminapi.Decode(r, &in)
		if (p == nil) != ok {
			t.Errorf("Decode(%.30q) problem = %v, want ok %v", body, p, ok)
		}
		if p != nil && p.Slug() != adminapi.SlugValidation {
			t.Errorf("Decode(%.30q) slug = %q", body, p.Slug())
		}
	}
}

func TestWriteProblem_WritesAProblemDocument(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()

	adminapi.WriteProblem(rec, adminapi.NewProblem(http.StatusConflict, adminapi.SlugConflict, "already there"))

	var p adminapi.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != 409 || rec.Header().Get("Content-Type") != "application/problem+json" || p.Type != "urn:agentsone:problem:conflict" || p.Error() != "Conflict: already there" {
		t.Fatalf("%d %s %+v", rec.Code, rec.Header().Get("Content-Type"), p)
	}
	if (&adminapi.Problem{Title: "Gone"}).Error() != "Gone" || (&adminapi.Problem{Type: "about:blank"}).Slug() != "about:blank" {
		t.Error("Error() or Slug() of a bare problem")
	}
}
