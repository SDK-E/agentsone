// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	httpadapter "github.com/SDK-E/agentsone/internal/api/adapters/http"
	"github.com/SDK-E/agentsone/internal/api/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

var ada = core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}

// verify knows one session cookie and one token.
func verify(_ context.Context, v string) (core.Caller, error) {
	switch v {
	case "aos_good":
		return ada, nil
	case "aot_good":
		return core.Caller{Kind: core.CallerAgent, Name: "founder", UID: -1, Scopes: []string{"mcp:write"}}, nil
	}
	return core.Anonymous, fmt.Errorf("bad: %w", core.ErrUnauthorized)
}

// echo answers 200 with the caller and the via marker it saw.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	c, _ := core.CallerFrom(r.Context())
	w.Header().Set("X-Caller", string(c.Kind)+":"+c.Name)
	w.Header().Set("X-Via", r.Header.Get(httpadapter.ViaHeader))
	w.Header().Set("X-Remote", r.RemoteAddr)
	w.Header().Set("X-Host", r.Host)
	w.WriteHeader(http.StatusOK)
})

// machineIP is one of the machine's own addresses in these tests.
const machineIP = "172.18.0.3"

var tcp = httpadapter.TCPIdentity{
	Verify: verify, EdgeIPs: []netip.Addr{netip.MustParseAddr("172.18.0.2")},
	IsOwn: func(a netip.Addr) bool { return a == netip.MustParseAddr(machineIP) },
}.Wrap(echo)

type req struct {
	method, path, remote, cookie, bearer, origin string
	// host is the Host header the daemon receives; empty keeps example.com.
	host    string
	headers map[string]string
}

func send(q req) *httptest.ResponseRecorder { return sendTo(tcp, q) }

func sendTo(h http.Handler, q req) *httptest.ResponseRecorder {
	r := httptest.NewRequest(q.method, q.path, nil)
	if q.host != "" {
		r.Host = q.host
	}
	r.RemoteAddr = q.remote
	if r.RemoteAddr == "" {
		r.RemoteAddr = "127.0.0.1:5000"
	}
	if q.cookie != "" {
		r.AddCookie(&http.Cookie{Name: httpadapter.CookieName, Value: q.cookie}) // #nosec G124 -- request cookie in a test; attributes are not sent
	}
	if q.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+q.bearer)
	}
	if q.origin != "" {
		r.Header.Set("Origin", q.origin)
	}
	for k, v := range q.headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestTCPIdentity_NamesTheCaller(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		req    req
		status int
		caller string
	}{
		{"session cookie", req{method: "GET", path: "/v1/agents", cookie: "aos_good"}, 200, "human:ada"},
		{"bearer token", req{method: "GET", path: "/v1/agents", bearer: "aot_good"}, 200, "agent:founder"},
		{"no credentials on a guarded operation", req{method: "GET", path: "/v1/agents"}, 401, ""},
		{"bad cookie on a guarded operation", req{method: "GET", path: "/v1/agents", cookie: "aos_bad"}, 401, ""},
		{"public operation without credentials", req{method: "GET", path: "/v1/setup"}, 200, "human:"},
		{"public operation ignores a bad cookie", req{method: "GET", path: "/v1/setup", cookie: "aos_bad"}, 200, "human:"},
		{"unknown v1 path needs a caller", req{method: "GET", path: "/v1/nothing"}, 401, ""},
		{"interface paths are open", req{method: "GET", path: "/agents/ada/chat"}, 200, "human:"},
		{"interface paths ignore a bad cookie", req{method: "GET", path: "/agents/ada/chat", cookie: "aos_bad"}, 200, "human:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := send(c.req)
			if rec.Code != c.status || rec.Header().Get("X-Caller") != c.caller {
				t.Fatalf("got %d %q, want %d %q: %s", rec.Code, rec.Header().Get("X-Caller"), c.status, c.caller, rec.Body)
			}
		})
	}
}

func TestTCPIdentity_ClearsABadSessionCookie(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "GET", path: "/v1/agents", cookie: "aos_bad"})
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("cookies %v", c)
	}
}

