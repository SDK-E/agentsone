// SPDX-License-Identifier: MIT

package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// gwRequest is one call the fake gateway received.
type gwRequest struct {
	Path, Query, Auth, APIKey, Model, System, Last string
}

// gwReply is what the fake gateway answers.
type gwReply struct {
	Status int      // 0 means 200
	Text   []string // text deltas
	Hold   bool     // stream one delta, then block until the call is cancelled
	Wait   <-chan struct{}
}

// fakeGateway is the gateway's Anthropic Messages API, streaming SSE.
type fakeGateway struct {
	srv *httptest.Server

	mu      sync.Mutex
	reqs    []gwRequest
	respond func(gwRequest) gwReply
	spent   map[string]bool
}

func newGateway(t testing.TB, respond func(gwRequest) gwReply) *fakeGateway {
	t.Helper()
	g := &fakeGateway{respond: respond, spent: map[string]bool{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(func() { g.srv.CloseClientConnections(); g.srv.Close() })
	return g
}

// URL is the welcome's model.baseUrl, with the /v1 suffix real gateways use.
func (g *fakeGateway) URL() string { return g.srv.URL + "/v1" }

func (g *fakeGateway) requests() []gwRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.reqs)
}

// firstTime is true once per key: the budget is refused once per token.
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
	if r.URL.Path != "/anthropic/v1/messages" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model    string `json:"model"`
		System   string `json:"system"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	req := gwRequest{Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), APIKey: r.Header.Get("X-Api-Key"),
		Model: body.Model, System: body.System, Last: body.Messages[len(body.Messages)-1].Content}
	g.mu.Lock()
	g.reqs = append(g.reqs, req)
	g.mu.Unlock()
	rep := g.respond(req)
	if rep.Wait != nil {
		select {
		case <-rep.Wait:
		case <-r.Context().Done():
			return
		}
	}
	switch rep.Status {
	case 0:
	case http.StatusLocked:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(rep.Status)
		_, _ = io.WriteString(w, `{"type":"urn:agentsone:problem:frozen","title":"Frozen"}`)
		return
	default:
		http.Error(w, "refused", rep.Status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	event := func(typ, data string) {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, data)
		flusher.Flush()
	}
	delta := func(text string) {
		enc, _ := json.Marshal(text)
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(enc)+`}}`)
	}
	event("message_start", `{"type":"message_start","message":{"model":"autoagents/one-actual","usage":{"input_tokens":11}}}`)
	event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	if rep.Hold {
		delta("working")
		<-r.Context().Done()
		return
	}
	for _, text := range rep.Text {
		delta(text)
	}
	event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`)
	event("message_stop", `{"type":"message_stop"}`)
}

// fakeMCP is an MCP server with one tool, add. It records the bearer tokens
// it saw and accepts only the allowed one.
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

func newMCP(t testing.TB, allowed string) *fakeMCP {
	t.Helper()
	f := &fakeMCP{}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "add", Description: "add two numbers"},
		func(_ context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, addOut, error) {
			f.mu.Lock()
			f.calls = append(f.calls, "add")
			f.mu.Unlock()
			return nil, addOut{Sum: in.A + in.B}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		f.tokens = append(f.tokens, got)
		f.mu.Unlock()
		if got != allowed {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { f.srv.CloseClientConnections(); f.srv.Close() })
	return f
}

func (f *fakeMCP) seen() (tokens, calls []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tokens), slices.Clone(f.calls)
}

// trap counts every request that reaches it. Nothing may.
type trap struct {
	srv  *httptest.Server
	hits atomic.Int64
}

func newTrap(t testing.TB) *trap {
	t.Helper()
	tr := &trap{}
	tr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tr.hits.Add(1)
		http.Error(w, "trapped", http.StatusTeapot)
	}))
	t.Cleanup(tr.srv.Close)
	return tr
}
