// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/SDK-E/agentsone/internal/agents/adapters/http"
	"github.com/SDK-E/agentsone/internal/agents/adapters/memory"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// callerHeader carries the test caller as "<kind>:<name>"; the real API
// layer sets the caller from peer credentials or a session instead.
const callerHeader = "X-Test-Caller"

type server struct {
	t       *testing.T
	h       http.Handler
	society *app.Society
	fakes   *coretest.Fakes
	ticks   chan time.Time
}

func newServer(t *testing.T) *server {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	repo := memory.New()
	for _, a := range agentstest.Society() {
		if err := repo.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	society := app.New(repo, agentstest.NewRuntime(), ports, fakes.Clock, fakes.IDs)
	ticks := make(chan time.Time, 1)
	h := httpadapter.New(society, fakes.Live, httpadapter.WithTicker(func() (<-chan time.Time, func()) { return ticks, func() {} }))
	mux := http.NewServeMux()
	opts := core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
		},
		ResponseError: core.WriteError,
	}
	for _, g := range h.Groups() {
		g.Register(mux, opts)
	}
	withCaller := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.Header.Get(callerHeader); c != "" {
			kind, name, _ := strings.Cut(c, ":")
			r = r.WithContext(core.WithCaller(r.Context(), core.Caller{Kind: core.CallerKind(kind), Name: name, UID: -1}))
		}
		mux.ServeHTTP(w, r)
	})
	return &server{t: t, h: withCaller, society: society, fakes: fakes, ticks: ticks}
}

// do sends one request as caller and checks the response against the
// contract.
func (s *server) do(caller, method, target, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if caller != "" {
		req.Header.Set(callerHeader, caller)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	contracttest.CheckResponse(s.t, req, rec)
	return rec
}

func ctxAs(kind core.CallerKind, name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: kind, Name: name, UID: -1})
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, slug string) {
	t.Helper()
	wantStatus(t, rec, status)
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p core.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != core.ProblemTypeBase+slug || p.Status != status {
		t.Fatalf("problem type %q status %d, want %s %d", p.Type, p.Status, slug, status)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

type agentDTO struct {
	Name    string  `json:"name"`
	Team    string  `json:"team"`
	Manager *string `json:"manager"`
	Unit    string  `json:"unit"`
	Limits  *struct {
		Wall string `json:"wall"`
	} `json:"limits"`
	OneLine *struct {
		Text string `json:"text"`
	} `json:"one_line"`
	State string `json:"state"`
}

type page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func names(items []agentDTO) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.Name)
	}
	return out
}

func TestListAgents_PagesByName(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	first := decode[page[agentDTO]](t, s.do("human:ada", http.MethodGet, "/v1/agents?limit=4", ""))
	if got := strings.Join(names(first.Items), ","); got != "dev,founder,intern,lead" || first.NextCursor == nil {
		t.Fatalf("first page %s, cursor %v", got, first.NextCursor)
	}
	rest := decode[page[agentDTO]](t, s.do("human:ada", http.MethodGet, "/v1/agents?cursor="+*first.NextCursor, ""))
	if got := strings.Join(names(rest.Items), ","); got != "ops,watcher" || rest.NextCursor != nil {
		t.Fatalf("second page %s, cursor %v", got, rest.NextCursor)
	}
}

func TestListAgents_FiltersByKindAndTeam(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	wakers := decode[page[agentDTO]](t, s.do("human:ada", http.MethodGet, "/v1/agents?kind=waker", ""))
	team := decode[page[agentDTO]](t, s.do("human:ada", http.MethodGet, "/v1/agents?team=lead&state=sleeping&is_root=false&manager=lead", ""))

	if got := strings.Join(names(wakers.Items), ","); got != "watcher" {
		t.Errorf("wakers %s, want watcher", got)
	}
	if got := strings.Join(names(team.Items), ","); got != "dev" {
		t.Errorf("team lead reports of lead %s, want dev", got)
	}
}

