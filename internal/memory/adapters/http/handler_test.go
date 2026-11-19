// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/memoryapi"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/memory/adapters/http"
	"github.com/SDK-E/agentsone/internal/memory/adapters/memory"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

type server struct {
	h    http.Handler
	repo *memory.Repository
}

// newServer serves the real handler over the in-memory repository. The test
// names the caller with the X-Test-Caller header: "name" for an agent,
// "human:name" for a human. Absent means nobody signed in.
func newServer(t *testing.T) *server {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "builder"})
	fakes.Agents.Add(core.Agent{Name: "scribe", Manager: "builder"})
	fakes.Agents.Add(core.Agent{Name: "keeper"})
	fakes.Agents.Add(core.Agent{Name: "founder", IsRoot: true})
	repo := memory.New()
	svc := app.New(repo, ports, fakes.Clock, fakes.IDs)

	mux := http.NewServeMux()
	memoryapi.Group(httpadapter.New(svc)).Register(mux, core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
		},
		ResponseError: core.WriteError,
	})
	return &server{h: withCaller(mux), repo: repo}
}

func withCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if who := r.Header.Get("X-Test-Caller"); who != "" {
			c := core.Caller{Kind: core.CallerAgent, Name: who, UID: -1, IsRoot: who == "founder"}
			if name, ok := strings.CutPrefix(who, "human:"); ok {
				c = core.Caller{Kind: core.CallerHuman, Name: name, UID: -1}
			}
			r = r.WithContext(core.WithCaller(r.Context(), c))
		}
		next.ServeHTTP(w, r)
	})
}

// do sends a request and checks the response against the contract.
func (s *server) do(t *testing.T, as, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if as != "" {
		req.Header.Set("X-Test-Caller", as)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

func (s *server) mustStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body)
	}
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p core.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != rec.Code {
		t.Fatalf("problem status %d, response %d", p.Status, rec.Code)
	}
	return p.Type
}

// ---- golden fixtures -------------------------------------------------------

type fixtureRecord struct {
	Items []json.RawMessage `json:"items"`
}

