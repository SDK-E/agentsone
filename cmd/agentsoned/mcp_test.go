// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// Integration test: an MCP tool call on the real socket reaches a real
// operation (getSetupStatus, served by the auth module) in process.
func TestDaemon_MCPToolCallReachesARealOperation(t *testing.T) {
	short, err := os.MkdirTemp("", "a1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	deps, _ := coretest.NewDeps(t)
	deps.Config.RuntimeDir = short
	deps.Config.LibDir = t.TempDir()
	deps.Config.API.Addr = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, deps) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	httpc := unixClient(deps.Config.SocketPath())
	resp := waitFor(t, httpc, "http://agentsoned/v1/setup")
	_ = resp.Body.Close() // test response body
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup status %d", resp.StatusCode)
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
