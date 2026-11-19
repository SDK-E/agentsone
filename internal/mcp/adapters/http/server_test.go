// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/internal/core"
	mcphttp "github.com/SDK-E/agentsone/internal/mcp/adapters/http"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

const testSpec = `{
  "servers": [{"url": "http://127.0.0.1:7700/v1"}],
  "paths": {
    "/agents/{agent_name}": {
      "get": {
        "operationId": "getAgent", "tags": ["agents"], "summary": "Get an agent", "description": "Reads one agent.",
        "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}}}
      },
      "delete": {
        "operationId": "retireAgent", "tags": ["agents"], "summary": "Retire an agent",
        "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"204": {"description": "gone"}}
      }
    },
    "/agents/{agent_name}/output": {
      "get": {
        "operationId": "streamAgentOutput", "tags": ["agents"], "summary": "Follow output", "x-stream": {"protocol": "sse"},
        "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "s", "content": {"text/event-stream": {"schema": {"type": "object"}}}}}
      }
    }
  }
}`

// fakeAPI is the composed handler: it answers with the name asked for and
// the caller it saw, or a problem for the agent "ghost".
func fakeAPI() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /v1/agents/{agent_name}", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		caller, _ := core.CallerFrom(r.Context())
		if r.PathValue("agent_name") == "ghost" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(nethttp.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"urn:agentsone:problem:not-found","title":"Not found","status":404,"detail":"no agent ghost"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": r.PathValue("agent_name"), "seen_by": caller.Name})
	})
	mux.HandleFunc("DELETE /v1/agents/{agent_name}", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNoContent)
	})
	return mux
}

// asCaller stands in for the API layer: it sets the caller from a test header.
func asCaller(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if name := r.Header.Get("X-Test-Caller"); name != "" {
			c := core.Caller{Kind: core.CallerAgent, Name: name, UID: 1001}
			r = r.WithContext(core.WithCaller(r.Context(), c))
		}
		next.ServeHTTP(w, r)
	})
}

type headerTransport struct{ name string }

func (h headerTransport) RoundTrip(r *nethttp.Request) (*nethttp.Response, error) {
	r = r.Clone(r.Context())
	if h.name != "" {
		r.Header.Set("X-Test-Caller", h.name)
	}
	return nethttp.DefaultTransport.RoundTrip(r)
}

func newMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	cat, err := app.NewCatalog([]byte(testSpec))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	disp := mcphttp.NewHandlerDispatcher(fakeAPI())
	srv := mcphttp.NewServer(cat, app.NewCallTool(cat, disp), "test")
	mux := nethttp.NewServeMux()
	mux.Handle("/v1/mcp", asCaller(srv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func connect(t *testing.T, ts *httptest.Server, caller string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/v1/mcp",
		HTTPClient:           &nethttp.Client{Transport: headerTransport{name: caller}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func text(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestServer_ListsOneToolPerOperationExceptStreams(t *testing.T) {
	t.Parallel()
	session := connect(t, newMCPServer(t), "mira")

	res, err := session.ListTools(t.Context(), nil)

	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "get_agent,retire_agent" {
		t.Fatalf("tools = %v, want get_agent and retire_agent", names)
	}
	get := res.Tools[0]
	if !strings.Contains(get.Description, "Get an agent") || get.Annotations == nil || !get.Annotations.ReadOnlyHint {
		t.Errorf("get_agent description %q annotations %+v, want summary and read-only hint", get.Description, get.Annotations)
	}
	if retire := res.Tools[1]; retire.Annotations == nil || retire.Annotations.DestructiveHint == nil || !*retire.Annotations.DestructiveHint {
		t.Errorf("retire_agent annotations = %+v, want destructive hint", retire.Annotations)
	}
}

func TestServer_CallsTheOperationAsTheSessionsCaller(t *testing.T) {
	t.Parallel()
	session := connect(t, newMCPServer(t), "mira")

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_agent", Arguments: map[string]any{"agent_name": "bo"}})

	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("result is an error: %s", text(res))
	}
	if !strings.Contains(text(res), `"seen_by":"mira"`) || !strings.Contains(text(res), `"name":"bo"`) {
		t.Fatalf("text = %s, want the body with the caller mira", text(res))
	}
	structured, _ := res.StructuredContent.(map[string]any)
	if structured["name"] != "bo" {
		t.Errorf("structured content = %v", res.StructuredContent)
	}
}

func TestServer_AnswersAProblemAsAToolErrorWithTypeAndDetail(t *testing.T) {
	t.Parallel()
	session := connect(t, newMCPServer(t), "mira")

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_agent", Arguments: map[string]any{"agent_name": "ghost"}})

	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if !res.IsError {
		t.Fatal("a problem response was not a tool error")
	}
	if !strings.Contains(text(res), "urn:agentsone:problem:not-found") || !strings.Contains(text(res), "no agent ghost") {
		t.Fatalf("text = %q, want the problem type and detail", text(res))
	}
}

func TestServer_AnswersBadArgumentsAsAToolError(t *testing.T) {
	t.Parallel()
	session := connect(t, newMCPServer(t), "mira")

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_agent", Arguments: map[string]any{}})

	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if !res.IsError || !strings.Contains(text(res), "agent_name") {
		t.Fatalf("result = %+v %q, want a tool error naming agent_name", res.IsError, text(res))
	}
}

func TestServer_AnswersAnEmptySuccessWithOK(t *testing.T) {
	t.Parallel()
	session := connect(t, newMCPServer(t), "mira")

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "retire_agent", Arguments: map[string]any{"agent_name": "bo"}})

	if err != nil || res.IsError {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if !strings.Contains(text(res), "204") {
		t.Fatalf("text = %q, want the status", text(res))
	}
}

func TestServer_RefusesARequestWithoutACaller(t *testing.T) {
	t.Parallel()
	ts := newMCPServer(t)

	resp, err := nethttp.Post(ts.URL+"/v1/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != nethttp.StatusUnauthorized || resp.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status %d type %q, want 401 problem+json", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestServer_RefusesAnotherCallerOnSomeoneElsesSession(t *testing.T) {
	t.Parallel()
	ts := newMCPServer(t)
	session := connect(t, ts, "mira")

	req, _ := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, ts.URL+"/v1/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", session.ID())
	req.Header.Set("X-Test-Caller", "mallory")
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != nethttp.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a different caller on the session", resp.StatusCode)
	}
}

func TestServer_GroupServesTheMcpTagOnItsPath(t *testing.T) {
	t.Parallel()
	cat, _ := app.NewCatalog([]byte(testSpec))
	disp := mcphttp.NewHandlerDispatcher(fakeAPI())
	srv := mcphttp.NewServer(cat, app.NewCallTool(cat, disp), "test")

	g := srv.Group()
	mux := nethttp.NewServeMux()
	g.Register(mux, core.APIOptions{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/v1/mcp", strings.NewReader(`{}`)))

	if g.Tag != "mcp" {
		t.Fatalf("tag = %q", g.Tag)
	}
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("status = %d, want the mcp handler's 401 for no caller", rec.Code)
	}
}