// refusedToken checks the answer to a bad bearer token: 401 before the module
// runs, the unauthenticated problem, no word of the token, and the session
// cookie beside it left alone.
func refusedToken(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("X-Caller") != "" {
		t.Fatalf("got %d %q, want 401 before the module runs: %s", rec.Code, rec.Header().Get("X-Caller"), body)
	}
	if !strings.Contains(body, `"type":"urn:agentsone:problem:unauthenticated"`) {
		t.Errorf("body %s, want the unauthenticated problem", body)
	}
	if strings.Contains(body, "forged") {
		t.Errorf("body %s repeats the token", body)
	}
	if c := rec.Result().Cookies(); len(c) != 0 {
		t.Errorf("cookies %v: a bad token must not end the session", c)
	}
}

// A bad bearer token is refused on every path, public ones and the interface
// included, and also beside a good session cookie: whoever sends a token means
// to be its caller, so the request never goes on as no one or as the cookie.
func TestTCPIdentity_RefusesABadTokenOnEveryPath(t *testing.T) {
	t.Parallel()
	auth := func(v string) map[string]string { return map[string]string{"Authorization": v} }
	cases := []struct {
		name string
		req  req
	}{
		{"on a guarded operation", req{method: "GET", path: "/v1/agents", bearer: "aot_forged"}},
		{"on a public operation", req{method: "GET", path: "/v1/setup", bearer: "aot_forged"}},
		{"on sign-in", req{method: "POST", path: "/v1/session", bearer: "aot_forged"}},
		{"on a signed webhook", req{method: "POST", path: "/v1/webhooks/emt_1", bearer: "aot_forged"}},
		{"on an interface path", req{method: "GET", path: "/agents/ada/chat", bearer: "aot_forged"}},
		{"on the interface root", req{method: "GET", path: "/", bearer: "aot_forged"}},
		{"on an unknown v1 path", req{method: "GET", path: "/v1/nothing", bearer: "aot_forged"}},
		{"through the edge", req{method: "GET", path: "/v1/setup", remote: "172.18.0.2:4000", bearer: "aot_forged", headers: forged}},
		{"beside a good cookie on a guarded operation", req{method: "GET", path: "/v1/agents", bearer: "aot_forged", cookie: "aos_good"}},
		{"beside a good cookie on a public operation", req{method: "GET", path: "/v1/setup", bearer: "aot_forged", cookie: "aos_good"}},
		{"beside a good cookie on an interface path", req{method: "GET", path: "/agents/ada/chat", bearer: "aot_forged", cookie: "aos_good"}},
		{"an empty token", req{method: "GET", path: "/v1/setup", headers: auth("Bearer ")}},
		{"the scheme alone", req{method: "GET", path: "/v1/setup", cookie: "aos_good", headers: auth("Bearer")}},
		{"the scheme in lower case", req{method: "GET", path: "/v1/setup", cookie: "aos_good", headers: auth("bearer aot_forged")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			refusedToken(t, send(c.req))
		})
	}
}

func TestTCPIdentity_RefusesCrossOriginUnsafeRequests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		req    req
		status int
	}{
		{"same origin cookie post", req{method: "POST", path: "/v1/agents", cookie: "aos_good", origin: "http://example.com"}, 200},
		{"cross origin cookie post", req{method: "POST", path: "/v1/agents", cookie: "aos_good", origin: "http://evil.test"}, 403},
		{"cross origin login (login CSRF)", req{method: "POST", path: "/v1/session", origin: "http://evil.test"}, 403},
		{"no origin and no fetch metadata", req{method: "DELETE", path: "/v1/session", cookie: "aos_good"}, 403},
		{"bearer clients are not browsers", req{method: "POST", path: "/v1/agents", bearer: "aot_good"}, 200},
		{"signed webhooks come from servers", req{method: "POST", path: "/v1/webhooks/emt_1"}, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if rec := send(c.req); rec.Code != c.status {
				t.Fatalf("got %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}

var forged = map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.9", "X-Forwarded-Proto": "https"}

func TestTCPIdentity_HonoursForwardedHeadersFromTheEdgeIP(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "GET", path: "/v1/setup", remote: "172.18.0.2:4000", headers: forged})
	if rec.Code != 200 || rec.Header().Get("X-Via") != "edge" || rec.Header().Get("X-Remote") != "203.0.113.9:0" {
		t.Fatalf("edge request: %d via %q remote %q", rec.Code, rec.Header().Get("X-Via"), rec.Header().Get("X-Remote"))
	}
}

func TestTCPIdentity_IgnoresForgedForwardedHeadersFromLoopback(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "GET", path: "/v1/setup", remote: "127.0.0.1:4000", headers: forged})
	if rec.Code != 200 || rec.Header().Get("X-Via") != "" || rec.Header().Get("X-Remote") != "127.0.0.1:4000" {
		t.Fatalf("loopback: %d via %q remote %q", rec.Code, rec.Header().Get("X-Via"), rec.Header().Get("X-Remote"))
	}
}

