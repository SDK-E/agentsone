// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auditchain"
	httpadapter "github.com/SDK-E/agentsone/internal/edge/audit/adapters/http"
	"github.com/SDK-E/agentsone/internal/edge/audit/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/edge/audit/app"
	"github.com/SDK-E/agentsone/internal/edge/tokenkey"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC) }

// testKey stands for the gateway key the edge shares with the machine.
var testKey = bytes.Repeat([]byte{0x5a}, tokenkey.Size)

// token signs c with key, issued now for five minutes unless c says otherwise.
func token(t *testing.T, key []byte, c gatewaytoken.Claims) string {
	t.Helper()
	if c.IssuedAt == 0 {
		c.IssuedAt = clock{}.Now().Unix()
		c.ExpiresAt = clock{}.Now().Add(5 * time.Minute).Unix()
	}
	tok, err := gatewaytoken.Sign(key, c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// daemonToken is what the daemon's shipper sends: a system caller's token for the audit sink.
func daemonToken(t *testing.T) string {
	t.Helper()
	return token(t, testKey, gatewaytoken.Claims{
		Agent: "system:audit", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceAudit,
	})
}

func inner(s *app.Sink) http.Handler {
	return httpadapter.Inner(s, testKey, clock{}, slog.New(slog.DiscardHandler))
}

// send serves one request to h with the Authorization header auth (none when empty).
func send(t *testing.T, h http.Handler, method, path, auth string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func newSink(t *testing.T) *app.Sink {
	t.Helper()
	st, err := sqlite.Open(t.Context(), t.TempDir()+"/audit.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return app.New(st, clock{}, nil)
}

func chain(t *testing.T, n int) []json.RawMessage {
	t.Helper()
	prev := auditchain.Genesis
	var out []json.RawMessage
	for i := 1; i <= n; i++ {
		raw := fmt.Sprintf(`{"seq":%d,"time":"2026-11-19T21:00:00Z","actor":{"kind":"agent","name":"founder"},`+
			`"action":"agent.hire","target":{"kind":"agent","id":"a%d"},"outcome":"success","details":{},"origin":"inside"}`, i, i)
		sealed, err := auditchain.Seal([]byte(raw), prev)
		if err != nil {
			t.Fatal(err)
		}
		prev, _ = auditchain.Hash(sealed)
		out = append(out, sealed)
	}
	return out
}

// post sends a batch as the daemon does, with its audit token.
func post(t *testing.T, h http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, h, http.MethodPost, "/v1/audit/records", "Bearer "+daemonToken(t), body)
}

func batch(t *testing.T, records []json.RawMessage) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"records": records})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// get reads path with the daemon's audit token.
func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, h, http.MethodGet, path, "Bearer "+daemonToken(t), nil)
}

