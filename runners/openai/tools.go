// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/runners/sdk"
)

// ToolBox gives the model the agent's tools. The runner uses the machine MCP
// endpoint of the welcome; tests use a fake.
type ToolBox interface {
	// Tools lists the tools the model may call.
	Tools(ctx context.Context) ([]ToolDef, error)
	// Call runs one tool. args is a JSON object. A tool that ran but failed
	// returns its message as the result, so the model can react to it.
	Call(ctx context.Context, name string, args json.RawMessage) (string, error)
	// Close ends the session with the tool server.
	Close() error
}

// Dialer opens a ToolBox for the MCP endpoint of a welcome.
type Dialer func(ctx context.Context, m sdk.MCP, client *http.Client) (ToolBox, error)

// DialMCP connects to the streamable-HTTP MCP endpoint of the welcome with its
// scoped token.
func DialMCP(ctx context.Context, m sdk.MCP, client *http.Client) (ToolBox, error) {
	if m.Transport != "streamable-http" || m.URL == "" {
		return nil, fmt.Errorf("mcp transport %q is not supported; need streamable-http with a url", m.Transport)
	}
	hc := *client
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	hc.Transport = bearer{token: m.Token, next: base}
	c := mcp.NewClient(&mcp.Implementation{Name: "agentsone-runner-openai", Version: Version}, nil)
	session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: m.URL, HTTPClient: &hc, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP endpoint: %w", err)
	}
	return &mcpBox{session: session}, nil
}

// bearer adds the scoped token to every request.
type bearer struct {
	token sdk.Secret
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token.Reveal())
	return b.next.RoundTrip(r) //nolint:wrapcheck // transport errors pass through unchanged
}

type mcpBox struct{ session *mcp.ClientSession }

func (b *mcpBox) Tools(ctx context.Context) ([]ToolDef, error) {
	var defs []ToolDef
	cursor := ""
	for {
		res, err := b.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
		for _, t := range res.Tools {
			schema, err := json.Marshal(t.InputSchema)
			if err != nil || string(schema) == "null" {
				schema = []byte(`{"type":"object"}`)
			}
			defs = append(defs, ToolDef{Type: "function", Function: FunctionDef{Name: t.Name, Description: t.Description, Parameters: schema}})
		}
		if res.NextCursor == "" {
			return defs, nil
		}
		cursor = res.NextCursor
	}
}

func (b *mcpBox) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(strings.TrimSpace(string(args))) == 0 {
		args = json.RawMessage(`{}`)
	}
	res, err := b.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return "error: " + text.String(), nil
	}
	return text.String(), nil
}

func (b *mcpBox) Close() error {
	if err := b.session.Close(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("close MCP session: %w", err)
	}
	return nil
}
