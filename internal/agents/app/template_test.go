// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func addTemplate(e env, id string, root bool) {
	raw, _ := json.Marshal(map[string]any{
		"name": "docs-writer", "kind": "chat", "isRoot": root,
		"oneLine": "Goal: clear docs. Now: waiting.",
		"goals":   []string{"Keep every guide current."},
	})
	e.fakes.Templates.Add(core.AgentTemplate{ID: id, Name: "docs-writer", Kind: core.AgentChat, IsRoot: root, Runner: "claude", Raw: raw})
}

func TestTemplates_ListsInstalledTemplates(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	addTemplate(e, "small-seed/docs-writer", false)

	got, err := e.society.Templates(asHuman("ada"))
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].ID != "small-seed/docs-writer" {
		t.Fatalf("templates %+v", got)
	}
}

func TestHire_FromATemplateSeedsTheOneLineAndGoals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	addTemplate(e, "small-seed/docs-writer", false)
	s := spec("scribe")
	s.Template = "small-seed/docs-writer"

	got, err := e.society.Hire(asAgent("lead"), s)
	if err != nil {
		t.Fatal(err)
	}

	if got.Template != "small-seed/docs-writer" {
		t.Errorf("template = %q", got.Template)
	}
	line, err := e.society.OneLine(context.Background(), "scribe")
	if err != nil || line.Line != "Goal: clear docs. Now: waiting." {
		t.Errorf("one-line %+v %v", line, err)
	}
	goals, _ := e.society.Goals(context.Background(), domain.GoalFilter{Owner: "scribe"})
	var titles []string
	for _, g := range goals {
		titles = append(titles, g.Title)
	}
	if diff := cmp.Diff([]string{"Keep every guide current."}, titles); diff != "" {
		t.Errorf("goals (-want +got):\n%s", diff)
	}
	var data map[string]any
	_ = json.Unmarshal(e.fakes.Events.Published()[0].Data, &data)
	if data["template"] != "small-seed/docs-writer" {
		t.Errorf("hired event template = %v", data["template"])
	}
}

func TestHire_FromATemplateRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		actor    string
		template string
		wantErr  error
	}{
		{"custom agent hires from a root template", "lead", "small-seed/guard", domain.ErrRootOnly},
		{"unknown template", "lead", "small-seed/ghost", domain.ErrInvalidAgent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			addTemplate(e, "small-seed/guard", true)
			s := spec("guard")
			s.Template = tt.template
			_, err := e.society.Hire(asAgent(tt.actor), s)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Hire() error = %v, want %v", err, tt.wantErr)
			}
			e.assertNothingHappened(t)
		})
	}
}

func TestHire_FromARootTemplateMakesARootAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	addTemplate(e, "small-seed/guard", true)
	s := spec("guard")
	s.Template = "small-seed/guard"

	got, err := e.society.Hire(asAgent("founder"), s)
	if err != nil {
		t.Fatal(err)
	}

	if !got.IsRoot || got.UID != 0 {
		t.Fatalf("agent %+v, want root", got)
	}
}

func token(scopes ...string) app.TokenRequest {
	return app.TokenRequest{Label: "build tool", Scopes: scopes, TTL: 24 * time.Hour}
}

func withScopes(ctx context.Context, scopes ...string) context.Context {
	c, _ := core.CallerFrom(ctx)
	c.Scopes = scopes
	return core.WithCaller(ctx, c)
}

func TestIssueToken_ForSelfAboveOrAHuman(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		ctx     context.Context
		agent   string
		wantErr error
	}{
		{"the agent itself", withScopes(asAgent("dev"), "tasks:write"), "dev", nil},
		{"an agent above it", withScopes(asAgent("lead"), "tasks:write"), "dev", nil},
		{"a human", withScopes(asHuman("ada"), "tasks:write"), "dev", nil},
		{"a report of the agent", withScopes(asAgent("intern"), "tasks:write"), "dev", domain.ErrNotBelowActor},
		{"a peer branch", withScopes(asAgent("ops"), "tasks:write"), "dev", domain.ErrNotBelowActor},
		{"scopes the issuer does not hold", withScopes(asAgent("dev"), "memory:read"), "dev", core.ErrForbidden},
		{"unknown agent", withScopes(asHuman("ada"), "tasks:write"), "ghost", domain.ErrAgentNotFound},
		{"no caller", context.Background(), "dev", core.ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			got, err := e.society.IssueToken(tt.ctx, tt.agent, token("tasks:write"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("IssueToken() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				e.assertNothingHappened(t)
				return
			}
			if !strings.HasPrefix(got.ID, "tok_") || got.Value == "" || got.Subject != "agent:dev" {
				t.Fatalf("token %+v", got)
			}
			caller, err := e.fakes.Tokens.Verify(context.Background(), got.Value)
			if err != nil || caller.Name != "dev" || caller.Kind != core.CallerAgent {
				t.Fatalf("Verify() = %+v %v", caller, err)
			}
			rec := e.fakes.Audit.Entries()[0]
			if rec.Action != "token.issue" || strings.Contains(string(rec.Detail), got.Value) {
				t.Fatalf("audit %+v must name the issue and never hold the secret", rec)
			}
		})
	}
}

func TestIssueToken_RootCallersHoldEveryScope(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 0, IsRoot: true})

	if _, err := e.society.IssueToken(root, "dev", token("memory:admin")); err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
}

func TestIssueToken_Validation(t *testing.T) {
	t.Parallel()
	tests := map[string]app.TokenRequest{
		"empty label":        {Label: "", Scopes: []string{"tasks:write"}, TTL: time.Hour},
		"label of 81":        {Label: strings.Repeat("l", 81), Scopes: []string{"tasks:write"}, TTL: time.Hour},
		"no scopes":          {Label: "x", TTL: time.Hour},
		"ttl under a minute": {Label: "x", Scopes: []string{"tasks:write"}, TTL: 59 * time.Second},
		"ttl over 30 days":   {Label: "x", Scopes: []string{"tasks:write"}, TTL: 30*24*time.Hour + time.Second},
	}
	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			_, err := e.society.IssueToken(withScopes(asHuman("ada"), "tasks:write"), "dev", req)
			if !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("IssueToken() error = %v, want %v", err, core.ErrInvalid)
			}
		})
	}
}
