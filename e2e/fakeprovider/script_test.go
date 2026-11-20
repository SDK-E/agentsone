// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// closeBody closes the answer's body and fails the test when that fails.
func closeBody(t *testing.T, res *http.Response) {
	t.Helper()
	if err := res.Body.Close(); err != nil {
		t.Errorf("close the answer body: %v", err)
	}
}

const hireCall = `{"id":"call_hire","type":"function","function":{"name":"hire_agent","arguments":"{\"body\":{\"name\":\"scribe\"}}"}}`

const chatBody = `{"model":"m","messages":[{"role":"user","content":"hire a writer"}]}`

// putScript sends PUT /script and returns the status and the answer.
func putScript(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url+"/script", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, res)
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}

// scriptLeft is GET /script: how many scripted replies are not used yet.
func scriptLeft(t *testing.T, url string) int {
	t.Helper()
	res, err := http.Get(url + "/script")
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, res)
	var s struct {
		Left int `json:"left"`
	}
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s.Left
}

type toolAnswer struct {
	Choices []struct {
		Message struct {
			Role      string          `json:"role"`
			Content   *string         `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage usage `json:"usage"`
}

func TestScriptedRepliesAnswerChatCallsInOrderThenTheChatReply(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	if status, raw := putScript(t, srv.URL, `{"replies":[{"tool_calls":[`+hireCall+`]},{"content":"I hired scribe."}]}`); status != http.StatusNoContent {
		t.Fatalf("PUT /script = %d: %s", status, raw)
	}
	if got := scriptLeft(t, srv.URL); got != 2 {
		t.Fatalf("left after PUT = %d, want 2", got)
	}

	_, first := post(t, srv.URL, "Bearer k", chatBody)
	_, second := post(t, srv.URL, "Bearer k", chatBody)
	_, third := post(t, srv.URL, "Bearer k", chatBody)

	var a toolAnswer
	if err := json.Unmarshal(first, &a); err != nil || len(a.Choices) != 1 {
		t.Fatalf("first answer %s: %v", first, err)
	}
	c := a.Choices[0]
	var calls []map[string]any
	if err := json.Unmarshal(c.Message.ToolCalls, &calls); err != nil {
		t.Fatalf("tool calls %s: %v", c.Message.ToolCalls, err)
	}
	var want []map[string]any
	if err := json.Unmarshal([]byte("["+hireCall+"]"), &want); err != nil {
		t.Fatal(err)
	}
	if c.Message.Role != "assistant" || c.Message.Content != nil || c.FinishReason != "tool_calls" ||
		!jsonEqual(t, calls, want) || a.Usage != fixedUsage {
		t.Fatalf("first answer = %s", first)
	}
	if got := decode(t, second).Choices[0].Message.Content; got != "I hired scribe." {
		t.Fatalf("second answer = %s", second)
	}
	if got := decode(t, third).Choices[0].Message.Content; got != ChatReply {
		t.Fatalf("third answer = %s", third)
	}
	if got := scriptLeft(t, srv.URL); got != 0 {
		t.Fatalf("left after three calls = %d, want 0", got)
	}
	s := seen(t, srv.URL).Calls
	if len(s) != 3 || !s[0].Scripted || !s[1].Scripted || s[2].Scripted {
		t.Fatalf("calls = %+v, want the first two scripted", s)
	}
}

func TestAScriptedToolCallStreamsAsToolCallDeltas(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	putScript(t, srv.URL, `{"replies":[{"tool_calls":[`+hireCall+`]}]}`)

	res, raw := post(t, srv.URL, "Bearer k", `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	var events []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			events = append(events, data)
		}
	}
	if len(events) != 4 || events[3] != "[DONE]" {
		t.Fatalf("events = %q", events)
	}
	var first, stop struct {
		Choices []struct {
			Delta struct {
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(events[0]), &first); err != nil || len(first.Choices) != 1 {
		t.Fatalf("first event %s: %v", events[0], err)
	}
	tc := first.Choices[0].Delta.ToolCalls
	if len(tc) != 1 || tc[0].Index != 0 || tc[0].ID != "call_hire" || tc[0].Type != "function" ||
		tc[0].Function.Name != "hire_agent" || tc[0].Function.Arguments != `{"body":{"name":"scribe"}}` {
		t.Fatalf("first event = %s", events[0])
	}
	if err := json.Unmarshal([]byte(events[1]), &stop); err != nil || stop.Choices[0].FinishReason == nil ||
		*stop.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("stop event %s: %v", events[1], err)
	}
}

func TestRoutingPromptsLeaveTheScriptAlone(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	putScript(t, srv.URL, `{"replies":[{"content":"scripted"}]}`)

	_, routed := post(t, srv.URL, "Bearer k", routerBody)

	if !strings.Contains(decode(t, routed).Choices[0].Message.Content, RouteReason) {
		t.Fatalf("routing answer = %s", routed)
	}
	if got := scriptLeft(t, srv.URL); got != 1 {
		t.Fatalf("left after a routing prompt = %d, want 1", got)
	}
	if s := seen(t, srv.URL).Calls; len(s) != 1 || s[0].Scripted {
		t.Fatalf("calls = %+v, want one routing call, not scripted", s)
	}
}

func TestAPutReplacesTheScript(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	putScript(t, srv.URL, `{"replies":[{"content":"old one"},{"content":"old two"}]}`)
	putScript(t, srv.URL, `{"replies":[{"content":"new"}]}`)

	_, raw := post(t, srv.URL, "Bearer k", chatBody)

	if got := decode(t, raw).Choices[0].Message.Content; got != "new" || scriptLeft(t, srv.URL) != 0 {
		t.Fatalf("answer %s, left %d", raw, scriptLeft(t, srv.URL))
	}
}

func TestABadScriptIsRefusedAndKeepsTheOldOne(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	putScript(t, srv.URL, `{"replies":[{"content":"kept"}]}`)
	for _, body := range []string{
		`not json`,
		`{"replies":[{}]}`,
		`{"replies":[{"content":"both","tool_calls":[` + hireCall + `]}]}`,
		`{"replies":[{"tool_calls":[{"id":"","type":"function","function":{"name":"n","arguments":"{}"}}]}]}`,
		`{"replies":[{"tool_calls":[{"id":"c","type":"other","function":{"name":"n","arguments":"{}"}}]}]}`,
		`{"replies":[{"tool_calls":[{"id":"c","type":"function","function":{"name":"","arguments":"{}"}}]}]}`,
		`{"replies":[{"tool_calls":[{"id":"c","type":"function","function":{"name":"n","arguments":"not json"}}]}]}`,
		`{"replies":[],"extra":1}`,
	} {
		if status, raw := putScript(t, srv.URL, body); status != http.StatusBadRequest {
			t.Fatalf("PUT /script %s = %d %s, want 400", body, status, raw)
		}
	}
	if got := scriptLeft(t, srv.URL); got != 1 {
		t.Fatalf("left after bad scripts = %d, want 1", got)
	}
}

func TestTheNoteHoldsTheToolResultsACallSendsBack(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	long := strings.Repeat("x", maxToolResult+10)
	body := `{"model":"m","tools":[{"type":"function","function":{"name":"list_agents"}},` +
		`{"type":"function","function":{"name":"create_task"}}],"messages":[{"role":"user","content":"list"},` +
		`{"role":"assistant","content":null,"tool_calls":[` + hireCall + `]},` +
		`{"role":"tool","tool_call_id":"call_hire","content":"{\"items\":[{\"name\":\"founder\"}]}"},` +
		`{"role":"tool","tool_call_id":"call_long","content":"` + long + `"},` +
		`{"role":"tool","tool_call_id":"call_parts","content":[{"type":"text","text":"t"}]}]}`

	post(t, srv.URL, "Bearer k", body)
	post(t, srv.URL, "Bearer k", chatBody)

	calls := seen(t, srv.URL).Calls
	want := []ToolResult{
		{ToolCallID: "call_hire", Content: `{"items":[{"name":"founder"}]}`},
		{ToolCallID: "call_long", Content: long[:maxToolResult]},
		{ToolCallID: "call_parts", Content: `[{"type":"text","text":"t"}]`},
	}
	if len(calls) != 2 || !jsonEqual(t, calls[0].ToolResults, want) {
		t.Fatalf("tool results = %+v, want %+v", calls, want)
	}
	if !slices.Equal(calls[0].Tools, []string{"create_task", "list_agents"}) {
		t.Fatalf("tools = %v, want the offered names, sorted", calls[0].Tools)
	}
	if calls[1].ToolResults == nil || len(calls[1].ToolResults) != 0 || calls[1].Tools == nil || len(calls[1].Tools) != 0 {
		t.Fatalf("a call with no tools notes %v and %v, want empty lists", calls[1].ToolResults, calls[1].Tools)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ra, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(ra, rb)
}
