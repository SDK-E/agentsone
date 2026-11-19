// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
)

const templateDoc = `{"name":"docs-writer","kind":"chat","isRoot":false,"runner":{"name":"claude","config":{}},
"oneLine":"Goal: clear docs. Now: waiting.","charter":"Write plain guides.","goals":["Keep guides current."],
"skills":[],"mcpServers":[],"limits":{"cpuPercent":100,"memoryMiB":1024,"tasks":256},"wake":{"subscriptions":[]},"schedules":[]}`

func (s *server) addTemplates(ids ...string) {
	for _, id := range ids {
		s.fakes.Templates.Add(core.AgentTemplate{ID: id, Name: "docs-writer", Kind: core.AgentChat, Runner: "claude", Raw: json.RawMessage(templateDoc)})
	}
}

type templatePage struct {
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func TestListAgentTemplates_PagesByID(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.addTemplates("seed/a", "seed/b", "seed/c")

	first := s.do("human:ada", http.MethodGet, "/v1/agent-templates?limit=2", "")
	wantStatus(t, first, http.StatusOK)
	page := decode[templatePage](t, first)
	if len(page.Items) != 2 || page.NextCursor == nil || *page.NextCursor != "seed/b" {
		t.Fatalf("first page %+v", page)
	}

	second := s.do("human:ada", http.MethodGet, "/v1/agent-templates?limit=2&cursor=seed%2Fb", "")
	wantStatus(t, second, http.StatusOK)
	if page := decode[templatePage](t, second); len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("second page %+v", page)
	}
}

func TestListAgentTemplates_EmptyCatalog(t *testing.T) {
	t.Parallel()
	rec := newServer(t).do("human:ada", http.MethodGet, "/v1/agent-templates", "")

	wantStatus(t, rec, http.StatusOK)
	if page := decode[templatePage](t, rec); len(page.Items) != 0 {
		t.Fatalf("items %+v", page.Items)
	}
}

func TestHireAgent_FromATemplate(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.addTemplates("seed/docs-writer")
	body := `{"name":"scribe","display_name":"Scribe","kind":"chat","runner":"claude","role":"Writer","template":"seed/docs-writer"}`

	rec := s.do("agent:lead", http.MethodPost, "/v1/agents", body)

	wantStatus(t, rec, http.StatusCreated)
	var got struct {
		Template *string `json:"template"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Template == nil || *got.Template != "seed/docs-writer" {
		t.Fatalf("template %v %v", got.Template, err)
	}
}

func TestHireAgent_FromAnUnknownTemplateIsAValidationProblem(t *testing.T) {
	t.Parallel()
	body := `{"name":"scribe","display_name":"Scribe","kind":"chat","runner":"claude","role":"Writer","template":"seed/ghost"}`

	rec := newServer(t).do("agent:lead", http.MethodPost, "/v1/agents", body)

	wantProblem(t, rec, http.StatusBadRequest, "validation")
}

func (s *server) issue(caller, scopes, agent, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/"+agent+"/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(callerHeader, caller)
	if scopes != "" {
		req.Header.Set(scopesHeader, scopes)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	contracttest.CheckResponse(s.t, req, rec)
	return rec
}

func TestIssueAgentToken_ReturnsTheSecretOnce(t *testing.T) {
	t.Parallel()
	s := newServer(t)

	rec := s.issue("human:ada", "tasks:write", "dev", `{"label":"build tool","scopes":["tasks:write"]}`)

	wantStatus(t, rec, http.StatusCreated)
	var got struct {
		ID      string   `json:"id"`
		Token   string   `json:"token"`
		Subject string   `json:"subject"`
		Scopes  []string `json:"scopes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.ID, "tok_") || got.Token == "" || got.Subject != "agent:dev" || len(got.Scopes) != 1 {
		t.Fatalf("token %+v", got)
	}
}

func TestIssueAgentToken_Problems(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, caller, scopes, agent, body string
		status                            int
		slug                              string
	}{
		{"scope the issuer lacks", "human:ada", "memory:read", "dev", `{"label":"x","scopes":["tasks:write"]}`, http.StatusForbidden, "forbidden"},
		{"agent outside the branch", "agent:ops", "tasks:write", "dev", `{"label":"x","scopes":["tasks:write"]}`, http.StatusForbidden, "not-below-you"},
		{"unknown agent", "human:ada", "tasks:write", "ghost", `{"label":"x","scopes":["tasks:write"]}`, http.StatusNotFound, "not-found"},
		{"lifetime too short", "human:ada", "tasks:write", "dev", `{"label":"x","scopes":["tasks:write"],"ttl_seconds":59}`, http.StatusBadRequest, "validation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := newServer(t).issue(tt.caller, tt.scopes, tt.agent, tt.body)
			wantProblem(t, rec, tt.status, tt.slug)
		})
	}
}
