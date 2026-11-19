// SPDX-License-Identifier: MIT

package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/runners/openai"
)

// gwRequest is one call the fake gateway received.
type gwRequest struct {
	Auth         string
	Weight       string
	Model        string
	Messages     []openai.Message
	Tools        []openai.ToolDef
	Stream       bool
	IncludeUsage bool
}

// last is the content of the last message.
func (r gwRequest) last() openai.Message { return r.Messages[len(r.Messages)-1] }

// fakeCall is a tool call the fake model asks for.
type fakeCall struct{ ID, Name, Args string }

// reply is what the fake gateway answers.
type reply struct {
	Status int      // 0 means 200
	Text   []string // content deltas
	Calls  []fakeCall
	Hold   bool   // block until the request is cancelled
	Raw    string // replaces the whole stream body
	NoUse  bool   // leave out the usage chunk
	Cached int    // cached prompt tokens
}

// fakeGateway is an OpenAI-compatible chat-completions server.
type fakeGateway struct {
	srv *httptest.Server

	mu      sync.Mutex
	reqs    []gwRequest
	respond func(gwRequest) reply
	spent   map[string]bool
}

func newGateway(t testing.TB, respond func(gwRequest) reply) *fakeGateway {
	t.Helper()
	g := &fakeGateway{respond: respond, spent: map[string]bool{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(func() { g.srv.CloseClientConnections(); g.srv.Close() })
	return g
}

// URL is the base URL, with the /v1 prefix real gateways use.
func (g *fakeGateway) URL() string { return g.srv.URL + "/v1" }

func (g *fakeGateway) requests() []gwRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]gwRequest(nil), g.reqs...)
}

// firstTime is true once per key: the fake gateway refuses the budget once per
// token, the way a daemon that tops the budget up would.
func (g *fakeGateway) firstTime(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.spent[key] {
		return false
	}
	g.spent[key] = true
	return true
}

func (g *fakeGateway) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model         string           `json:"model"`
		Messages      []openai.Message `json:"messages"`
		Tools         []openai.ToolDef `json:"tools"`
		Stream        bool             `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := gwRequest{Auth: r.Header.Get("Authorization"), Weight: r.Header.Get(openai.WeightHeader), Model: body.Model,
		Messages: body.Messages, Tools: body.Tools, Stream: body.Stream, IncludeUsage: body.StreamOptions.IncludeUsage}
	g.mu.Lock()
	g.reqs = append(g.reqs, req)
	g.mu.Unlock()
	rep := g.respond(req)
	if rep.Status != 0 {
		http.Error(w, "refused", rep.Status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	if rep.Hold {
		sse(w, "data: "+chunk(`{"content":"working"}`)+"\n\n")
		flusher.Flush()
		<-r.Context().Done()
		return
	}
	if rep.Raw != "" {
		sse(w, rep.Raw)
		return
	}
	for _, text := range rep.Text {
		enc, _ := json.Marshal(text)
		sse(w, "data: "+chunk(`{"content":`+string(enc)+`}`)+"\n\n")
		flusher.Flush()
	}
	for i, c := range rep.Calls {
		first, _ := json.Marshal(map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": c.ID, "type": "function",
			"function": map[string]any{"name": c.Name, "arguments": ""}}}})
		sse(w, "data: "+chunk(string(first))+"\n\n")
		half := len(c.Args) / 2
		for _, part := range []string{c.Args[:half], c.Args[half:]} {
			more, _ := json.Marshal(map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": part}}}})
			sse(w, "data: "+chunk(string(more))+"\n\n")
		}
	}
	if !rep.NoUse {
		sse(w, fmt.Sprintf(`data: {"model":"autoagents/one-actual","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":%d}}}`+"\n\n", rep.Cached))
	}
	sse(w, "data: [DONE]\n\n")
}

// sse writes to the event stream; a client that left is not an error here.
func sse(w http.ResponseWriter, s string) { _, _ = io.WriteString(w, s) }

func chunk(delta string) string {
	return `{"model":"autoagents/one-actual","choices":[{"index":0,"delta":` + delta + `}]}`
}

// fakeMCP is an in-process MCP server with two tools, add and fail. It records
// the bearer tokens it saw.
type fakeMCP struct {
	srv *httptest.Server

	mu     sync.Mutex
	tokens []string
	calls  []string
}

type addIn struct {
	A int `json:"a"`
	B int `json:"b"`
}

type addOut struct {
	Sum int `json:"sum"`
}

func newMCP(t testing.TB, wantToken string) *fakeMCP {
	t.Helper()
	f := &fakeMCP{}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "add", Description: "add two numbers"},
		func(_ context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, addOut, error) {
			f.record("add")
			return nil, addOut{Sum: in.A + in.B}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "always fails"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ addIn) (*mcp.CallToolResult, addOut, error) {
			f.record("fail")
			return nil, addOut{}, errors.New("boom")
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		f.tokens = append(f.tokens, got)
		f.mu.Unlock()
		if got != wantToken {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { f.srv.CloseClientConnections(); f.srv.Close() })
	return f
}

func (f *fakeMCP) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeMCP) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeMCP) seenTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokens...)
}