func TestGetAgent_ShowsTeamUnitLimitsAndOneLine(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if err := s.fakes.Live.UpdateLine(context.Background(), "dev", "Goal: ship. Now: testing."); err != nil {
		t.Fatal(err)
	}

	dev := decode[agentDTO](t, s.do("human:ada", http.MethodGet, "/v1/agents/dev", ""))
	founder := decode[agentDTO](t, s.do("human:ada", http.MethodGet, "/v1/agents/founder", ""))

	if dev.Team != "lead" || dev.Unit != "agent@dev.service" || dev.Limits == nil || dev.Limits.Wall != "soft" || dev.OneLine == nil || dev.OneLine.Text != "Goal: ship. Now: testing." {
		t.Errorf("dev = %+v", dev)
	}
	if founder.Manager != nil || founder.Limits != nil || founder.OneLine != nil || founder.Team != "founder" {
		t.Errorf("founder = %+v, want no manager, no limits, no one-line", founder)
	}
}

func TestGetAgent_ReturnsNotFoundForAnUnknownAgent(t *testing.T) {
	t.Parallel()
	wantProblem(t, newServer(t).do("human:ada", http.MethodGet, "/v1/agents/ghost", ""), http.StatusNotFound, "not-found")
}

const hireBody = `{"name":"mira","display_name":"Mira","kind":"chat","runner":"echo","role":"Reviewer","limits":{"memory_mb":256}}`

func TestHireAgent_HiresBelowTheCaller(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	rec := s.do("agent:lead", http.MethodPost, "/v1/agents", hireBody)

	wantStatus(t, rec, http.StatusCreated)
	if a := decode[agentDTO](t, rec); a.Manager == nil || *a.Manager != "lead" || a.Team != "lead" || a.State != "sleeping" {
		t.Fatalf("hired %+v", a)
	}
}

func TestHireAgent_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, caller, body string
		status             int
		slug               string
	}{
		{"custom agent hires a root agent", "agent:lead", `{"name":"guard","display_name":"G","kind":"chat","runner":"echo","role":"Guard","is_root":true}`, http.StatusForbidden, "root-only"},
		{"hire above self", "agent:lead", `{"name":"mira","display_name":"M","kind":"chat","runner":"echo","role":"R","manager":"founder"}`, http.StatusForbidden, "not-below-you"},
		{"human hires", "human:ada", hireBody, http.StatusForbidden, "forbidden"},
		{"taken name", "agent:lead", `{"name":"ops","display_name":"O","kind":"chat","runner":"echo","role":"R"}`, http.StatusConflict, "conflict"},
		{"invalid name", "agent:lead", `{"name":"Mira","display_name":"M","kind":"chat","runner":"echo","role":"R"}`, http.StatusBadRequest, "invalid"},
		{"broken body", "agent:lead", `{"name":`, http.StatusBadRequest, "invalid"},
		{"no caller", "", hireBody, http.StatusUnauthorized, "unauthorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantProblem(t, newServer(t).do(tt.caller, http.MethodPost, "/v1/agents", tt.body), tt.status, tt.slug)
		})
	}
}

