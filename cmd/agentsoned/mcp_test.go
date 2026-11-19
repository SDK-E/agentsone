// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// Integration test: an MCP tool call on the real socket reaches a real
// operation (getSetupStatus, served by the auth module) in process.
func TestDaemon_MCPCall(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	deps.Config.LibDir = t.TempDir()
	httpc := startDaemon(t, deps)
	if code := get(t, httpc, "http://agentsoned/v1/setup"); code != http.StatusOK {
		t.Fatalf("setup status %d", code)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: "http://agentsoned/v1/mcp", HTTPClient: httpc, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_setup_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if res.IsError || !strings.Contains(sb.String(), "needs-human") {
		t.Fatalf("result = %v %q, want the setup status", res.IsError, sb.String())
	}
}