// Only the daemon holds the key, so only it can mint an audit token. Anything else in the machine
// (a custom agent with its model-call token, a token for another purpose, no token at all) is
// refused on both inner routes, and nothing is stored.
func TestInner_TakesRecordsOnlyWithTheDaemonsAuditToken(t *testing.T) {
	t.Parallel()
	now := clock{}.Now()
	otherKey := bytes.Repeat([]byte{0x33}, tokenkey.Size)
	audit := gatewaytoken.Claims{Agent: "system:audit", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceAudit}
	for name, tc := range map[string]struct {
		auth string
		code int
		typ  string
	}{
		"no token":        {"", http.StatusUnauthorized, httpadapter.ProblemUnauthenticated},
		"not a bearer":    {"Basic " + daemonToken(t), http.StatusUnauthorized, httpadapter.ProblemUnauthenticated},
		"not a token":     {"Bearer forged", http.StatusUnauthorized, httpadapter.ProblemUnauthenticated},
		"another key":     {"Bearer " + token(t, otherKey, audit), http.StatusUnauthorized, httpadapter.ProblemUnauthenticated},
		"expired":         {"Bearer " + token(t, testKey, withWindow(audit, now.Add(-10*time.Minute), now)), http.StatusUnauthorized, httpadapter.ProblemUnauthenticated},
		"not yet valid":   {"Bearer " + token(t, testKey, withWindow(audit, now.Add(5*time.Minute), now.Add(10*time.Minute))), http.StatusUnauthorized, httpadapter.ProblemUnauthenticated},
		"agent model":     {"Bearer " + token(t, testKey, gatewaytoken.Claims{Agent: "dev", Kind: gatewaytoken.KindChat, Chart: "founder/dev", Audience: gatewaytoken.AudienceGateway}), http.StatusForbidden, httpadapter.ProblemForbidden},
		"system model":    {"Bearer " + token(t, testKey, gatewaytoken.Claims{Agent: "system:router", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceGateway}), http.StatusForbidden, httpadapter.ProblemForbidden},
		"agent audit":     {"Bearer " + token(t, testKey, gatewaytoken.Claims{Agent: "dev", Kind: gatewaytoken.KindAlwaysOn, Chart: "founder/dev", Audience: gatewaytoken.AudienceAudit}), http.StatusForbidden, httpadapter.ProblemForbidden},
		"daemon accepted": {"Bearer " + daemonToken(t), http.StatusOK, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := inner(newSink(t))
			head := send(t, h, http.MethodGet, "/v1/audit/head", tc.auth, nil)
			rec := send(t, h, http.MethodPost, "/v1/audit/records", tc.auth, batch(t, chain(t, 1)))
			for route, got := range map[string]*httptest.ResponseRecorder{"GET head": head, "POST records": rec} {
				if got.Code != tc.code {
					t.Errorf("%s = %d %s, want %d", route, got.Code, got.Body, tc.code)
				}
				if tc.typ != "" && !strings.Contains(got.Body.String(), `"type":"`+tc.typ+`"`) {
					t.Errorf("%s problem = %s, want type %s", route, got.Body, tc.typ)
				}
			}
			want := `"seq":0`
			if tc.code == http.StatusOK {
				want = `"seq":1`
			}
			if after := get(t, h, "/v1/audit/head"); !strings.Contains(after.Body.String(), want) {
				t.Errorf("head after = %s, want %s", after.Body, want)
			}
		})
	}
}

func withWindow(c gatewaytoken.Claims, issued, expires time.Time) gatewaytoken.Claims {
	c.IssuedAt, c.ExpiresAt = issued.Unix(), expires.Unix()
	return c
}

func TestInner_TakesABatchAndAnswersTheHead(t *testing.T) {
	t.Parallel()
	h := inner(newSink(t))
	rec := post(t, h, batch(t, chain(t, 2)))
	var got struct {
		Head struct {
			Seq  int64  `json:"seq"`
			Hash string `json:"hash"`
		} `json:"head"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || got.Head.Seq != 2 {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	head := get(t, h, "/v1/audit/head")
	if head.Code != http.StatusOK || !strings.Contains(head.Body.String(), `"seq":2`) {
		t.Fatalf("GET head = %d %s", head.Code, head.Body)
	}
}

func TestInner_AnswersAConflictWithTheHeadOnAGap(t *testing.T) {
	t.Parallel()
	h := inner(newSink(t))
	c := chain(t, 3)
	rec := post(t, h, batch(t, []json.RawMessage{c[0], c[2]}))
	var p struct {
		Type    string `json:"type"`
		Verdict string `json:"verdict"`
		Head    struct {
			Seq int64 `json:"seq"`
		} `json:"head"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict || p.Type != "urn:agentsone:problem:conflict" || p.Verdict != "gap" || p.Head.Seq != 1 {
		t.Fatalf("gap = %d %s", rec.Code, rec.Body)
	}
}

func TestInner_RefusesBadBodies(t *testing.T) {
	t.Parallel()
	h := inner(newSink(t))
	for name, tc := range map[string]struct {
		body []byte
		code int
	}{
		"not json":     {[]byte(`{`), http.StatusBadRequest},
		"no records":   {[]byte(`{"records":[]}`), http.StatusBadRequest},
		"unknown key":  {[]byte(`{"records":[{}],"extra":1}`), http.StatusBadRequest},
		"too many":     {batch(t, make([]json.RawMessage, app.MaxLimit+1)), http.StatusRequestEntityTooLarge},
		"too large":    {bytes.Repeat([]byte(" "), httpadapter.MaxBodyBytes+1), http.StatusRequestEntityTooLarge},
		"trailing doc": {[]byte(`{"records":[{}]}{}`), http.StatusBadRequest},
	} {
		if rec := post(t, h, tc.body); rec.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body, tc.code)
		}
	}
}

