// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

const routerBody = `{"model":"fake-quick","max_tokens":600,"messages":[` +
	`{"role":"system","content":"You are a router. You match one human request to the agent that fits it best."},` +
	`{"role":"user","content":"{\"request\":\"write the docs\",\"candidates\":[{\"agent\":\"waker\"},{\"agent\":\"founder\"}],\"routers\":[]}"}]}`

func newServer(t *testing.T) (*httptest.Server, *Provider) {
	t.Helper()
	p := &Provider{}
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return srv, p
}

// reply is a provider answer, read to the end.
type reply struct {
	StatusCode int
	Header     http.Header
}

func post(t *testing.T, url, auth, body string) (reply, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{StatusCode: res.StatusCode, Header: res.Header}, raw
}

func seen(t *testing.T, url string) Seen {
	t.Helper()
	res, err := http.Get(url + "/seen")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var s Seen
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type answer struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage usage `json:"usage"`
}

func decode(t *testing.T, raw []byte) answer {
	t.Helper()
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("answer %s: %v", raw, err)
	}
	if len(a.Choices) != 1 {
		t.Fatalf("answer %s has %d choices, want 1", raw, len(a.Choices))
	}
	return a
}

func TestRoutingPromptPicksTheFirstCandidate(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

	res, raw := post(t, srv.URL, "Bearer provider-key", routerBody)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	a := decode(t, raw)
	if a.Model != "fake-quick" || a.Usage != fixedUsage || a.Choices[0].Message.Role != "assistant" {
		t.Fatalf("answer = %+v", a)
	}
	var v struct {
		Ratings []rating        `json:"ratings"`
		Handoff json.RawMessage `json:"handoff"`
	}
	if err := json.Unmarshal([]byte(a.Choices[0].Message.Content), &v); err != nil {
		t.Fatalf("verdict %q: %v", a.Choices[0].Message.Content, err)
	}
	want := []rating{{Agent: "waker", Score: 1, Reason: RouteReason}, {Agent: "founder", Score: 0, Reason: OtherReason}}
	if len(v.Ratings) != 2 || v.Ratings[0] != want[0] || v.Ratings[1] != want[1] || string(v.Handoff) != "null" {
		t.Fatalf("verdict = %+v handoff %s", v.Ratings, v.Handoff)
	}
}

func TestOtherCallsGetTheChatReply(t *testing.T) {
	t.Parallel()
	srv, p := newServer(t)
	for _, body := range []string{
		`{"model":"m","messages":[{"role":"user","content":"hello"}]}`,
		`{"model":"m","messages":[{"role":"system","content":"You are a helpful agent."},{"role":"user","content":"hi"}]}`,
		// A router prompt whose user message is not the routing JSON is a plain chat.
		`{"model":"m","messages":[{"role":"system","content":"You are a router."},{"role":"user","content":"not json"}]}`,
		`{"model":"m","messages":[{"role":"system","content":[{"type":"text","text":"parts"}]},{"role":"user","content":"x"}]}`,
	} {
		res, raw := post(t, srv.URL, "Bearer k", body)
		if res.StatusCode != http.StatusOK || decode(t, raw).Choices[0].Message.Content != ChatReply {
			t.Fatalf("body %s: status %d answer %s", body, res.StatusCode, raw)
		}
	}
	for _, c := range seen(t, srv.URL).Calls {
		if c.Kind != KindChat {
			t.Fatalf("call %+v, want kind chat", c)
		}
	}
	if len(p.calls) != 4 {
		t.Fatalf("calls = %d, want 4", len(p.calls))
	}
}

func TestSeenNotesTheKeyHashAndNeverTheKey(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	if got := seen(t, srv.URL).Calls; got == nil || len(got) != 0 {
		t.Fatalf("calls before any call = %#v, want an empty list", got)
	}

	post(t, srv.URL, "Bearer provider-key-123", routerBody)

	res, err := http.Get(srv.URL + "/seen")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("provider-key-123")) {
		t.Fatalf("/seen shows the key: %s", raw)
	}
	var s Seen
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	c := s.Calls[0]
	if len(s.Calls) != 1 || c.Kind != KindRoute || c.Path != "/v1/chat/completions" || c.Model != "fake-quick" ||
		c.AuthSHA256 != sha("Bearer provider-key-123") || c.GatewayToken || c.Stream {
		t.Fatalf("calls = %+v", s.Calls)
	}
	if strings.Join(c.Headers, ",") != "Accept-Encoding,Authorization,Content-Length,Content-Type,User-Agent" {
		t.Fatalf("headers = %v", c.Headers)
	}
}