func TestTCPIdentity_IgnoresForgedForwardedHeadersFromTheMachinesOwnIP(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "GET", path: "/v1/setup", remote: machineIP + ":4000", headers: forged})
	if rec.Code != 200 || rec.Header().Get("X-Via") != "" || rec.Header().Get("X-Remote") != machineIP+":4000" {
		t.Fatalf("own address: %d via %q remote %q", rec.Code, rec.Header().Get("X-Via"), rec.Header().Get("X-Remote"))
	}
}

func TestTCPIdentity_RefusesPeersThatAreNeitherLocalNorTheEdge(t *testing.T) {
	t.Parallel()
	if rec := send(req{method: "GET", path: "/v1/setup", remote: "172.18.0.9:4000", headers: forged}); rec.Code != http.StatusForbidden {
		t.Fatalf("untrusted peer: %d", rec.Code)
	}
}

func TestTCPIdentity_IdentityHeadersFromTheEdgeGiveNoSession(t *testing.T) {
	t.Parallel()
	h := map[string]string{
		"X-Forwarded-For": "203.0.113.9", "X-Forwarded-User": "ada", "X-Remote-User": "ada", "Remote-User": "ada", "X-Auth-Request-User": "ada",
	}
	rec := send(req{method: "GET", path: "/v1/agents", remote: "172.18.0.2:4000", headers: h})
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"type":"urn:agentsone:problem:unauthenticated"`) {
		t.Fatalf("got %d %q %s, want the 401 unauthenticated problem", rec.Code, rec.Header().Get("X-Caller"), rec.Body)
	}
}

func TestSocketIdentity_IgnoresForwardedAndIdentityHeaders(t *testing.T) {
	t.Parallel()
	h := httpadapter.SocketIdentity(verify, noReport, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-User", "X-Auth-User"} {
			if r.Header.Get(name) != "" {
				t.Errorf("%s reached the module", name)
			}
		}
	}))
	r := httptest.NewRequest("GET", "/v1/agents", nil)
	for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-User", "X-Auth-User"} {
		r.Header.Set(name, "x")
	}
	r = r.WithContext(httpadapter.WithPeerCaller(r.Context(), core.Caller{Kind: core.CallerAgent, Name: "founder"}))
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func TestSocketIdentity_WithoutPeerCredentialsIsUnauthenticated(t *testing.T) {
	t.Parallel()
	h := httpadapter.SocketIdentity(nil, noReport, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request without peer credentials reached the module")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/agents", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"type":"urn:agentsone:problem:unauthenticated"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestTCPIdentity_DropsAClientSuppliedViaHeader(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "GET", path: "/v1/setup", headers: map[string]string{httpadapter.ViaHeader: "edge"}})
	if rec.Header().Get("X-Via") != "" {
		t.Fatalf("via %q", rec.Header().Get("X-Via"))
	}
}

func TestTCPIdentity_SameOriginThroughTheEdgeUsesTheHostTheEdgeSends(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "POST", path: "/v1/agents", remote: "172.18.0.2:4000", cookie: "aos_good", origin: "https://example.com", headers: forged})
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
}

// The web door sends the daemon's own address as Host and the address the browser used as
// X-Forwarded-Host.
var door = map[string]string{"X-Forwarded-For": "203.0.113.9", "X-Forwarded-Proto": "http", "X-Forwarded-Host": "127.0.0.1:7780"}

func TestTCPIdentity_SameOriginThroughTheDoorUsesTheForwardedHost(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "POST", path: "/v1/agents", remote: "172.18.0.2:4000", host: "machine:7700", cookie: "aos_good", origin: "http://127.0.0.1:7780", headers: door})
	if rec.Code != 200 || rec.Header().Get("X-Host") != "127.0.0.1:7780" {
		t.Fatalf("got %d host %q: %s; want 200 with the forwarded host", rec.Code, rec.Header().Get("X-Host"), rec.Body)
	}
}

func TestTCPIdentity_RefusesAForeignOriginThroughTheDoor(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "POST", path: "/v1/agents", remote: "172.18.0.2:4000", host: "machine:7700", cookie: "aos_good", origin: "http://evil.test", headers: door})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
}

func TestTCPIdentity_IgnoresAForwardedHostFromInsideTheMachine(t *testing.T) {
	t.Parallel()
	h := map[string]string{"X-Forwarded-Host": "evil.test"}
	for _, remote := range []string{"127.0.0.1:4000", machineIP + ":4000"} {
		rec := send(req{method: "POST", path: "/v1/agents", remote: remote, host: "127.0.0.1:7700", cookie: "aos_good", origin: "http://evil.test", headers: h})
		if rec.Code != http.StatusForbidden {
			t.Errorf("from %s: got %d, want the forged host ignored and the origin refused: %s", remote, rec.Code, rec.Body)
		}
	}
}

func TestTCPIdentity_KeepsTheHostWhenTheForwardedHostIsMalformed(t *testing.T) {
	t.Parallel()
	h := map[string]string{"X-Forwarded-Host": "evil.test/path"}
	rec := send(req{method: "GET", path: "/v1/setup", remote: "172.18.0.2:4000", host: "machine:7700", headers: h})
	if rec.Header().Get("X-Host") != "machine:7700" {
		t.Fatalf("host %q, want machine:7700", rec.Header().Get("X-Host"))
	}
}

// behindTLS is the web door behind a platform that ends TLS: the browser opened
// https://agents.example.test.
var behindTLS = map[string]string{"X-Forwarded-For": "203.0.113.9", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "agents.example.test"}

func TestTCPIdentity_ThroughTheDoorTheOriginSchemeIsTheForwardedProto(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, origin string
		status       int
	}{
		{"https page behind TLS", "https://agents.example.test", 200},
		{"same host over plain http", "http://agents.example.test", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := send(req{method: "POST", path: "/v1/agents", remote: "172.18.0.2:4000", host: "machine:7700", cookie: "aos_good", origin: c.origin, headers: behindTLS})
			if rec.Code != c.status {
				t.Fatalf("got %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}

func TestTCPIdentity_IgnoresAForwardedProtoFromInsideTheMachine(t *testing.T) {
	t.Parallel()
	h := map[string]string{"X-Forwarded-Proto": "https"}
	rec := send(req{method: "POST", path: "/v1/agents", host: "127.0.0.1:7700", cookie: "aos_good", origin: "http://127.0.0.1:7700", headers: h})
	if rec.Code != 200 {
		t.Fatalf("got %d, want the forged scheme ignored: %s", rec.Code, rec.Body)
	}
}

func TestTCPIdentity_TerminalWebSocketsFollowTheSameOriginRule(t *testing.T) {
	t.Parallel()
	upgrade := func(extra map[string]string) map[string]string {
		h := map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}
		for k, v := range extra {
			h[k] = v
		}
		return h
	}
	cases := []struct {
		name, origin string
		headers      map[string]string
		status       int
	}{
		{"same origin through the door", "http://127.0.0.1:7780", upgrade(door), 200},
		{"https page behind TLS", "https://agents.example.test", upgrade(behindTLS), 200},
		{"plain http page behind TLS", "http://agents.example.test", upgrade(behindTLS), 403},
		{"another site", "http://evil.test", upgrade(door), 403},
		{"a program, no Origin", "", upgrade(door), 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := send(req{method: "GET", path: "/v1/machine/pty/pty_1/socket", remote: "172.18.0.2:4000", host: "machine:7700", cookie: "aos_good", origin: c.origin, headers: c.headers})
			if rec.Code != c.status {
				t.Fatalf("got %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}

// public is the daemon with [api] public_url set.
var public = httpadapter.TCPIdentity{
	Verify: verify, EdgeIPs: []netip.Addr{netip.MustParseAddr("172.18.0.2")},
	IsOwn:  func(a netip.Addr) bool { return a == netip.MustParseAddr(machineIP) },
	Public: &url.URL{Scheme: "https", Host: "agents.example.test"},
}.Wrap(echo)

func TestTCPIdentity_WithAPublicURLOnlyThatOriginPasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, origin string
		status       int
	}{
		{"the public URL", "https://agents.example.test", 200},
		{"the door address", "http://127.0.0.1:7780", 403},
		{"the public host over http", "http://agents.example.test", 403},
		{"a foreign origin", "https://evil.test", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := sendTo(public, req{method: "POST", path: "/v1/agents", remote: "172.18.0.2:4000", host: "machine:7700", cookie: "aos_good", origin: c.origin, headers: door})
			if rec.Code != c.status {
				t.Fatalf("got %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}

func TestTCPIdentity_WithAPublicURLTheRequestHostIsThePublicHost(t *testing.T) {
	t.Parallel()
	rec := sendTo(public, req{method: "GET", path: "/v1/setup", remote: "172.18.0.2:4000", host: "machine:7700", headers: door})
	if rec.Header().Get("X-Host") != "agents.example.test" {
		t.Fatalf("host %q, want agents.example.test (terminal WebSockets compare the Origin to it)", rec.Header().Get("X-Host"))
	}
}

func TestSocketIdentity_UsesThePeerCaller(t *testing.T) {
	t.Parallel()
	h := httpadapter.SocketIdentity(verify, noReport, echo)
	r := httptest.NewRequest("GET", "/v1/agents", nil)
	r.Header.Set(httpadapter.ViaHeader, "edge")
	r = r.WithContext(httpadapter.WithPeerCaller(r.Context(), core.Caller{Kind: core.CallerAgent, Name: "founder"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Header().Get("X-Caller") != "agent:founder" || rec.Header().Get("X-Via") != "" {
		t.Fatalf("got %d %q via %q", rec.Code, rec.Header().Get("X-Caller"), rec.Header().Get("X-Via"))
	}
}

func TestSocketIdentity_RefusesWithoutPeerCredentials(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpadapter.SocketIdentity(verify, noReport, echo).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/agents", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

// noReport ignores refused peers, for tests about something else.
func noReport(*http.Request, error) {}

// refusedRoot is a uid-0 peer outside every agent cgroup that is not the daemon.
var refusedRoot = &domain.Refusal{Peer: domain.Peer{PID: 99, UID: 0}, Reason: domain.ReasonRootOutsideAgents}

// reports records what SocketIdentity reports about refused peers.
type reports struct {
	mu   sync.Mutex
	errs []error
}

func (rp *reports) report(_ *http.Request, err error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.errs = append(rp.errs, err)
}

func (rp *reports) all() []error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return append([]error(nil), rp.errs...)
}

// socketAsRefused sends a request on the socket chain from a refused peer.
func socketAsRefused(method, path, bearer string) (*httptest.ResponseRecorder, []error) {
	r := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	r = r.WithContext(httpadapter.WithPeer(r.Context(), core.Anonymous, refusedRoot))
	rec := httptest.NewRecorder()
	var rp reports
	httpadapter.SocketIdentity(verify, rp.report, echo).ServeHTTP(rec, r)
	return rec, rp.all()
}

func TestSocketIdentity_RootAgentOutsideItsCgroupIsNamedByItsToken(t *testing.T) {
	t.Parallel()
	rec, reported := socketAsRefused("GET", "/v1/agents", "aot_good")
	if rec.Code != 200 || rec.Header().Get("X-Caller") != "agent:founder" || len(reported) != 0 {
		t.Fatalf("got %d %q, reported %v", rec.Code, rec.Header().Get("X-Caller"), reported)
	}
}

func TestSocketIdentity_RefusedPeerWithoutTokenIsForbiddenAndReported(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/v1/agents", "/v1/setup"} {
		rec, reported := socketAsRefused("GET", path, "")
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"type":"urn:agentsone:problem:forbidden"`) {
			t.Fatalf("%s: got %d %s", path, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "uid") || strings.Contains(rec.Body.String(), domain.ReasonRootOutsideAgents) {
			t.Errorf("%s: the answer tells the caller why: %s", path, rec.Body)
		}
		if len(reported) != 1 || !errors.Is(reported[0], refusedRoot) {
			t.Errorf("%s: reported %v, want the refusal once", path, reported)
		}
	}
}

