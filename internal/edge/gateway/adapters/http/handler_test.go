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
	return signed(t, gatewaytoken.Claims{
		Agent: "worker", Kind: gatewaytoken.KindAlwaysOn, Chart: "founder/worker", Defence: defence,
		Audience: gatewaytoken.AudienceGateway, IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: exp.Unix(),
	})
}

func signed(t *testing.T, c gatewaytoken.Claims) string {
	t.Helper()
	tok, err := gatewaytoken.Sign(key, c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// records keeps the call records the handler writes.
type records struct {
	mu  sync.Mutex
	got []gatewayhttp.CallRecord
}

func (r *records) add(_ context.Context, c gatewayhttp.CallRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, c)
}

func (r *records) all() []gatewayhttp.CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gatewayhttp.CallRecord(nil), r.got...)
}

func server(t *testing.T, r *fakeRouter) *httptest.Server {
	t.Helper()
	return serverWithFreeze(t, r, func(context.Context) (bool, error) { return false, nil })
}

func serverWithFreeze(t *testing.T, r *fakeRouter, frozen func(context.Context) (bool, error)) *httptest.Server {
	t.Helper()
	return serverWith(t, r, frozen, nil)
}

func serverWith(t *testing.T, r *fakeRouter, frozen func(context.Context) (bool, error), rec *records) *httptest.Server {
	t.Helper()
	o := gatewayhttp.Options{
		Router:     r,
		Key:        func() []byte { return key },
		Clock:      fakeClock{},
		RetryAfter: 30 * time.Second,
		Frozen:     frozen,
	}
	if rec != nil {
		o.Record = rec.add
	}
	srv := httptest.NewServer(gatewayhttp.New(o))
	t.Cleanup(srv.Close)
	return srv
}

func TestAudience_RefusesATokenForTheAuditSinkOnEveryRoute(t *testing.T) {
	t.Parallel()
	r := &fakeRouter{}
	answer(r, 200, "application/json", `{}`)
	srv := server(t, r)
	tok := signed(t, gatewaytoken.Claims{
		Agent: "system:router", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceAudit,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix(),
	})

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/chat/completions"}, {http.MethodPost, "/anthropic/v1/messages"},
		{http.MethodPost, "/anthropic/v1/messages/count_tokens"}, {http.MethodGet, "/v1/models"},
		{http.MethodGet, "/v1/budget"},
	} {
		got := do(t, srv, c.method, c.path, `{"model":"autoagents/one"}`, map[string]string{"Authorization": "Bearer " + tok})
		var p map[string]any
		_ = json.NewDecoder(got.Body).Decode(&p)
		if got.StatusCode != http.StatusForbidden || p["type"] != gatewayhttp.ProblemWrongAudience {
			t.Errorf("%s %s with an audit token = %d %v, want 403 %s", c.method, c.path, got.StatusCode, p["type"], gatewayhttp.ProblemWrongAudience)
		}
	}
	if len(r.got) != 0 || len(r.counts) != 0 {
		t.Errorf("the router was called %d times with an audit token", len(r.got)+len(r.counts))
	}
}

func TestSystemCallers_CallModelsOnTheSystemLine(t *testing.T) {
	t.Parallel()
	r := &fakeRouter{}
	answer(r, 200, "application/json", `{}`)
	rec := &records{}
	srv := serverWith(t, r, func(context.Context) (bool, error) { return false, nil }, rec)
	tok := signed(t, gatewaytoken.Claims{
		Agent: "system:router", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceGateway,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix(),
	})

	got := do(t, srv, http.MethodPost, "/v1/chat/completions", `{"model":"autoagents/one"}`, map[string]string{"Authorization": "Bearer " + tok})

	if got.StatusCode != http.StatusOK || len(r.got) != 1 || r.got[0].Caller.Agent != "system" {
		t.Fatalf("status %d, calls %+v; want one call booked on the system line", got.StatusCode, r.got)
	}
	if all := rec.all(); len(all) != 1 || all[0].Agent != "system:router" || all[0].Kind != "system" {
		t.Fatalf("records = %+v, want the system caller named", all)
	}
}

func TestRecord_WritesOneRecordPerModelCall(t *testing.T) {
	t.Parallel()
	r := &fakeRouter{}
	answer(r, 200, "application/json", `{"ok":true}`)
	r.result.Spend = &gatewayapp.Spend{Usage: routing.Usage{Input: 120, Output: 30}, Reported: true, Cost: 4500}
	rec := &records{}
	srv := serverWith(t, r, func(context.Context) (bool, error) { return false, nil }, rec)

	got := do(t, srv, http.MethodPost, "/v1/chat/completions", `{"model":"autoagents/one"}`, bearer(t))
	_, _ = io.ReadAll(got.Body)

	want := gatewayhttp.CallRecord{
		Agent: "worker", Kind: "always-on", Chart: "founder/worker", Model: "openai/gpt-mid",
		Status: 200, InputTokens: 120, OutputTokens: 30, CostMicros: 4500,
	}
	if all := rec.all(); len(all) != 1 || all[0] != want {
		t.Fatalf("records = %+v, want [%+v]", all, want)
	}
	if want.Outcome() != "success" {
		t.Errorf("Outcome() = %q, want success", want.Outcome())
	}
}

