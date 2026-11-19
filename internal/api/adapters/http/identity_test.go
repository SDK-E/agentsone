// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	httpadapter "github.com/SDK-E/agentsone/internal/api/adapters/http"
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
	headers                                      map[string]string
}

func send(q req) *httptest.ResponseRecorder {
	r := httptest.NewRequest(q.method, q.path, nil)
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
	tcp.ServeHTTP(rec, r)
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
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d %q, want 401", rec.Code, rec.Header().Get("X-Caller"))
	}
}

func TestSocketIdentity_IgnoresForwardedAndIdentityHeaders(t *testing.T) {
	t.Parallel()
	h := httpadapter.SocketIdentity(verify, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
	r = r.WithContext(httpadapter.WithPeerCaller(r.Context(), core.Caller{Kind: core.CallerSystem, Name: "uid:0"}))
	h.ServeHTTP(httptest.NewRecorder(), r)
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

func TestSocketIdentity_UsesThePeerCaller(t *testing.T) {
	t.Parallel()
	h := httpadapter.SocketIdentity(verify, echo)
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
	httpadapter.SocketIdentity(verify, echo).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/agents", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

// socketAs sends a request on the socket chain as a uid-0 system peer.
func socketAs(bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/v1/agents", nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	r = r.WithContext(httpadapter.WithPeerCaller(r.Context(), core.Caller{Kind: core.CallerSystem, Name: "uid:0", UID: 0, IsRoot: true}))
	rec := httptest.NewRecorder()
	httpadapter.SocketIdentity(verify, echo).ServeHTTP(rec, r)
	return rec
}

func TestSocketIdentity_RootAgentOutsideItsCgroupIsNamedByItsToken(t *testing.T) {
	t.Parallel()
	if rec := socketAs("aot_good"); rec.Code != 200 || rec.Header().Get("X-Caller") != "agent:founder" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("X-Caller"))
	}
}

func TestSocketIdentity_Uid0WithoutTokenStaysSystem(t *testing.T) {
	t.Parallel()
	if rec := socketAs(""); rec.Code != 200 || rec.Header().Get("X-Caller") != "system:uid:0" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("X-Caller"))
	}
}

func TestSocketIdentity_RefusesABadToken(t *testing.T) {
	t.Parallel()
	if rec := socketAs("aot_forged"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}