func TestSocketIdentity_RefusedPeerStillGetsTheDoorOnlyAnswerForTheFirstHuman(t *testing.T) {
	t.Parallel()
	rec, _ := socketAsRefused("POST", "/v1/setup/first-human", "")
	refusedFirstHuman(t, rec)
}

func TestSocketIdentity_RefusesABadToken(t *testing.T) {
	t.Parallel()
	if rec, _ := socketAsRefused("GET", "/v1/agents", "aot_forged"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

// A bad bearer token on the socket is refused whoever the peer is: the
// request never goes on as the agent, the human or the daemon the kernel
// names, on guarded and public operations alike.
func TestSocketIdentity_RefusesABadTokenWhoeverThePeerIs(t *testing.T) {
	t.Parallel()
	founder := core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 1001}
	daemon := core.Caller{Kind: core.CallerSystem, Name: "daemon", UID: 0, IsRoot: true}
	cases := []struct {
		name, path, authorization string
		peer                      core.Caller
		refused                   error
	}{
		{"an agent on a guarded operation", "/v1/agents", "Bearer aot_forged", founder, nil},
		{"an agent on a public operation", "/v1/setup", "Bearer aot_forged", founder, nil},
		{"an agent on an interface path", "/agents/founder/chat", "Bearer aot_forged", founder, nil},
		{"an agent with an empty token", "/v1/agents", "Bearer ", founder, nil},
		{"an agent with the scheme alone", "/v1/agents", "Bearer", founder, nil},
		{"an agent with the scheme in lower case", "/v1/agents", "bearer aot_forged", founder, nil},
		{"a human", "/v1/agents", "Bearer aot_forged", ada, nil},
		{"the daemon itself", "/v1/setup", "Bearer aot_forged", daemon, nil},
		{"a refused peer", "/v1/setup", "Bearer aot_forged", core.Anonymous, refusedRoot},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("GET", c.path, nil)
			r.Header.Set("Authorization", c.authorization)
			r = r.WithContext(httpadapter.WithPeer(r.Context(), c.peer, c.refused))
			rec := httptest.NewRecorder()
			httpadapter.SocketIdentity(verify, noReport, echo).ServeHTTP(rec, r)
			refusedToken(t, rec)
		})
	}
}