func TestUpdateAgent_KeepsLimitsTheRequestLeavesOut(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	rec := s.do("agent:lead", http.MethodPatch, "/v1/agents/dev", `{"role":"Reviewer","kind":"chat","limits":{"tasks":32}}`)

	wantStatus(t, rec, http.StatusOK)
	a, err := s.society.Get(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if a.Role != "Reviewer" || a.Kind != domain.KindChat || *a.Limits != (domain.Limits{CPUPercent: 100, MemoryMB: 512, Tasks: 32}) {
		t.Fatalf("dev = %+v limits %+v", a, *a.Limits)
	}
}

func TestUpdateAgent_RefusesAboveSelf(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	wantProblem(t, s.do("agent:dev", http.MethodPatch, "/v1/agents/lead", `{"role":"Boss"}`), http.StatusForbidden, "not-below-you")
	wantProblem(t, s.do("agent:dev", http.MethodPatch, "/v1/agents/ghost", `{"role":"Boss"}`), http.StatusNotFound, "not-found")
}

func TestRetireAgent_RetiresBelowTheCaller(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	wantStatus(t, s.do("agent:lead", http.MethodDelete, "/v1/agents/dev", ""), http.StatusNoContent)
	wantProblem(t, s.do("agent:lead", http.MethodDelete, "/v1/agents/dev", ""), http.StatusConflict, "conflict")
	wantProblem(t, s.do("agent:dev", http.MethodDelete, "/v1/agents/lead", ""), http.StatusConflict, "conflict")
	wantProblem(t, s.do("agent:intern", http.MethodDelete, "/v1/agents/lead", ""), http.StatusForbidden, "not-below-you")
}

type chatDTO struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

func TestChat_SendThenListNewestFirst(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	first := s.do("human:ada", http.MethodPost, "/v1/agents/dev/chat", `{"text":"hello"}`)
	wantStatus(t, first, http.StatusCreated)
	s.do("human:ada", http.MethodPost, "/v1/agents/dev/chat", `{"text":"again"}`)
	if m := decode[chatDTO](t, first); m.Role != "human" || m.Author != "human:ada" {
		t.Fatalf("message %+v", m)
	}

	p := decode[page[chatDTO]](t, s.do("human:ada", http.MethodGet, "/v1/agents/dev/chat?limit=1", ""))
	if len(p.Items) != 1 || p.Items[0].Text != "again" || p.NextCursor == nil {
		t.Fatalf("first page %+v", p)
	}
	rest := decode[page[chatDTO]](t, s.do("human:ada", http.MethodGet, "/v1/agents/dev/chat?cursor="+*p.NextCursor, ""))
	if len(rest.Items) != 1 || rest.Items[0].Text != "hello" || rest.NextCursor != nil {
		t.Fatalf("second page %+v", rest)
	}
}

func TestChat_Refusals(t *testing.T) {
	t.Parallel()
	frozen := newServer(t)
	if err := frozen.society.Freezer().Freeze(ctxAs(core.CallerHost, "agentsone"), core.FreezeScope{Kind: core.FreezeAgent, Target: "dev"}); err != nil {
		t.Fatal(err)
	}
	broke := newServer(t)
	pool := domain.Pool{Currency: "USD", TotalMicros: 0, CheckIntervalSeconds: 60}
	if err := broke.society.Budget().PushPool(ctxAs(core.CallerHost, "agentsone"), pool); err != nil {
		t.Fatal(err)
	}

	wantProblem(t, frozen.do("human:ada", http.MethodPost, "/v1/agents/dev/chat", `{"text":"hi"}`), http.StatusLocked, "frozen")
	wantProblem(t, broke.do("human:ada", http.MethodPost, "/v1/agents/dev/chat", `{"text":"hi"}`), http.StatusPaymentRequired, "budget-exhausted")
	wantProblem(t, newServer(t).do("human:ada", http.MethodGet, "/v1/agents/ghost/chat", ""), http.StatusNotFound, "not-found")
}

func TestOneLine_OnlyTheAgentSetsItsOwn(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	wantProblem(t, s.do("human:ada", http.MethodGet, "/v1/agents/dev/one-line", ""), http.StatusNotFound, "not-found")
	wantStatus(t, s.do("agent:dev", http.MethodPut, "/v1/agents/dev/one-line", `{"text":"Goal: ship. Now: testing."}`), http.StatusOK)
	wantStatus(t, s.do("human:ada", http.MethodGet, "/v1/agents/dev/one-line", ""), http.StatusOK)
	wantProblem(t, s.do("agent:lead", http.MethodPut, "/v1/agents/dev/one-line", `{"text":"x"}`), http.StatusForbidden, "forbidden")
}

func TestWakeAgent(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	wantStatus(t, s.do("agent:watcher", http.MethodPost, "/v1/agents/dev/wake", `{"reason":"Disk is filling up."}`), http.StatusAccepted)
	wantProblem(t, s.do("human:ada", http.MethodPost, "/v1/agents/dev/wake", `{"reason":"x"}`), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("agent:watcher", http.MethodPost, "/v1/agents/ghost/wake", `{"reason":"x"}`), http.StatusNotFound, "not-found")
}

func TestSleepAgent(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	wantStatus(t, s.do("agent:dev", http.MethodPost, "/v1/agents/dev/sleep", `{}`), http.StatusAccepted)
	wantStatus(t, s.do("agent:lead", http.MethodPost, "/v1/agents/dev/sleep", `{"compact":false}`), http.StatusAccepted)
	wantProblem(t, s.do("agent:dev", http.MethodPost, "/v1/agents/lead/sleep", `{}`), http.StatusForbidden, "not-below-you")
}

func TestChart_GetAndMove(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	type chart struct {
		AgentCount int `json:"agent_count"`
		Roots      []struct {
			Agent    string `json:"agent"`
			Children []struct {
				Agent string `json:"agent"`
			} `json:"children"`
		} `json:"roots"`
	}

	got := decode[chart](t, s.do("human:ada", http.MethodGet, "/v1/chart", ""))
	if got.AgentCount != 6 || len(got.Roots) != 1 || got.Roots[0].Agent != "founder" || len(got.Roots[0].Children) != 2 {
		t.Fatalf("chart %+v", got)
	}

	moved := decode[chart](t, s.do("agent:founder", http.MethodPost, "/v1/chart/move", `{"agent":"intern","new_manager":"founder"}`))
	if len(moved.Roots[0].Children) != 3 {
		t.Fatalf("founder reports after move %+v", moved.Roots[0].Children)
	}
	wantProblem(t, s.do("agent:founder", http.MethodPost, "/v1/chart/move", `{"agent":"lead","new_manager":"dev"}`), http.StatusConflict, "conflict")
}

func TestCharter_RootAgentsRewriteIt(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	type charter struct {
		Version   int    `json:"version"`
		Text      string `json:"text"`
		UpdatedBy string `json:"updated_by"`
	}

	if c := decode[charter](t, s.do("human:ada", http.MethodGet, "/v1/charter", "")); c.Version != 0 || c.Text != "" {
		t.Fatalf("empty charter %+v", c)
	}
	if c := decode[charter](t, s.do("agent:founder", http.MethodPut, "/v1/charter", `{"text":"Be kind.","expected_version":0}`)); c.Version != 1 || c.UpdatedBy != "agent:founder" {
		t.Fatalf("rewritten charter %+v", c)
	}
	wantProblem(t, s.do("agent:founder", http.MethodPut, "/v1/charter", `{"text":"x","expected_version":0}`), http.StatusConflict, "conflict")
	wantProblem(t, s.do("agent:lead", http.MethodPut, "/v1/charter", `{"text":"x","expected_version":1}`), http.StatusForbidden, "root-only")
}

type goalDTO struct {
	ID       string  `json:"id"`
	Owner    string  `json:"owner"`
	Status   string  `json:"status"`
	ParentID *string `json:"parent_id"`
}

func TestGoals_Lifecycle(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	parent := decode[goalDTO](t, s.do("agent:lead", http.MethodPost, "/v1/goals", `{"title":"Ship the docs"}`))
	child := s.do("agent:lead", http.MethodPost, "/v1/goals", `{"title":"Write the page","owner":"dev","detail":"Layout first.","parent_id":"`+parent.ID+`"}`)
	wantStatus(t, child, http.StatusCreated)
	g := decode[goalDTO](t, child)
	if g.Owner != "dev" || g.ParentID == nil || *g.ParentID != parent.ID {
		t.Fatalf("child goal %+v", g)
	}

	wantStatus(t, s.do("human:ada", http.MethodGet, "/v1/goals/"+g.ID, ""), http.StatusOK)
	p := decode[page[goalDTO]](t, s.do("human:ada", http.MethodGet, "/v1/goals?limit=1", ""))
	if len(p.Items) != 1 || p.NextCursor == nil {
		t.Fatalf("first page %+v", p)
	}
	devs := decode[page[goalDTO]](t, s.do("human:ada", http.MethodGet, "/v1/goals?owner=dev&status=open", ""))
	if len(devs.Items) != 1 || devs.Items[0].ID != g.ID {
		t.Fatalf("dev's open goals %+v", devs)
	}
	if u := decode[goalDTO](t, s.do("agent:dev", http.MethodPatch, "/v1/goals/"+g.ID, `{"status":"achieved","title":"Wrote the page","detail":"Done."}`)); u.Status != "achieved" {
		t.Fatalf("updated %+v", u)
	}
	wantProblem(t, s.do("agent:intern", http.MethodDelete, "/v1/goals/"+g.ID, ""), http.StatusForbidden, "not-below-you")
	wantStatus(t, s.do("agent:lead", http.MethodDelete, "/v1/goals/"+g.ID, ""), http.StatusNoContent)
	wantProblem(t, s.do("human:ada", http.MethodGet, "/v1/goals/"+g.ID, ""), http.StatusNotFound, "not-found")
}

func TestGoals_RefuseAGoalForAnAgentAbove(t *testing.T) {
	t.Parallel()
	wantProblem(t, newServer(t).do("agent:dev", http.MethodPost, "/v1/goals", `{"title":"Boss","owner":"lead"}`), http.StatusForbidden, "not-below-you")
}

func TestBudget_TotalSplitAndAllocation(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	wantProblem(t, s.do("human:ada", http.MethodGet, "/v1/budget", ""), http.StatusServiceUnavailable, "unavailable")
	wantStatus(t, s.do("human:ada", http.MethodGet, "/v1/budget/split", ""), http.StatusOK)

	pool := domain.Pool{Currency: "EUR", TotalMicros: 1000, CheckIntervalSeconds: 60, Defence: domain.Defence{Enabled: true, ReservedMicros: 100}}
	if err := s.society.Budget().PushPool(ctxAs(core.CallerHost, "agentsone"), pool); err != nil {
		t.Fatal(err)
	}
	type budget struct {
		Wall     string `json:"wall"`
		Currency string `json:"currency"`
	}
	if b := decode[budget](t, s.do("human:ada", http.MethodGet, "/v1/budget", "")); b.Wall != "hard" || b.Currency != "EUR" {
		t.Fatalf("budget %+v", b)
	}

	type node struct {
		Agent           string `json:"agent"`
		AllocatedMicros *int64 `json:"allocated_micros"`
		Wall            string `json:"wall"`
	}
	n := decode[node](t, s.do("agent:founder", http.MethodPut, "/v1/budget/allocations/lead", `{"allocated_micros":600}`))
	if n.AllocatedMicros == nil || *n.AllocatedMicros != 600 || n.Wall != "soft" {
		t.Fatalf("node %+v", n)
	}
	wantProblem(t, s.do("agent:founder", http.MethodPut, "/v1/budget/allocations/ops", `{"allocated_micros":401}`), http.StatusConflict, "conflict")
	wantProblem(t, s.do("agent:lead", http.MethodPut, "/v1/budget/allocations/ops", `{"allocated_micros":1}`), http.StatusForbidden, "not-below-you")

	type split struct {
		Currency string `json:"currency"`
		Nodes    []node `json:"nodes"`
	}
	if sp := decode[split](t, s.do("human:ada", http.MethodGet, "/v1/budget/split", "")); sp.Currency != "EUR" || len(sp.Nodes) != 6 || sp.Nodes[0].Agent != "founder" {
		t.Fatalf("split %+v", sp)
	}
}

func TestAgentTemplatesAndTokens_AreNotImplementedYet(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	for _, target := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/agent-templates", ""},
		{http.MethodPost, "/v1/agents/dev/tokens", `{"name":"ci","scopes":["agents:read"]}`},
	} {
		req := httptest.NewRequest(target.method, target.path, strings.NewReader(target.body))
		req.Header.Set(callerHeader, "agent:dev")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s status = %d, want 501", target.method, target.path, rec.Code)
		}
	}
}
