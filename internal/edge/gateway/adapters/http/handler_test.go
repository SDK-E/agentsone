// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	gatewayhttp "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/http"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

var (
	key = []byte("test-boot-secret-for-handler-tests")
	now = time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
)

type fakeClock struct{}

func (fakeClock) Now() time.Time { return now }

// fakeRouter records calls and answers with what it was given.
type fakeRouter struct {
	mu     sync.Mutex
	got    []gatewayapp.CallRequest
	counts []gatewayapp.CountRequest
	result *gatewayapp.CallResult
	err    error
	view   gatewayapp.BudgetView
	closed bool
}

type closeRecorder struct {
	io.Reader
	r *fakeRouter
}

func (c closeRecorder) Close() error {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	c.r.closed = true
	return nil
}

func (f *fakeRouter) Call(_ context.Context, req gatewayapp.CallRequest) (*gatewayapp.CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, req)
	return f.result, f.err
}

func (f *fakeRouter) CountTokens(_ context.Context, req gatewayapp.CountRequest) (*gatewayapp.CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts = append(f.counts, req)
	return f.result, f.err
}

func (f *fakeRouter) Budget(c ledger.Caller) gatewayapp.BudgetView {
	v := f.view
	v.Defence = c.Defence
	return v
}

func answer(r *fakeRouter, status int, contentType, body string) {
	r.result = &gatewayapp.CallResult{
		Model:  routing.Model{Provider: "openai", ID: "gpt-mid"},
		Status: status,
		Header: http.Header{"Content-Type": {contentType}, "Set-Cookie": {"secret=1"}},
		Body:   closeRecorder{Reader: strings.NewReader(body), r: r},
	}
}