// refusedFirstHuman checks the door-only problem for createFirstHuman.
func refusedFirstHuman(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), `"type":"urn:agentsone:problem:forbidden"`) ||
		!strings.Contains(rec.Body.String(), "Open the web door to finish setup.") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestFirstHuman_RefusedOnTheSocket(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/setup/first-human", nil)
	r = r.WithContext(httpadapter.WithPeerCaller(r.Context(), core.Caller{Kind: core.CallerAgent, Name: "founder", IsRoot: true}))
	rec := httptest.NewRecorder()
	httpadapter.SocketIdentity(verify, noReport, echo).ServeHTTP(rec, r)
	refusedFirstHuman(t, rec)
}

func TestFirstHuman_RefusedOnLoopbackTCP(t *testing.T) {
	t.Parallel()
	refusedFirstHuman(t, send(req{method: "POST", path: "/v1/setup/first-human", origin: "http://example.com"}))
}

func TestFirstHuman_RefusedFromTheMachinesOwnAddress(t *testing.T) {
	t.Parallel()
	refusedFirstHuman(t, send(req{method: "POST", path: "/v1/setup/first-human", remote: machineIP + ":4000", origin: "http://example.com"}))
}

func TestFirstHuman_AcceptedFromTheEdgeIP(t *testing.T) {
	t.Parallel()
	rec := send(req{method: "POST", path: "/v1/setup/first-human", remote: "172.18.0.2:4000", origin: "https://example.com", headers: forged})
	if rec.Code != http.StatusOK || rec.Header().Get("X-Caller") != "human:" {
		t.Fatalf("got %d %q: %s", rec.Code, rec.Header().Get("X-Caller"), rec.Body)
	}
}

func TestFirstHuman_ForwardedHeadersCannotFakeTheEdge(t *testing.T) {
	t.Parallel()
	h := map[string]string{"X-Forwarded-For": "172.18.0.2", "X-Forwarded-Proto": "https", httpadapter.ViaHeader: "edge"}
	refusedFirstHuman(t, send(req{method: "POST", path: "/v1/setup/first-human", origin: "http://example.com", headers: h}))
}
