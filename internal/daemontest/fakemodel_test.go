// SPDX-License-Identifier: MIT

package daemontest

// The fake model lives in a _test.go file on purpose: Go builds test files
// only into test binaries, so no binary the project ships can hold a fake
// provider (TESTING.md B5f). The harness's tests reach it as
// daemontest.NewFakeModel.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

// FakeModelReply is the text the fake model answers when no reply is
// scripted.
const FakeModelReply = "Hello from the fake model."

// The usage every answer of the fake model reports.
const (
	fakePromptTokens     = 1000
	fakeCompletionTokens = 50
)

// secretHeaders are the headers a ModelCall leaves out: the provider key
// travels in them, and a test reads only whether it was the right one.
var secretHeaders = []string{"Authorization", "Proxy-Authorization", "X-Api-Key", "Cookie"}

// ModelReply is one scripted answer of the fake model: a text, or the tool
// calls the model asks for.
type ModelReply struct {
	Text      string
	ToolCalls []ModelToolCall
}

// ModelToolCall is one function call the model asks for. Arguments is JSON.
type ModelToolCall struct {
	ID, Name, Arguments string
}

// ModelCall is what the fake model saw of one call, without its secrets.
type ModelCall struct {
	Path  string
	Model string
	// Headers are the call's headers without the secret ones.
	Headers http.Header
	// ProviderKey is true when the call carried the provider's key as a
	// bearer token.
	ProviderKey bool
	// AgentToken is true when an agent's gateway token (ao1.) was in any
	// header or in the body.
	AgentToken bool
	// Tools are the names of the functions the call offered, sorted.
	Tools []string
	// ToolResults are the texts of the tool messages the call sent back.
	ToolResults []string
	// Prompt is the text of the call's last user message.
	Prompt string
	// Fields are the call's top-level request fields, sorted.
	Fields []string
	// Refused is the message of the 400 the fake answered, empty when it
	// answered the call.
	Refused string
	// Scripted is true when the answer was a scripted reply.
	Scripted bool
}

// FakeModel is an OpenAI-compatible model provider on a loopback listener,
// for WithModelProvider. It answers with the scripted replies in order, then
// with FakeModelReply, and keeps what it saw of each call.
//
// It holds calls to the sampling rules of Claude 4.7 and later behind
// Anthropic's OpenAI-compatible endpoint: a temperature below 1, a top_p
// other than 0.99, both together, or any top_k is refused with a 400 in the
// OpenAI error format.
type FakeModel struct {
	srv *httptest.Server
	key string

	mu     sync.Mutex
	script []ModelReply
	calls  []ModelCall
}

// NewFakeModel serves a fake model until the test ends. Its key is new for
// each test.
func NewFakeModel(t testing.TB) *FakeModel {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("make the fake model's key: %v", err)
	}
	m := &FakeModel{key: "fake-" + hex.EncodeToString(b)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", m.complete)
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// URL is the provider's OpenAI base URL, with its /v1.
func (m *FakeModel) URL() string { return m.srv.URL + "/v1" }

// Key is the provider key the fake model accepts.
func (m *FakeModel) Key() string { return m.key }

// Script sets the next answers, in order, replacing any left.
func (m *FakeModel) Script(replies ...ModelReply) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.script = slices.Clone(replies)
}

// Calls are the calls the fake model got, in order.
func (m *FakeModel) Calls() []ModelCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

type fakeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type fakeRequest struct {
	Model    string        `json:"model"`
	Stream   bool          `json:"stream"`
	Messages []fakeMessage `json:"messages"`
	Tools    []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func (m *FakeModel) complete(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	call := ModelCall{
		Path: r.URL.Path, Headers: r.Header.Clone(),
		ProviderKey: r.Header.Get("Authorization") == "Bearer "+m.key,
		AgentToken:  hasAgentToken(r.Header, body),
	}
	for _, h := range secretHeaders {
		call.Headers.Del(h)
	}
	var req fakeRequest
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	call.Model = req.Model
	for _, tool := range req.Tools {
		call.Tools = append(call.Tools, tool.Function.Name)
	}
	slices.Sort(call.Tools)
	for _, msg := range req.Messages {
		var text string
		if json.Unmarshal(msg.Content, &text) != nil {
			continue
		}
		switch msg.Role {
		case "tool":
			call.ToolResults = append(call.ToolResults, text)
		case "user":
			call.Prompt = text
		}
	}
	if err != nil || !call.ProviderKey {
		m.note(call)
		http.Error(w, "send a JSON chat completion with the provider key", http.StatusUnauthorized)
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields) // the body decoded above, so it is a JSON object
	call.Fields = slices.Sorted(maps.Keys(fields))
	if call.Refused = samplingRefusal(fields); call.Refused != "" {
		m.note(call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": call.Refused}})
		return
	}
	reply, scripted := m.next()
	call.Scripted = scripted
	m.note(call)
	if req.Stream {
		writeFakeStream(w, req.Model, reply)
		return
	}
	writeFakeJSON(w, fakeCompletion(req.Model, reply))
}

// samplingRefusal is the message Claude 4.7 and later refuse the call with,
// or "" when its sampling fields are fine.
func samplingRefusal(fields map[string]json.RawMessage) string {
	num := func(k string) (float64, bool) {
		raw, ok := fields[k]
		var v float64
		if ok && json.Unmarshal(raw, &v) != nil {
			v = -1
		}
		return v, ok
	}
	temp, hasTemp := num("temperature")
	topP, hasTopP := num("top_p")
	_, hasTopK := fields["top_k"]
	switch {
	case hasTemp && hasTopP:
		return "`temperature` and `top_p` cannot both be specified for this model."
	case hasTemp && temp < 1:
		return "temperature: only the default value 1 is supported for this model."
	case hasTopP && topP != 0.99:
		return "top_p: only the default value 0.99 is supported for this model."
	case hasTopK:
		return "top_k: not supported for this model."
	}
	return ""
}

func (m *FakeModel) note(c ModelCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c)
}

func (m *FakeModel) next() (ModelReply, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.script) == 0 {
		return ModelReply{Text: FakeModelReply}, false
	}
	r := m.script[0]
	m.script = m.script[1:]
	return r, true
}

func hasAgentToken(h http.Header, body []byte) bool {
	for _, vs := range h {
		for _, v := range vs {
			if strings.Contains(v, gatewaytoken.Prefix) {
				return true
			}
		}
	}
	return bytes.Contains(body, []byte(gatewaytoken.Prefix))
}

var fakeUsage = map[string]int{
	"prompt_tokens": fakePromptTokens, "completion_tokens": fakeCompletionTokens,
	"total_tokens": fakePromptTokens + fakeCompletionTokens,
}

func (r ModelReply) finish() string {
	if len(r.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

// toolCalls are the reply's tool calls in the chat-completions shape, with
// their index for a stream.
func (r ModelReply) toolCalls() []map[string]any {
	calls := make([]map[string]any, 0, len(r.ToolCalls))
	for i, c := range r.ToolCalls {
		calls = append(calls, map[string]any{
			"index": i, "id": c.ID, "type": "function",
			"function": map[string]string{"name": c.Name, "arguments": c.Arguments},
		})
	}
	return calls
}

func fakeCompletion(model string, r ModelReply) map[string]any {
	msg := map[string]any{"role": "assistant", "content": r.Text}
	if len(r.ToolCalls) > 0 {
		msg = map[string]any{"role": "assistant", "content": nil, "tool_calls": r.toolCalls()}
	}
	return map[string]any{
		"id": "chatcmpl-fake", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": r.finish()}},
		"usage":   fakeUsage,
	}
}

// writeFakeStream answers as an OpenAI event stream: the text or the tool
// calls, the finish, the usage and [DONE].
func writeFakeStream(w http.ResponseWriter, model string, r ModelReply) {
	w.Header().Set("Content-Type", "text/event-stream")
	delta := map[string]any{"role": "assistant", "content": r.Text}
	if len(r.ToolCalls) > 0 {
		delta = map[string]any{"role": "assistant", "tool_calls": r.toolCalls()}
	}
	chunk := func(choices []any) map[string]any {
		return map[string]any{"id": "chatcmpl-fake", "object": "chat.completion.chunk", "model": model, "choices": choices}
	}
	usage := chunk([]any{})
	usage["usage"] = fakeUsage
	for _, c := range []map[string]any{
		chunk([]any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}),
		chunk([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": r.finish()}}),
		usage,
	} {
		raw, err := json.Marshal(c)
		if err != nil {
			panic(err) // maps of plain values always encode
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return // the gateway went away; its call fails on its side
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func writeFakeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
