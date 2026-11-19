// SPDX-License-Identifier: MIT

package claude

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/runners/sdk"
)

const (
	gatewayToken = "gateway-token-0123456789abcdef"
	mcpToken     = "mcp-token-0123456789abcdef"
)

// tokens is a token source the test can swap.
type tokens struct {
	mu           sync.Mutex
	gateway, mcp sdk.Secret
}

func (t *tokens) GatewayToken() sdk.Secret {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.gateway
}

func (t *tokens) MCPToken() sdk.Secret {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mcp
}

func (t *tokens) swap(gateway, mcp string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gateway, t.mcp = sdk.Secret(gateway), sdk.Secret(mcp)
}

// seen is one request an upstream got.
type seen struct {
	Method, Path, Query string
	Header              http.Header
	Body                string
}

// upstream is a fake gateway or MCP server that records its requests.
type upstream struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []seen
}

func newUpstream(t *testing.T, h http.HandlerFunc) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.reqs = append(u.reqs, seen{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body)})
		u.mu.Unlock()
		if h != nil {
			h(w, r)
			return
		}
		w.Header().Set("X-Agentsone-Model", "test-model")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) requests() []seen {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seen(nil), u.reqs...)
}

type proxyRig struct {
	p       *proxy
	gateway *upstream
	mcp     *upstream
	tokens  *tokens
	logs    *strings.Builder
	logMu   *sync.Mutex
}

func newProxyRig(t *testing.T, gw http.HandlerFunc) *proxyRig {
	t.Helper()
	r := &proxyRig{
		gateway: newUpstream(t, gw), mcp: newUpstream(t, nil),
		tokens: &tokens{gateway: gatewayToken, mcp: mcpToken},
		logs:   &strings.Builder{}, logMu: &sync.Mutex{},
	}
	p, err := startProxy(proxyConfig{
		ModelBaseURL: r.gateway.URL + "/v1", MCPURL: r.mcp.URL + "/mcp", Tokens: r.tokens,
		Log: func(msg string) {
			r.logMu.Lock()
			defer r.logMu.Unlock()
			r.logs.WriteString(msg + "\n")
		},
	})
	if err != nil {
		t.Fatalf("startProxy() error = %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	r.p = p
	return r
}

// answer is a proxy response, read in full.
type answer struct {
	StatusCode int
	Header     http.Header
}

func (r *proxyRig) do(t *testing.T, method, path, auth string, header http.Header, body string) answer {
	t.Helper()
	resp := r.open(t, method, path, auth, header, body)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return answer{StatusCode: resp.StatusCode, Header: resp.Header}
}

// open sends a request and leaves the body to the caller.
func (r *proxyRig) open(t *testing.T, method, path, auth string, header http.Header, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, r.p.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func (r *proxyRig) bearer() string { return "Bearer " + r.p.Secret() }

func TestProxySecretIsLongRandomAndPrivate(t *testing.T) {
	a, b := newProxyRig(t, nil), newProxyRig(t, nil)
	if len(a.p.Secret()) != 64 {
		t.Errorf("secret length = %d, want 64 hex characters (32 bytes)", len(a.p.Secret()))
	}
	if a.p.Secret() == b.p.Secret() {
		t.Error("two proxies share a secret, want one per process")
	}
	if !strings.HasPrefix(a.p.URL(), "http://127.0.0.1:") {
		t.Errorf("URL() = %q, want a loopback address", a.p.URL())
	}
	if s := fmt.Sprint(a.p); strings.Contains(s, a.p.Secret()) {
		t.Errorf("the proxy prints its secret: %s", s)
	}
}

func TestProxyRefusesAWrongOrMissingSecret(t *testing.T) {
	r := newProxyRig(t, nil)
	for name, auth := range map[string]string{
		"missing":      "",
		"wrong":        "Bearer " + strings.Repeat("0", 64),
		"prefix":       "Bearer " + r.p.Secret()[:32],
		"longer":       r.bearer() + "0",
		"not bearer":   "Basic " + r.p.Secret(),
		"real token":   "Bearer " + gatewayToken,
		"bare secret":  r.p.Secret(),
		"lower bearer": "bearer " + r.p.Secret() + "x",
	} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/v1/messages", "/mcp"} {
				if resp := r.do(t, http.MethodPost, path, auth, nil, "{}"); resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("POST %s status = %d, want 401", path, resp.StatusCode)
				}
			}
		})
	}
	if n := len(r.gateway.requests()) + len(r.mcp.requests()); n != 0 {
		t.Errorf("upstream requests = %d, want 0", n)
	}
}