func TestInner_DoesNotServeTheAdminRoutes(t *testing.T) {
	t.Parallel()
	h := inner(newSink(t))
	for _, path := range []string{"/v1/audit/inside", "/v1/audit/proof", "/v1/audit/alerts", "/v1/audit/verify"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("machine side GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func admin(t *testing.T, s *app.Sink) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	httpadapter.RegisterAdmin(mux, s, slog.New(slog.DiscardHandler))
	return mux
}

func TestAdmin_ListsBothChainsAlertsAndVerifies(t *testing.T) {
	t.Parallel()
	s := newSink(t)
	if _, err := s.Receive(t.Context(), chain(t, 3)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProof(t.Context(), app.ProofEntry{Action: "freeze.on", Target: "machine:all", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Receive(t.Context(), chain(t, 5)[4:])
	mux := admin(t, s)

	var page struct {
		Records []json.RawMessage `json:"records"`
	}
	rec := get(t, mux, "/v1/audit/inside?after=1&limit=1")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Records) != 1 ||
		!strings.Contains(string(page.Records[0]), `"seq":2`) {
		t.Fatalf("inside page = %d %s", rec.Code, rec.Body)
	}
	rec = get(t, mux, "/v1/audit/proof")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Records) != 1 {
		t.Fatalf("proof page = %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, mux, "/v1/audit/alerts"); !strings.Contains(rec.Body.String(), `"kind":"gap"`) {
		t.Fatalf("alerts = %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, mux, "/v1/audit/verify"); !strings.Contains(rec.Body.String(), `"inside":{"ok":true}`) ||
		!strings.Contains(rec.Body.String(), `"proof":{"ok":true}`) {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, mux, "/v1/audit/head"); rec.Code != http.StatusOK {
		t.Fatalf("head = %d", rec.Code)
	}
}

func TestAdmin_RefusesBadPaging(t *testing.T) {
	t.Parallel()
	mux := admin(t, newSink(t))
	for _, q := range []string{"?after=x", "?limit=-1", "?after=-2", "?limit=nine"} {
		if rec := get(t, mux, "/v1/audit/inside"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("inside%s = %d, want 400", q, rec.Code)
		}
	}
	if rec := get(t, mux, "/v1/audit/alerts?limit=x"); rec.Code != http.StatusBadRequest {
		t.Errorf("alerts?limit=x = %d", rec.Code)
	}
}

func TestAdmin_HidesStoreErrors(t *testing.T) {
	t.Parallel()
	st, err := sqlite.Open(t.Context(), t.TempDir()+"/audit.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	s := app.New(st, clock{}, nil)
	mux := admin(t, s)
	machine := inner(s)
	for _, rec := range []*httptest.ResponseRecorder{
		get(t, mux, "/v1/audit/inside"), get(t, mux, "/v1/audit/alerts"), get(t, mux, "/v1/audit/head"),
		post(t, machine, batch(t, chain(t, 1))), get(t, machine, "/v1/audit/head"),
	} {
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sql") {
			t.Errorf("store down = %d %s, want 500 without detail", rec.Code, rec.Body)
		}
	}
	if rec := get(t, mux, "/v1/audit/verify"); !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Errorf("verify on a down store = %s", rec.Body)
	}
}
