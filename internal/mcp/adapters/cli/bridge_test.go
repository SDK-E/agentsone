// SPDX-License-Identifier: MIT

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	mcphttp "github.com/SDK-E/agentsone/internal/mcp/adapters/http"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

// startMCPServer serves the machine MCP for the test catalog. The caller is
// always the agent "mira", as the API layer would set it from a socket peer.
func startMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	cat := newCatalog(t)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/agents/{agent_name}", func(w http.ResponseWriter, r *http.Request) {
		caller, _ := core.CallerFrom(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": r.PathValue("agent_name"), "seen_by": caller.Name})
	})
	srv := mcphttp.NewServer(cat, app.NewCallTool(cat, mcphttp.NewHandlerDispatcher(api)), "test")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := core.Caller{Kind: core.CallerAgent, Name: "mira", UID: 1001}
		srv.Handler().ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestBridge_ForwardsAnMCPSessionBetweenTwoTransports(t *testing.T) {
	t.Parallel()
	ts := startMCPServer(t)
	ep, _ := cli.ParseEndpoint(ts.URL)
	remote := cli.NewClient(ep, "").MCPTransport()
	localClientSide, localBridgeSide := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- cli.Bridge(ctx, localBridgeSide, remote) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-user", Version: "1"}, nil)
	session, err := client.Connect(ctx, localClientSide, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	if !names["get_agent"] || !names["hire_agent"] || names["stream_events"] {
		t.Fatalf("tools = %v", names)
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_agent", Arguments: map[string]any{"agent_name": "bo"}})
	if err != nil || res.IsError {
		t.Fatalf("CallTool() = %+v, %v", res, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !strings.Contains(text.String(), `"seen_by":"mira"`) {
		t.Fatalf("text = %q, want the call to run as the session caller", text.String())
	}

	_ = session.Close()
	if err := <-done; err != nil {
		t.Fatalf("Bridge() error = %v, want nil when the local side hangs up", err)
	}
}

func TestMCPCommand_ServesStdioToTheDaemon(t *testing.T) {
	t.Parallel()
	ts := startMCPServer(t)
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_agent","arguments":{"agent_name":"bo"}}}` + "\n")
	var out, errOut bytes.Buffer

	code := cli.Run(t.Context(), newCatalog(t), "test", []string{"--url", ts.URL, "mcp"}, cli.IO{In: in, Out: &out, Err: &errOut})

	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, `"id":1`) || !strings.Contains(got, `"id":2`) || !strings.Contains(got, `seen_by`) {
		t.Fatalf("stdout = %q, want both answers and the tool result", got)
	}
}

func TestMCPCommand_ReportsAnUnreachableDaemon(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")

	code := cli.Run(t.Context(), newCatalog(t), "test", []string{"--url", "/nonexistent/api.sock", "mcp"}, cli.IO{In: in, Out: &out, Err: &errOut})

	if code != 1 || !strings.Contains(errOut.String(), "/nonexistent/api.sock") {
		t.Fatalf("exit %d stderr %q stdout %q", code, errOut.String(), out.String())
	}
}
