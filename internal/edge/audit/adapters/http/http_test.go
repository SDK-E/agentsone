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
)

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC) }

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

func post(t *testing.T, h http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/audit/records", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func batch(t *testing.T, records []json.RawMessage) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"records": records})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

func TestInner_TakesABatchAndAnswersTheHead(t *testing.T) {
	t.Parallel()
	h := httpadapter.Inner(newSink(t), slog.New(slog.DiscardHandler))
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
	h := httpadapter.Inner(newSink(t), slog.New(slog.DiscardHandler))
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
	h := httpadapter.Inner(newSink(t), slog.New(slog.DiscardHandler))
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
	h := httpadapter.Inner(newSink(t), slog.New(slog.DiscardHandler))
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
	inner := httpadapter.Inner(s, slog.New(slog.DiscardHandler))
	for _, rec := range []*httptest.ResponseRecorder{
		get(t, mux, "/v1/audit/inside"), get(t, mux, "/v1/audit/alerts"), get(t, mux, "/v1/audit/head"),
		post(t, inner, batch(t, chain(t, 1))), get(t, inner, "/v1/audit/head"),
	} {
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sql") {
			t.Errorf("store down = %d %s, want 500 without detail", rec.Code, rec.Body)
		}
	}
	if rec := get(t, mux, "/v1/audit/verify"); !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Errorf("verify on a down store = %s", rec.Body)
	}
}
