// SPDX-License-Identifier: MIT

package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/contract/api/agentsapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	mcpmod "github.com/SDK-E/agentsone/internal/mcp"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := mcpmod.New(deps)
	if m.Name() != "mcp" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "mcp")
}

// agentsModule is a stand-in agents module whose operations answer 501.
type agentsModule struct{ core.Base }

func (agentsModule) Name() string { return "agents" }
func (agentsModule) API() core.API {
	return core.API{Groups: []core.APIGroup{agentsapi.Group(agentsapi.Unimplemented{})}}
}

func TestModuleServesToolsThroughTheComposedHandler(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := mcpmod.New(deps)
	composed, err := api.Compose([]core.Module{agentsModule{}, m})
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	m.SetHandler(composed)
	asAgent := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := core.Caller{Kind: core.CallerAgent, Name: "mira", UID: 1001}
		composed.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	})
	ts := httptest.NewServer(asAgent)
	t.Cleanup(ts.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/v1/mcp", DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) < 100 {
		t.Fatalf("got %d tools, want one per API operation", len(tools.Tools))
	}
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_agents", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if !res.IsError || !strings.Contains(sb.String(), "not-implemented") {
		t.Fatalf("result = %v %q, want the 501 problem as a tool error", res.IsError, sb.String())
	}
}

func TestModuleToolCallsFailPlainlyBeforeTheHandlerIsSet(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := mcpmod.New(deps)
	composed, err := api.Compose([]core.Module{m})
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := core.Caller{Kind: core.CallerAgent, Name: "mira"}
		composed.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	}))
	t.Cleanup(ts.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/v1/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_agents", Arguments: map[string]any{}})

	if err != nil || !res.IsError {
		t.Fatalf("result = %+v, err = %v, want a tool error", res, err)
	}
}