func TestRecord_WritesRefusalsAndFailuresToo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		frozen  bool
		err     error
		status  int
		outcome string
	}{
		{"budget spent", false, ledger.ErrBudgetExhausted, http.StatusPaymentRequired, "denied"},
		{"frozen", true, nil, http.StatusLocked, "denied"},
		{"providers failed", false, &gatewayapp.UpstreamError{Status: 500, Err: errors.New("down")}, http.StatusBadGateway, "failure"},
	} {
		r := &fakeRouter{err: tc.err}
		rec := &records{}
		frozen := tc.frozen
		srv := serverWith(t, r, func(context.Context) (bool, error) { return frozen, nil }, rec)

		_ = do(t, srv, http.MethodPost, "/anthropic/v1/messages", `{"model":"autoagents/one"}`, bearer(t))

		all := rec.all()
		if len(all) != 1 || all[0].Status != tc.status || all[0].Outcome() != tc.outcome || all[0].Agent != "worker" {
			t.Errorf("%s: records = %+v, want one %d %s", tc.name, all, tc.status, tc.outcome)
		}
	}
}

func TestRecord_WritesNothingForReadsOrABadToken(t *testing.T) {
	t.Parallel()
	rec := &records{}
	srv := serverWith(t, &fakeRouter{}, func(context.Context) (bool, error) { return false, nil }, rec)

	_ = do(t, srv, http.MethodGet, "/v1/models", "", bearer(t))
	_ = do(t, srv, http.MethodGet, "/v1/budget", "", bearer(t))
	_ = do(t, srv, http.MethodPost, "/v1/chat/completions", `{}`, map[string]string{"Authorization": "Bearer forged"})

	if all := rec.all(); len(all) != 0 {
		t.Fatalf("records = %+v, want none", all)
	}
}

func TestFreeze_RefusesEveryModelCallWith423(t *testing.T) {
	t.Parallel()
	for name, frozen := range map[string]func(context.Context) (bool, error){
		"frozen":                 func(context.Context) (bool, error) { return true, nil },
		"switch cannot be read":  func(context.Context) (bool, error) { return false, errors.New("disk gone") },
		"no switch given at all": nil,
	} {
		r := &fakeRouter{}
		srv := serverWithFreeze(t, r, frozen)
		auth := map[string]string{"Authorization": "Bearer " + token(t, false, now.Add(10*time.Minute))}
		for _, path := range []string{"/v1/chat/completions", "/anthropic/v1/messages", "/anthropic/v1/messages/count_tokens"} {
			got := do(t, srv, http.MethodPost, path, `{"model":"autoagents/one"}`, auth)
			var p map[string]any
			_ = json.NewDecoder(got.Body).Decode(&p)
			if got.StatusCode != http.StatusLocked || got.Header.Get("Content-Type") != "application/problem+json" ||
				got.Header.Get("Cache-Control") != "no-store" || got.Header.Get("Retry-After") != "" ||
				p["type"] != "urn:agentsone:problem:frozen" || p["title"] != "Frozen" || p["status"] != float64(423) ||
				p["detail"] != "A human froze all agents. Model calls are off until a human releases the freeze." {
				t.Errorf("%s %s: %d %v %v", name, path, got.StatusCode, got.Header, p)
			}
		}
		if len(r.got)+len(r.counts) != 0 {
			t.Errorf("%s: %d calls reached the router", name, len(r.got)+len(r.counts))
		}
	}
}

func TestFreeze_StillChecksTheTokenFirst(t *testing.T) {
	t.Parallel()
	srv := serverWithFreeze(t, &fakeRouter{}, func(context.Context) (bool, error) { return true, nil })

	got := do(t, srv, http.MethodPost, "/v1/chat/completions", `{"model":"autoagents/one"}`, nil)

	if got.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 before the freeze answer", got.StatusCode)
	}
}

func TestFreeze_LeavesReadsOpen(t *testing.T) {
	t.Parallel()
	srv := serverWithFreeze(t, &fakeRouter{}, func(context.Context) (bool, error) { return true, nil })
	auth := map[string]string{"Authorization": "Bearer " + token(t, false, now.Add(10*time.Minute))}

	for _, path := range []string{"/v1/models", "/v1/budget"} {
		if got := do(t, srv, http.MethodGet, path, "", auth); got.StatusCode != http.StatusOK {
			t.Errorf("GET %s while frozen = %d, want 200", path, got.StatusCode)
		}
	}
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
	return map[string]string{"Authorization": "Bearer " + token(t, false, now.Add(10*time.Minute))}
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
		map[string]string{"X-Api-Key": token(t, true, now.Add(10*time.Minute)), "Anthropic-Version": "2026-10-21"})
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
		map[string]string{"X-Api-Key": token(t, false, now.Add(10*time.Minute)), "Anthropic-Beta": "b1"})
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
				hdr["Authorization"] = "Bearer " + token(t, false, now.Add(10*time.Minute))
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