func TestProxyAnswersOtherPathsLocally(t *testing.T) {
	r := newProxyRig(t, nil)
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodHead, "/api/hello", http.StatusNotFound},
		{http.MethodGet, "/v1/models?limit=1000", http.StatusNotFound},
		{http.MethodGet, "/v1/messages", http.StatusMethodNotAllowed},
		{http.MethodPost, "/v1/complete", http.StatusNotFound},
		{http.MethodPost, "/anthropic/v1/messages", http.StatusNotFound},
		{http.MethodPost, "/mcp/../v1/messages/x", http.StatusNotFound},
		{http.MethodPost, "/mcp/extra", http.StatusNotFound},
		{http.MethodPost, "/", http.StatusNotFound},
	} {
		if resp := r.do(t, c.method, c.path, r.bearer(), nil, ""); resp.StatusCode != c.want {
			t.Errorf("%s %s status = %d, want %d", c.method, c.path, resp.StatusCode, c.want)
		}
	}
	if n := len(r.gateway.requests()) + len(r.mcp.requests()); n != 0 {
		t.Errorf("upstream requests = %d, want 0", n)
	}
}

func TestProxySendsModelCallsToTheGatewayWithTheRealToken(t *testing.T) {
	r := newProxyRig(t, nil)
	header := http.Header{
		"Content-Type":      {"application/json"},
		"Anthropic-Version": {"2023-06-01"},
		"Anthropic-Beta":    {"interleaved-thinking-2025-05-14"},
		"X-Api-Key":         {"sk-ant-leaked"},
		"Cookie":            {"session=1"},
		"X-Stainless-Os":    {"Linux"},
	}
	resp := r.do(t, http.MethodPost, "/v1/messages?beta=true", r.bearer(), header, `{"model":"x"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Agentsone-Model"); got != "test-model" {
		t.Errorf("X-Agentsone-Model = %q, want the gateway's answer header", got)
	}
	r.do(t, http.MethodPost, "/v1/messages/count_tokens?beta=true", r.bearer(), header, `{}`)
	reqs := r.gateway.requests()
	if len(reqs) != 2 {
		t.Fatalf("gateway requests = %d, want 2", len(reqs))
	}
	for i, wantPath := range []string{"/anthropic/v1/messages", "/anthropic/v1/messages/count_tokens"} {
		got := reqs[i]
		if got.Path != wantPath || got.Query != "beta=true" {
			t.Errorf("request %d = %s?%s, want %s?beta=true", i, got.Path, got.Query, wantPath)
		}
		if auth := got.Header.Get("Authorization"); auth != "Bearer "+gatewayToken {
			t.Errorf("request %d Authorization = %q, want the gateway token", i, auth)
		}
		for _, drop := range []string{"X-Api-Key", "Cookie", "X-Stainless-Os"} {
			if v := got.Header.Get(drop); v != "" {
				t.Errorf("request %d forwarded %s = %q, want it dropped", i, drop, v)
			}
		}
		for _, keep := range []string{"Content-Type", "Anthropic-Version", "Anthropic-Beta"} {
			if got.Header.Get(keep) != header.Get(keep) {
				t.Errorf("request %d %s = %q, want %q", i, keep, got.Header.Get(keep), header.Get(keep))
			}
		}
		if strings.Contains(fmt.Sprint(got.Header), r.p.Secret()) {
			t.Errorf("request %d carries the local secret upstream", i)
		}
	}
	if reqs[0].Body != `{"model":"x"}` {
		t.Errorf("body = %q, want it passed unchanged", reqs[0].Body)
	}
}

func TestProxySendsMCPToTheMCPEndpointWithTheMCPToken(t *testing.T) {
	r := newProxyRig(t, nil)
	header := http.Header{"Mcp-Session-Id": {"sess-1"}, "Mcp-Protocol-Version": {"2025-06-18"}, "Accept": {"text/event-stream"}}
	for _, m := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		if resp := r.do(t, m, "/mcp", r.bearer(), header, ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s /mcp status = %d, want 200", m, resp.StatusCode)
		}
	}
	reqs := r.mcp.requests()
	if len(reqs) != 3 {
		t.Fatalf("MCP requests = %d, want 3", len(reqs))
	}
	for _, got := range reqs {
		if got.Path != "/mcp" || got.Header.Get("Authorization") != "Bearer "+mcpToken {
			t.Errorf("%s %s Authorization %q, want /mcp with the MCP token", got.Method, got.Path, got.Header.Get("Authorization"))
		}
		for k := range header {
			if got.Header.Get(k) != header.Get(k) {
				t.Errorf("%s %s = %q, want %q", got.Method, k, got.Header.Get(k), header.Get(k))
			}
		}
	}
	if n := len(r.gateway.requests()); n != 0 {
		t.Errorf("gateway requests = %d, want 0", n)
	}
}

func TestProxyUsesTheLatestTokens(t *testing.T) {
	r := newProxyRig(t, nil)
	r.do(t, http.MethodPost, "/v1/messages", r.bearer(), nil, "{}")
	r.tokens.swap("gateway-token-rotated-000000", "mcp-token-rotated-0000000000")
	r.do(t, http.MethodPost, "/v1/messages", r.bearer(), nil, "{}")
	r.do(t, http.MethodPost, "/mcp", r.bearer(), nil, "{}")
	gw := r.gateway.requests()
	if gw[0].Header.Get("Authorization") != "Bearer "+gatewayToken || gw[1].Header.Get("Authorization") != "Bearer gateway-token-rotated-000000" {
		t.Errorf("gateway tokens = %q, %q, want the first then the rotated one", gw[0].Header.Get("Authorization"), gw[1].Header.Get("Authorization"))
	}
	if got := r.mcp.requests()[0].Header.Get("Authorization"); got != "Bearer mcp-token-rotated-0000000000" {
		t.Errorf("MCP token = %q, want the rotated one", got)
	}
}

func TestProxyStreamsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	r := newProxyRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"first\":true}\n\n")
		w.(http.Flusher).Flush()
		<-release // the stream stays open until the test saw the first event
		_, _ = io.WriteString(w, "event: message_stop\ndata: {}\n\n")
	})
	resp := r.open(t, http.MethodPost, "/v1/messages", r.bearer(), nil, "{}")
	defer func() { _ = resp.Body.Close() }()
	defer close(release)
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- s
	}()
	select {
	case got := <-line:
		if got != "event: content_block_delta\n" {
			t.Errorf("first line = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first event did not arrive before the stream ended: the proxy buffers")
	}
}

func TestProxyReportsAnUnreachableUpstreamWithoutSecrets(t *testing.T) {
	r := newProxyRig(t, nil)
	r.gateway.Close()
	if resp := r.do(t, http.MethodPost, "/v1/messages", r.bearer(), nil, "{}"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	r.logMu.Lock()
	logs := r.logs.String()
	r.logMu.Unlock()
	if logs == "" {
		t.Error("no log line for the failed upstream call")
	}
	for _, secret := range []string{r.p.Secret(), gatewayToken, mcpToken} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain a secret:\n%s", logs)
		}
	}
}

func TestProxyNeverUsesTheEnvironmentProxy(t *testing.T) {
	r := newProxyRig(t, nil)
	if r.p.transport.Proxy != nil {
		t.Error("the outbound transport has a proxy function, want direct connections to the gateway")
	}
}

func TestProxyRefusesBadUpstreams(t *testing.T) {
	for name, cfg := range map[string]proxyConfig{
		"no tokens":       {ModelBaseURL: "http://gw/v1"},
		"no model url":    {Tokens: &tokens{}},
		"relative url":    {ModelBaseURL: "gw/v1", Tokens: &tokens{}},
		"bad scheme":      {ModelBaseURL: "file:///v1", Tokens: &tokens{}},
		"bad mcp url":     {ModelBaseURL: "http://gw/v1", MCPURL: "ftp://x/mcp", Tokens: &tokens{}},
		"mcp url no host": {ModelBaseURL: "http://gw/v1", MCPURL: "http:///mcp", Tokens: &tokens{}},
	} {
		t.Run(name, func(t *testing.T) {
			if p, err := startProxy(cfg); err == nil {
				_ = p.Close()
				t.Error("startProxy() error = nil, want a refusal")
			}
		})
	}
}

func TestProxyWithoutMCPAnswersMCPLocally(t *testing.T) {
	gw := newUpstream(t, nil)
	p, err := startProxy(proxyConfig{ModelBaseURL: gw.URL + "/v1", Tokens: &tokens{gateway: gatewayToken}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, p.URL()+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+p.Secret())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 without an MCP endpoint", resp.StatusCode)
	}
}

func TestGatewayRootDropsTheTrailingV1(t *testing.T) {
	for in, want := range map[string]string{
		"http://gw:8080/v1":      "http://gw:8080/anthropic",
		"http://gw:8080/v1/":     "http://gw:8080/anthropic",
		"http://gw:8080":         "http://gw:8080/anthropic",
		"http://gw:8080/api/v1":  "http://gw:8080/api/anthropic",
		"https://gw.example/v10": "https://gw.example/v10/anthropic",
	} {
		u, err := anthropicRoot(in)
		if err != nil || u.String() != want {
			t.Errorf("anthropicRoot(%q) = %v, %v, want %s", in, u, err, want)
		}
	}
}