func fixtures(t *testing.T) contracttest.APIFixtures {
	t.Helper()
	fx, err := contracttest.LoadAPIFixtures(filepath.Join("..", "..", "..", "..", "contract", "fixtures", "api"))
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

// seedFromFixture stores the records of the listMemory fixture, so the
// handler's output can be compared with the golden bodies.
func seedFromFixture(t *testing.T, s *server) map[string]json.RawMessage {
	t.Helper()
	var list fixtureRecord
	if err := json.Unmarshal(fixtures(t)["listMemory"][""].Body, &list); err != nil {
		t.Fatal(err)
	}
	byID := map[string]json.RawMessage{}
	for _, raw := range list.Items {
		var w struct {
			ID        string `json:"id"`
			Scope     string `json:"scope"`
			Owner     string `json:"owner"`
			Team      string `json:"team"`
			Title     string `json:"title"`
			Body      string `json:"body"`
			Tags      []string
			Links     []struct{ Kind, Ref, Title string }
			Source    struct{ Kind, Ref string }
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
			Revision  int       `json:"revision"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatal(err)
		}
		rec := domain.Record{
			ID: w.ID, Scope: domain.Scope(w.Scope), Owner: w.Owner, Team: w.Team, Title: w.Title, Body: w.Body,
			Tags: w.Tags, Links: []domain.Link{}, Source: domain.Source{Kind: domain.SourceKind(w.Source.Kind), Ref: w.Source.Ref},
			CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, Revision: w.Revision,
		}
		for _, l := range w.Links {
			rec.Links = append(rec.Links, domain.Link{Kind: l.Kind, Ref: l.Ref, Title: l.Title})
		}
		if err := s.repo.Save(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
		byID[w.ID] = raw
	}
	return byID
}

func sameJSON(t *testing.T, what string, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("%s\n got %s\nwant %s", what, gb, wb)
	}
}

func TestGetMemory_MatchesTheGoldenFixture(t *testing.T) {
	s := newServer(t)
	seedFromFixture(t, s)
	fx := fixtures(t)["getMemory"][""]

	rec := s.do(t, "founder", http.MethodGet, "/v1/memory/mem_01JD7ZK3M00000000000000001", "")

	s.mustStatus(t, rec, fx.Status)
	sameJSON(t, "getMemory body", rec.Body.Bytes(), fx.Body)
}

func TestGetMemory_AnInvisibleRecordIsNotFound(t *testing.T) {
	s := newServer(t)
	seedFromFixture(t, s)

	rec := s.do(t, "builder", http.MethodGet, "/v1/memory/mem_01JD7ZK3M00000000000000005", "") // keeper's private

	s.mustStatus(t, rec, http.StatusNotFound)
	if got := problemType(t, rec); got != core.ProblemTypeBase+"not-found" {
		t.Fatalf("problem type %q", got)
	}
}

func TestListMemory_ReturnsTheFixtureRecordsToARootAgent(t *testing.T) {
	s := newServer(t)
	want := seedFromFixture(t, s)

	rec := s.do(t, "founder", http.MethodGet, "/v1/memory", "")

	s.mustStatus(t, rec, http.StatusOK)
	var got struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Items) != len(want) || got.NextCursor != nil {
		t.Fatalf("body %s, err %v", rec.Body, err)
	}
	for _, raw := range got.Items {
		var id struct{ ID string }
		_ = json.Unmarshal(raw, &id)
		sameJSON(t, "record "+id.ID, raw, want[id.ID])
	}
}

func TestListMemory_HidesWhatTheCallerMayNotRead(t *testing.T) {
	s := newServer(t)
	seedFromFixture(t, s)

	rec := s.do(t, "keeper", http.MethodGet, "/v1/memory", "")

	var got struct{ Items []struct{ ID string } }
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	// keeper reads the two common records and its own private one, not builder's team record.
	if len(got.Items) != 3 {
		t.Fatalf("items %+v", got.Items)
	}
}

func TestListMemory_AnEmptyListHasNoItemsAndNoCursor(t *testing.T) {
	s := newServer(t)
	fx := fixtures(t)["listMemory"]["empty"]

	rec := s.do(t, "scribe", http.MethodGet, "/v1/memory", "")

	s.mustStatus(t, rec, fx.Status)
	sameJSON(t, "empty list", rec.Body.Bytes(), fx.Body)
}

func TestListMemory_PagesWithNextCursor(t *testing.T) {
	s := newServer(t)
	seedFromFixture(t, s)

	first := s.do(t, "founder", http.MethodGet, "/v1/memory?limit=3", "")
	var p1 struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &p1)
	if len(p1.Items) != 3 || p1.NextCursor == nil {
		t.Fatalf("first page %s", first.Body)
	}

	second := s.do(t, "founder", http.MethodGet, "/v1/memory?limit=3&cursor="+*p1.NextCursor, "")
	var p2 struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	_ = json.Unmarshal(second.Body.Bytes(), &p2)
	if len(p2.Items) != 1 || p2.NextCursor != nil {
		t.Fatalf("second page %s", second.Body)
	}
}

func TestListMemory_Refusals(t *testing.T) {
	s := newServer(t)
	tests := []struct {
		name   string
		as     string
		target string
		want   int
	}{
		{"nobody signed in", "", "/v1/memory", http.StatusUnauthorized},
		{"an unknown scope", "scribe", "/v1/memory?scope=world", http.StatusBadRequest},
		{"a bad cursor", "scribe", "/v1/memory?cursor=%21%21", http.StatusBadRequest},
		{"a limit above 200", "scribe", "/v1/memory?limit=201", http.StatusBadRequest},
		{"a limit that is not a number", "scribe", "/v1/memory?limit=many", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s.mustStatus(t, s.do(t, tt.as, http.MethodGet, tt.target, ""), tt.want)
		})
	}
}

func TestWriteMemory_StoresARecordAndReturnsIt(t *testing.T) {
	s := newServer(t)
	fx := fixtures(t)["writeMemory"][""]

	rec := s.do(t, "scribe", http.MethodPost, "/v1/memory",
		`{"scope":"team","title":"Disk clean-up summary","body":"Keeper freed 1.2 GiB.","tags":["disk","report"]}`)

	s.mustStatus(t, rec, fx.Status)
	var got struct {
		ID, Scope, Owner, Team, Title string
		Tags                          []string
		Links                         []any
		Revision                      int
		Source                        struct{ Kind, Ref string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.ID, "mem_") || got.Scope != "team" || got.Owner != "scribe" || got.Team != "builder" ||
		got.Revision != 1 || len(got.Tags) != 2 || got.Links == nil || got.Source.Kind != "agent" || got.Source.Ref != "scribe" {
		t.Fatalf("body %s", rec.Body)
	}
}

func TestWriteMemory_WithoutTagsAnswersAnEmptyList(t *testing.T) {
	s := newServer(t)

	rec := s.do(t, "scribe", http.MethodPost, "/v1/memory", `{"scope":"common","title":"t","body":""}`)

	s.mustStatus(t, rec, http.StatusCreated)
	if !strings.Contains(rec.Body.String(), `"tags":[]`) {
		t.Fatalf("body %s", rec.Body)
	}
}

func TestWriteMemory_Refusals(t *testing.T) {
	s := newServer(t)
	tests := []struct {
		name     string
		as       string
		body     string
		want     int
		wantType string
	}{
		{"nobody signed in", "", `{"scope":"common","title":"t","body":""}`, http.StatusUnauthorized, "unauthorized"},
		{"a human", "human:sam", `{"scope":"common","title":"t","body":""}`, http.StatusForbidden, "forbidden"},
		{"an empty title", "scribe", `{"scope":"common","title":"","body":""}`, http.StatusBadRequest, "invalid"},
		{"an unknown scope", "scribe", `{"scope":"world","title":"t","body":""}`, http.StatusBadRequest, "invalid"},
		{"a body over 64 KiB characters", "scribe", `{"scope":"common","title":"t","body":"` + strings.Repeat("x", 65537) + `"}`, http.StatusRequestEntityTooLarge, "payload-too-large"},
		{"a body that is not json", "scribe", `{"scope":`, http.StatusBadRequest, "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.do(t, tt.as, http.MethodPost, "/v1/memory", tt.body)
			s.mustStatus(t, rec, tt.want)
			if got := problemType(t, rec); got != core.ProblemTypeBase+tt.wantType {
				t.Fatalf("problem type %q, want %q", got, tt.wantType)
			}
		})
	}
}

func TestWriteMemory_AMissingBodyIsBadRequest(t *testing.T) {
	s := newServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/memory", nil)
	req.Header.Set("X-Test-Caller", "scribe")
	rec := httptest.NewRecorder()

	s.h.ServeHTTP(rec, req)

	contracttest.CheckResponse(t, req, rec)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestSearchMemory_ReturnsRankedHitsWithSnippets(t *testing.T) {
	s := newServer(t)
	seedFromFixture(t, s)

	rec := s.do(t, "founder", http.MethodGet, "/v1/memory/search?q=disk", "")

	s.mustStatus(t, rec, http.StatusOK)
	var got struct {
		Items []struct {
			Record  struct{ ID string }
			Score   float64
			Snippet string
		}
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.NextCursor != nil {
		t.Fatalf("body %s", rec.Body)
	}
	for _, h := range got.Items {
		if h.Score <= 0 || h.Snippet == "" {
			t.Fatalf("hit %+v", h)
		}
	}
}

func TestSearchMemory_NoHitsMatchTheEmptyFixture(t *testing.T) {
	s := newServer(t)
	fx := fixtures(t)["searchMemory"]["empty"]

	rec := s.do(t, "scribe", http.MethodGet, "/v1/memory/search?q=zebra", "")

	s.mustStatus(t, rec, fx.Status)
	sameJSON(t, "empty search", rec.Body.Bytes(), fx.Body)
}

func TestSearchMemory_HidesWhatTheCallerMayNotRead(t *testing.T) {
	s := newServer(t)
	seedFromFixture(t, s)

	rec := s.do(t, "builder", http.MethodGet, "/v1/memory/search?q=disk", "") // keeper's private note is hidden

	if strings.Contains(rec.Body.String(), "mem_01JD7ZK3M00000000000000005") {
		t.Fatalf("leaked a private record: %s", rec.Body)
	}
}

func TestSearchMemory_Refusals(t *testing.T) {
	s := newServer(t)
	tests := []struct {
		name   string
		as     string
		target string
		want   int
	}{
		{"nobody signed in", "", "/v1/memory/search?q=x", http.StatusUnauthorized},
		{"no query", "scribe", "/v1/memory/search", http.StatusBadRequest},
		{"a blank query", "scribe", "/v1/memory/search?q=%20%20", http.StatusBadRequest},
		{"a bad cursor", "scribe", "/v1/memory/search?q=x&cursor=%21%21", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s.mustStatus(t, s.do(t, tt.as, http.MethodGet, tt.target, ""), tt.want)
		})
	}
}

func TestSearchMemory_PagesWithNextCursor(t *testing.T) {
	s := newServer(t)
	seedFromFixture(t, s)

	first := s.do(t, "founder", http.MethodGet, "/v1/memory/search?q=disk&limit=1", "")
	var p struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &p)
	if len(p.Items) != 1 || p.NextCursor == nil {
		t.Fatalf("first page %s", first.Body)
	}
	second := s.do(t, "founder", http.MethodGet, "/v1/memory/search?q=disk&limit=1&cursor="+*p.NextCursor, "")
	_ = json.Unmarshal(second.Body.Bytes(), &p)
	if len(p.Items) != 1 || p.NextCursor != nil {
		t.Fatalf("second page %s", second.Body)
	}
}

func TestDeleteMemory_OwnerDeletesAndTheRecordIsGone(t *testing.T) {
	s := newServer(t)
	created := s.do(t, "scribe", http.MethodPost, "/v1/memory", `{"scope":"common","title":"t","body":""}`)
	var rec struct{ ID string }
	_ = json.Unmarshal(created.Body.Bytes(), &rec)

	del := s.do(t, "scribe", http.MethodDelete, "/v1/memory/"+rec.ID, "")

	s.mustStatus(t, del, http.StatusNoContent)
	s.mustStatus(t, s.do(t, "scribe", http.MethodGet, "/v1/memory/"+rec.ID, ""), http.StatusNotFound)
}

func TestDeleteMemory_Refusals(t *testing.T) {
	s := newServer(t)
	created := s.do(t, "scribe", http.MethodPost, "/v1/memory", `{"scope":"common","title":"t","body":""}`)
	var rec struct{ ID string }
	_ = json.Unmarshal(created.Body.Bytes(), &rec)
	tests := []struct {
		name string
		as   string
		id   string
		want int
	}{
		{"nobody signed in", "", rec.ID, http.StatusUnauthorized},
		{"an agent that does not own it", "keeper", rec.ID, http.StatusForbidden},
		{"an unknown id", "scribe", "mem_01JD7ZK3M00000000000000999", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s.mustStatus(t, s.do(t, tt.as, http.MethodDelete, "/v1/memory/"+tt.id, ""), tt.want)
		})
	}
	s.mustStatus(t, s.do(t, "founder", http.MethodDelete, "/v1/memory/"+rec.ID, ""), http.StatusNoContent)
}
