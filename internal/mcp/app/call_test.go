// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func callWith(ctx context.Context, t *testing.T, d *fakeDispatcher, name string, args map[string]any) (app.Result, error) {
	t.Helper()
	return app.NewCallTool(newCatalog(t), d).Do(ctx, name, args)
}

func TestCallTool_ReturnsTheResponseBody(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{reply: app.Reply{Status: 200, Body: []byte(`{"name":"mira"}`)}}

	got, err := callWith(t.Context(), t, d, "get_agent", map[string]any{"agent_name": "mira"})

	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if got.IsError || string(got.Body) != `{"name":"mira"}` {
		t.Fatalf("result = %+v", got)
	}
	if len(d.reqs) != 1 || d.reqs[0].Path != "/v1/agents/mira" || d.reqs[0].Method != "GET" {
		t.Fatalf("dispatched = %+v", d.reqs)
	}
}

func TestCallTool_AnswersOKForAnEmptySuccess(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{reply: app.Reply{Status: 204}}

	got, err := callWith(t.Context(), t, d, "get_agent", map[string]any{"agent_name": "mira"})

	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if got.IsError || len(got.Body) != 0 || got.Status != 204 {
		t.Fatalf("result = %+v, want an empty success", got)
	}
}

func TestCallTool_TurnsAProblemIntoAToolError(t *testing.T) {
	t.Parallel()
	body := `{"type":"urn:agentsone:problem:not-found","title":"Not found","status":404,"detail":"no agent mira"}`
	d := &fakeDispatcher{reply: app.Reply{Status: 404, Body: []byte(body)}}

	got, err := callWith(t.Context(), t, d, "get_agent", map[string]any{"agent_name": "mira"})

	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if !got.IsError || got.Problem == nil || got.Problem.Type != "urn:agentsone:problem:not-found" || got.Problem.Detail != "no agent mira" {
		t.Fatalf("result = %+v, want an error with the problem", got)
	}
}

func TestCallTool_KeepsTheCallersIdentityOnTheDispatchedRequest(t *testing.T) {
	t.Parallel()
	agent := core.Caller{Kind: core.CallerAgent, Name: "mira", UID: 1001, Scopes: []string{"agents:read"}}
	ctx := core.WithCaller(t.Context(), agent)
	d := &fakeDispatcher{reply: app.Reply{Status: 200, Body: []byte(`{}`)}}

	if _, err := callWith(ctx, t, d, "get_agent", map[string]any{"agent_name": "mira"}); err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if len(d.callers) != 1 || d.callers[0].Name != "mira" || d.callers[0].IsRoot || d.callers[0].Kind != core.CallerAgent {
		t.Fatalf("dispatcher saw caller %+v, want the same agent, not elevated", d.callers)
	}
}

func TestCallTool_RefusesAnUnknownTool(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{}

	_, err := callWith(t.Context(), t, d, "get_nothing", nil)

	if !errors.Is(err, app.ErrUnknownTool) {
		t.Fatalf("Do() error = %v, want ErrUnknownTool", err)
	}
	if len(d.reqs) != 0 {
		t.Fatal("a request was dispatched for an unknown tool")
	}
}

func TestCallTool_RefusesBadArgumentsWithoutDispatching(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{}

	_, err := callWith(t.Context(), t, d, "get_agent", map[string]any{})

	if !errors.Is(err, domain.ErrMissingArgument) {
		t.Fatalf("Do() error = %v, want ErrMissingArgument", err)
	}
	if len(d.reqs) != 0 {
		t.Fatal("a request was dispatched for bad arguments")
	}
}

func TestCallTool_ReportsAFailedDispatch(t *testing.T) {
	t.Parallel()
	boom := errors.New("handler gone")
	d := &fakeDispatcher{err: boom}

	_, err := callWith(t.Context(), t, d, "get_agent", map[string]any{"agent_name": "mira"})

	if !errors.Is(err, app.ErrDispatch) || !errors.Is(err, boom) {
		t.Fatalf("Do() error = %v, want ErrDispatch wrapping the cause", err)
	}
}