func token(t *testing.T, defence bool, exp time.Time) string {
	t.Helper()
	tok, err := gatewaytoken.Sign(key, gatewaytoken.Claims{
		Agent: "worker", Kind: gatewaytoken.KindAlwaysOn, Team: "t", Defence: defence,
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: exp.Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func server(t *testing.T, r *fakeRouter) *httptest.Server {
	t.Helper()
	h := gatewayhttp.New(gatewayhttp.Options{
		Router:     r,
		Key:        func() []byte { return key },
		Clock:      fakeClock{},
		RetryAfter: 30 * time.Second,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// reply is a fully read answer.
type reply struct {
	StatusCode int
	Header     http.Header
	Body       *bytes.Reader
}

func do(t *testing.T, srv *httptest.Server, method, path, body string, hdr map[string]string) *reply {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return &reply{StatusCode: resp.StatusCode, Header: resp.Header, Body: bytes.NewReader(b)}
}

func bearer(t *testing.T) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token(t, false, now.Add(time.Hour))}
}

func problem(t *testing.T, resp *reply) map[string]any {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q, want application/problem+json", ct)
	}
	var p map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	return p
}

func TestChatCompletionsRoutesAndPassesAnswerThrough(t *testing.T) {
	r := &fakeRouter{}
	answer(r, 200, "application/json", `{"choices":[]}`)
	srv := server(t, r)

	hdr := bearer(t)
	hdr["X-Agentsone-Weight"] = "deep"
	resp := do(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"autoagents/one","max_completion_tokens":77,"messages":[]}`, hdr)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != `{"choices":[]}` {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("X-Agentsone-Model") != "openai/gpt-mid" {
		t.Fatalf("headers = %v", resp.Header)
	}
	got := r.got[0]
	if got.Engine != routing.APIOpenAI || got.Weight != routing.Deep || got.MaxOutputTokens != 77 ||
		got.Caller.Agent != "worker" || got.Caller.Defence || got.Stream {
		t.Fatalf("call request = %+v", got)
	}
	if !r.closed {
		t.Fatal("answer body not closed, so the call was never settled")
	}
}

func TestStreamingIsFlushedThrough(t *testing.T) {
	r := &fakeRouter{}
	stream := "data: {\"a\":1}\n\ndata: [DONE]\n\n"
	answer(r, 200, "text/event-stream", stream)
	srv := server(t, r)
	resp := do(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"autoagents/one:quick","stream":true,"max_tokens":5}`, bearer(t))
	body, _ := io.ReadAll(resp.Body)
	if string(body) != stream || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("body %q headers %v", body, resp.Header)
	}
	if got := r.got[0]; !got.Stream || got.Weight != routing.Quick || got.MaxOutputTokens != 5 {
		t.Fatalf("call request = %+v", got)
	}
}

func TestAnthropicEndpointAcceptsAPIKeyHeader(t *testing.T) {
	r := &fakeRouter{}
	answer(r, 200, "application/json", `{"type":"message"}`)
	srv := server(t, r)
	resp := do(t, srv, http.MethodPost, "/anthropic/v1/messages",
		`{"model":"autoagents/one","max_tokens":256,"messages":[]}`,
		map[string]string{"X-Api-Key": token(t, true, now.Add(time.Hour)), "Anthropic-Version": "2026-10-21"})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := r.got[0]
	if got.Engine != routing.APIAnthropic || !got.Caller.Defence || got.MaxOutputTokens != 256 ||
		got.Header.Get("Anthropic-Version") != "2026-10-21" {
		t.Fatalf("call request = %+v", got)
	}
}

func TestCountTokensPassesThrough(t *testing.T) {
	r := &fakeRouter{}
	answer(r, 200, "application/json", `{"input_tokens":99}`)
	srv := server(t, r)
	resp := do(t, srv, http.MethodPost, "/anthropic/v1/messages/count_tokens",
		`{"model":"autoagents/one:deep","messages":[]}`,
		map[string]string{"X-Api-Key": token(t, false, now.Add(time.Hour)), "Anthropic-Beta": "b1"})
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != `{"input_tokens":99}` {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if len(r.got) != 0 || len(r.counts) != 1 {
		t.Fatalf("calls %d counts %d; counting must not be a paid call", len(r.got), len(r.counts))
	}
	if c := r.counts[0]; c.Weight != routing.Deep || c.Header.Get("Anthropic-Beta") != "b1" {
		t.Fatalf("count request = %+v", c)
	}
	if !r.closed {
		t.Fatal("answer body not closed")
	}
}

func TestCountTokensRefusals(t *testing.T) {
	srv := server(t, &fakeRouter{err: gatewayapp.ErrNoRoute})
	if resp := do(t, srv, http.MethodPost, "/anthropic/v1/messages/count_tokens", `{"model":"autoagents/one"}`, nil); resp.StatusCode != 401 {
		t.Fatalf("no token: status %d", resp.StatusCode)
	}
	if resp := do(t, srv, http.MethodPost, "/anthropic/v1/messages/count_tokens", `{"model":"claude-x"}`, bearer(t)); resp.StatusCode != 400 {
		t.Fatalf("bad model: status %d", resp.StatusCode)
	}
	if resp := do(t, srv, http.MethodPost, "/anthropic/v1/messages/count_tokens", `{"model":"autoagents/one"}`, bearer(t)); resp.StatusCode != 503 {
		t.Fatalf("no route: status %d", resp.StatusCode)
	}
}

func TestModelsListsOnlyThePublicName(t *testing.T) {
	srv := server(t, &fakeRouter{})
	resp := do(t, srv, http.MethodGet, "/v1/models", "", bearer(t))
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list.Object != "list" || len(list.Data) != 1 || list.Data[0].ID != "autoagents/one" {
		t.Fatalf("models = %+v", list)
	}
}

func TestRefusals(t *testing.T) {
	expired := token(t, false, now)
	tests := []struct {
		name      string
		path      string
		body      string
		hdr       map[string]string
		routerErr error
		status    int
		ptype     string
	}{
		{"no token", "/v1/chat/completions", `{"model":"autoagents/one"}`, nil, nil, 401, "urn:agentsone:problem:token-invalid"},
		{"expired token", "/v1/chat/completions", `{"model":"autoagents/one"}`, map[string]string{"Authorization": "Bearer " + expired}, nil, 401, "urn:agentsone:problem:token-invalid"},
		{"forged token", "/v1/models", "", map[string]string{"Authorization": "Bearer ao1.e30.AAAA"}, nil, 401, "urn:agentsone:problem:token-invalid"},
		{"other model", "/v1/chat/completions", `{"model":"gpt-4o"}`, nil, nil, 400, "urn:agentsone:problem:unknown-model"},
		{"bad weight", "/v1/chat/completions", `{"model":"autoagents/one"}`, map[string]string{"X-Agentsone-Weight": "huge"}, nil, 400, "urn:agentsone:problem:bad-weight"},
		{"bad json", "/anthropic/v1/messages", `{`, nil, nil, 400, "urn:agentsone:problem:bad-request"},
		{"budget", "/v1/chat/completions", `{"model":"autoagents/one"}`, nil, ledger.ErrBudgetExhausted, 402, "urn:agentsone:problem:budget-exhausted"},
		{"allocation", "/v1/chat/completions", `{"model":"autoagents/one"}`, nil, ledger.ErrAllocationExhausted, 402, "urn:agentsone:problem:budget-exhausted"},
		{"no route", "/anthropic/v1/messages", `{"model":"autoagents/one"}`, nil, gatewayapp.ErrNoRoute, 503, "urn:agentsone:problem:model-unavailable"},
		{"upstream", "/v1/chat/completions", `{"model":"autoagents/one"}`, nil, &gatewayapp.UpstreamError{Status: 500}, 502, "urn:agentsone:problem:upstream-failed"},
		{"other", "/v1/chat/completions", `{"model":"autoagents/one"}`, nil, errors.New("boom"), 500, "urn:agentsone:problem:internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRouter{err: tc.routerErr}
			srv := server(t, r)
			hdr := tc.hdr
			if hdr == nil {
				hdr = map[string]string{}
			}
			if _, set := hdr["Authorization"]; !set && tc.name != "no token" {
				hdr["Authorization"] = "Bearer " + token(t, false, now.Add(time.Hour))
			}
			method := http.MethodPost
			if tc.path == "/v1/models" {
				method = http.MethodGet
			}
			resp := do(t, srv, method, tc.path, tc.body, hdr)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			p := problem(t, resp)
			if p["type"] != tc.ptype || p["status"] != float64(tc.status) {
				t.Fatalf("problem = %v", p)
			}
			if tc.status == 402 {
				if resp.Header.Get("Retry-After") != "30" || p["retry_after_seconds"] != float64(30) {
					t.Fatalf("Retry-After = %q, problem %v", resp.Header.Get("Retry-After"), p)
				}
			}
		})
	}
}

func TestBodyTooLarge(t *testing.T) {
	srv := server(t, &fakeRouter{})
	big := `{"model":"autoagents/one","x":"` + strings.Repeat("a", gatewayhttp.MaxRequestBytes) + `"}`
	resp := do(t, srv, http.MethodPost, "/v1/chat/completions", big, bearer(t))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestBudgetEndpointLetsRunnersPoll(t *testing.T) {
	r := &fakeRouter{view: gatewayapp.BudgetView{Exhausted: true}}
	srv := server(t, r)
	resp := do(t, srv, http.MethodGet, "/v1/budget", "", bearer(t))
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v["exhausted"] != true || v["remaining_micro_usd"] != float64(0) || v["identity"] != "soft" ||
		v["retry_after_seconds"] != float64(30) || v["agent"] != "worker" {
		t.Fatalf("budget = %v", v)
	}
	r.view = gatewayapp.BudgetView{Remaining: 2_500_000}
	resp = do(t, srv, http.MethodGet, "/v1/budget", "", bearer(t))
	v = map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&v)
	if v["exhausted"] != false || v["remaining_usd"] != "$2.50" {
		t.Fatalf("budget = %v", v)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	srv := server(t, &fakeRouter{})
	resp := do(t, srv, http.MethodPost, "/v1/completions", `{}`, bearer(t))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
