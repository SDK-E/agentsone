// SPDX-License-Identifier: MIT

package openai_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/runners/openai"
	"github.com/SDK-E/agentsone/runners/sdk"
)

func newClient(url string) *openai.Gateway {
	return &openai.Gateway{BaseURL: url, Token: "gw-token-1", Model: "autoagents/one", HTTP: http.DefaultClient}
}

func discard(string) error { return nil }

// TestGatewayStreamsText checks the request headers and body, the streamed
// deltas and the usage block (TESTING.md C1).
func TestGatewayStreamsText(t *testing.T) {
	gw := newGateway(t, func(gwRequest) reply { return reply{Text: []string{"Hel", "lo"}, Cached: 3} })
	var deltas []string
	comp, err := newClient(gw.URL()).Complete(context.Background(),
		openai.ChatRequest{Weight: "deep", Messages: []openai.Message{{Role: "user", Content: "hi"}}},
		func(s string) error { deltas = append(deltas, s); return nil })
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := strings.Join(deltas, "|"); got != "Hel|lo" || comp.Content != "Hello" {
		t.Fatalf("deltas %q content %q", got, comp.Content)
	}
	if comp.Usage.PromptTokens != 11 || comp.Usage.CompletionTokens != 7 || comp.Usage.PromptTokensDetails.CachedTokens != 3 || comp.Model != "autoagents/one-actual" {
		t.Fatalf("usage or model wrong: %+v", comp)
	}
	req := gw.requests()[0]
	if req.Auth != "Bearer gw-token-1" || req.Weight != "deep" || req.Model != "autoagents/one" || !req.Stream || !req.IncludeUsage || len(req.Tools) != 0 {
		t.Fatalf("request wrong: %+v", req)
	}
}

// TestGatewayAssemblesToolCalls checks that argument fragments join per index.
func TestGatewayAssemblesToolCalls(t *testing.T) {
	gw := newGateway(t, func(gwRequest) reply {
		return reply{Calls: []fakeCall{{ID: "c1", Name: "add", Args: `{"a":1,"b":2}`}, {ID: "c2", Name: "fail", Args: `{}`}}}
	})
	tools := []openai.ToolDef{{Type: "function", Function: openai.FunctionDef{Name: "add", Parameters: []byte(`{"type":"object"}`)}}}
	comp, err := newClient(gw.URL()+"/").Complete(context.Background(), openai.ChatRequest{Messages: []openai.Message{{Role: "user", Content: "x"}}, Tools: tools}, discard)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(comp.ToolCalls) != 2 || comp.ToolCalls[0].ID != "c1" || comp.ToolCalls[0].Function.Arguments != `{"a":1,"b":2}` || comp.ToolCalls[1].Function.Name != "fail" {
		t.Fatalf("tool calls wrong: %+v", comp.ToolCalls)
	}
	if got := gw.requests()[0].Tools; len(got) != 1 || got[0].Function.Name != "add" {
		t.Fatalf("tools not sent: %+v", got)
	}
}

// TestGatewayErrorMapping checks which error each fault becomes (PROTOCOL.md
// error codes 1010, 1020, 1030).
func TestGatewayErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		rep  reply
		code int
		is   error
	}{
		{"budget", reply{Status: http.StatusPaymentRequired}, 0, sdk.ErrBudgetExhausted},
		{"rate limit", reply{Status: http.StatusTooManyRequests}, sdk.CodeRateLimited, nil},
		{"server error", reply{Status: http.StatusBadGateway}, sdk.CodeModelUnavailable, nil},
		{"bad json", reply{Raw: "data: {nope\n\n"}, sdk.CodeModelUnavailable, nil},
		{"error chunk", reply{Raw: "data: {\"error\":{\"message\":\"overloaded\"}}\n\n"}, sdk.CodeModelUnavailable, nil},
		{"line too long", reply{Raw: "data: " + strings.Repeat("x", 5<<20) + "\n\n"}, sdk.CodeModelUnavailable, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := newGateway(t, func(gwRequest) reply { return tc.rep })
			_, err := newClient(gw.URL()).Complete(context.Background(), openai.ChatRequest{Messages: []openai.Message{{Role: "user", Content: "x"}}}, discard)
			var coded *sdk.CodedError
			switch {
			case tc.is != nil && !errors.Is(err, tc.is):
				t.Fatalf("got %v, want %v", err, tc.is)
			case tc.is == nil && (!errors.As(err, &coded) || coded.Code != tc.code):
				t.Fatalf("got %v, want code %d", err, tc.code)
			}
		})
	}
}

// TestGatewayUnreachable maps a dead gateway to 1020.
func TestGatewayUnreachable(t *testing.T) {
	gw := newGateway(t, func(gwRequest) reply { return reply{} })
	url := gw.URL()
	gw.srv.Close()
	_, err := newClient(url).Complete(context.Background(), openai.ChatRequest{Messages: []openai.Message{{Role: "user", Content: "x"}}}, discard)
	var coded *sdk.CodedError
	if !errors.As(err, &coded) || coded.Code != sdk.CodeModelUnavailable {
		t.Fatalf("got %v, want a 1020 error", err)
	}
}

// TestGatewayCancel returns the context error when the caller cancels, both
// while connecting and while the stream is open.
func TestGatewayCancel(t *testing.T) {
	gw := newGateway(t, func(gwRequest) reply { return reply{Hold: true} })
	ctx, cancel := context.WithCancel(context.Background())
	_, err := newClient(gw.URL()).Complete(ctx, openai.ChatRequest{Messages: []openai.Message{{Role: "user", Content: "x"}}},
		func(string) error { cancel(); return nil }) // cancel on the first delta
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("open stream: got %v, want context.Canceled", err)
	}
	_, err = newClient(gw.URL()).Complete(ctx, openai.ChatRequest{Messages: []openai.Message{{Role: "user", Content: "x"}}}, discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("closed context: got %v, want context.Canceled", err)
	}
}

// TestGatewayDeltaError stops reading when the delta callback fails.
func TestGatewayDeltaError(t *testing.T) {
	gw := newGateway(t, func(gwRequest) reply { return reply{Text: []string{"a", "b"}} })
	stop := errors.New("stop")
	_, err := newClient(gw.URL()).Complete(context.Background(), openai.ChatRequest{Messages: []openai.Message{{Role: "user", Content: "x"}}},
		func(string) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("got %v, want the callback error", err)
	}
}