func TestAGatewayTokenAnywhereIsNoted(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

	post(t, srv.URL, "Bearer ao1.claims.mac", `{"model":"m","messages":[]}`)
	post(t, srv.URL, "Bearer k", `{"model":"m","messages":[{"role":"user","content":"my token is ao1.x.y"}]}`)
	post(t, srv.URL, "Bearer k", `{"model":"m","messages":[{"role":"user","content":"nothing here"}]}`)

	calls := seen(t, srv.URL).Calls
	if len(calls) != 3 || !calls[0].GatewayToken || !calls[1].GatewayToken || calls[2].GatewayToken {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestCallsWithoutABearerOrJSONAreRefusedAndNoted(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

	noAuth, _ := post(t, srv.URL, "", `{"model":"m","messages":[]}`)
	basic, _ := post(t, srv.URL, "Basic a2V5", `{"model":"m","messages":[]}`)
	notJSON, _ := post(t, srv.URL, "Bearer k", `not json`)

	if noAuth.StatusCode != http.StatusUnauthorized || basic.StatusCode != http.StatusUnauthorized ||
		notJSON.StatusCode != http.StatusBadRequest {
		t.Fatalf("statuses %d %d %d", noAuth.StatusCode, basic.StatusCode, notJSON.StatusCode)
	}
	calls := seen(t, srv.URL).Calls
	if len(calls) != 3 || calls[0].AuthSHA256 != "" || calls[1].AuthSHA256 != sha("Basic a2V5") {
		t.Fatalf("calls = %+v", calls)
	}
	for _, c := range calls {
		if c.Kind != KindRefused {
			t.Fatalf("call %+v, want kind refused", c)
		}
	}
}

func TestStreamedCallsEndWithUsageAndDone(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)

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
	var first, last struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(events[0]), &first); err != nil || first.Choices[0].Delta.Content != ChatReply {
		t.Fatalf("first event %s: %v", events[0], err)
	}
	if err := json.Unmarshal([]byte(events[2]), &last); err != nil || last.Usage == nil || *last.Usage != fixedUsage {
		t.Fatalf("usage event %s: %v", events[2], err)
	}
	if c := seen(t, srv.URL).Calls; len(c) != 1 || !c[0].Stream || c[0].Kind != KindChat {
		t.Fatalf("calls = %+v", c)
	}
}

func TestProbe(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	var out bytes.Buffer

	if got := run([]string{"probe", srv.URL + "/healthz"}, &out); got != 0 {
		t.Fatalf("probe healthy = %d: %s", got, out.String())
	}
	if got := run([]string{"probe", broken.URL}, &out); got != 1 {
		t.Fatalf("probe broken = %d, want 1", got)
	}
	if got := run([]string{"probe", "http://127.0.0.1:1/"}, &out); got != 1 {
		t.Fatalf("probe closed port = %d, want 1", got)
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"serve", "a", "b"}, {"probe"}, {"other"}} {
		var out bytes.Buffer
		if got := run(args, &out); got != 2 || !strings.Contains(out.String(), "usage:") {
			t.Fatalf("run(%q) = %d %q", args, got, out.String())
		}
	}
}

func TestServeStopsCleanlyWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- serve(ctx, addr, &out) }()

	deadline := time.Now().Add(5 * time.Second)
	for run([]string{"probe", "http://" + addr + "/healthz"}, io.Discard) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fake provider did not start")
		}
		<-time.After(20 * time.Millisecond)
	}
	cancel()

	if got := <-done; got != 0 {
		t.Fatalf("serve = %d: %s", got, out.String())
	}
}

// The e2e expects every fake answer to cost exactly 1100 micro-dollars through this config.
func TestGatewayConfigPointsAtTheFakeAndPricesItsAnswer(t *testing.T) {
	t.Parallel()
	f, err := gatewayconfig.Load("gateway.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Providers) != 1 {
		t.Fatalf("providers = %+v, want only the fake", f.Providers)
	}
	p := f.Providers[0]
	if p.ID != "e2e-fake" || p.Enabled || !p.KeyRequired || p.OpenAIBaseURL != "http://fakeprovider:8080/v1" {
		t.Fatalf("provider = %+v", p)
	}
	cat, err := f.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	tiers := map[routing.Weight]bool{}
	for _, m := range cat.Models {
		tiers[m.Tier] = true
		if got := m.Cost(routing.Usage{Input: PromptTokens, Output: CompletionTokens}); got != 1100 {
			t.Fatalf("model %s: a fake answer costs %d micro-dollars, want 1100", m.ID, got)
		}
	}
	if !tiers[routing.Quick] || !tiers[routing.Normal] || !tiers[routing.Deep] {
		t.Fatalf("tiers = %v, want quick, normal and deep", tiers)
	}
}

func TestServeFailsOnABusyAddress(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var out bytes.Buffer

	if got := serve(context.Background(), ln.Addr().String(), &out); got != 1 {
		t.Fatalf("serve on a busy address = %d, want 1", got)
	}
}
