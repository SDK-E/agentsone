// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/SDK-E/agentsone/internal/api/domain"
	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// CookieName is the human session cookie (cookieAuth in the contract).
const CookieName = "agentsone_session"

// ViaHeader is "edge" when the browser reached the edge proxy over TLS, so
// cookies must be Secure. Only this layer sets it; any copy a client sends
// is deleted first.
const ViaHeader = "X-Agentsone-Via"

// publicOperations need no caller: their contract security is empty or a
// signature the module checks itself. Fixed lookup table.
var publicOperations = map[string]bool{
	"createSession":    true,
	"getSetupStatus":   true,
	"createFirstHuman": true,
	"receiveWebhook":   true,
}

// Verifier turns a bearer token or session cookie value into a caller
// (core.TokenIssuer's Verify).
type Verifier func(ctx context.Context, value string) (core.Caller, error)

type peerKey struct{}

// peer is what the socket listener found about a connection: its caller,
// or why it was refused.
type peer struct {
	caller  core.Caller
	refused error
}

// WithPeerCaller stores the caller resolved from socket peer credentials
// when the connection was accepted.
func WithPeerCaller(ctx context.Context, c core.Caller) context.Context {
	return context.WithValue(ctx, peerKey{}, peer{caller: c})
}

// WithPeer stores what resolving the socket peer gave: the caller, or the
// refusal when err is set. Use it in http.Server.ConnContext.
func WithPeer(ctx context.Context, c core.Caller, err error) context.Context {
	if err != nil {
		return context.WithValue(ctx, peerKey{}, peer{refused: err})
	}
	return WithPeerCaller(ctx, c)
}

// SocketIdentity sets the caller of every request on the Unix socket from
// the peer stored by WithPeer: the kernel vouches for the peer. A bearer
// token, when sent, names the caller instead; root agents outside an agent
// cgroup prove who they are this way. A token that does not verify gets
// 401 whoever the peer is. A refused peer without a token gets 403, and
// report hears of it (for the audit trail) on every request.
func SocketIdentity(verify Verifier, report func(*http.Request, error), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isFirstHuman(r) {
			refuseFirstHuman(w, r)
			return
		}
		c, sent, err := verifyBearer(verify, r)
		if err != nil {
			core.WriteError(w, r, err)
			return
		}
		if sent {
			r = r.WithContext(WithPeerCaller(r.Context(), c))
		}
		r.Header.Del(ViaHeader)
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Proto")
		dropIdentityHeaders(r.Header)
		p, ok := r.Context().Value(peerKey{}).(peer)
		if !ok {
			core.WriteProblem(w, r, core.NewProblem(http.StatusUnauthorized, "unauthenticated", "peer credentials unavailable"))
			return
		}
		if p.refused != nil {
			// The reason goes to the audit trail, not to the caller.
			report(r, p.refused)
			core.WriteProblem(w, r, core.NewProblem(http.StatusForbidden, "forbidden",
				"this process is not an agent, a human or the daemon; send a token"))
			return
		}
		next.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), p.caller)))
	})
}

// TCPIdentity guards the TCP listener: browsers on loopback and, through
// the edge proxy, from the network.
type TCPIdentity struct {
	Verify Verifier
	// EdgeIPs are the edge proxy's exact addresses. X-Forwarded-For and
	// X-Forwarded-Proto are read only from them; other peers that are not
	// local are refused.
	EdgeIPs []netip.Addr
	// IsOwn reports the machine's own addresses. They count as local and
	// are never trusted as the edge, even if configured.
	IsOwn func(netip.Addr) bool
	// Public is the configured public origin ([api] public_url), or nil.
	Public *url.URL
}

// identityHeaders are dropped from every request: a human is known only by
// a daemon session, never by a header a proxy or client could set.
func dropIdentityHeaders(h http.Header) {
	for name := range h {
		switch {
		case name == "X-Forwarded-User", name == "X-Forwarded-Email", name == "X-Forwarded-Preferred-Username",
			name == "X-Remote-User", name == "Remote-User", strings.HasPrefix(name, "X-Auth-"):
			h.Del(name)
		}
	}
}

// Wrap returns next behind the TCP identity rules.
func (t TCPIdentity) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(ViaHeader)
		dropIdentityHeaders(r.Header)
		r, fromEdge, err := t.place(r)
		if err != nil {
			core.WriteError(w, r, err)
			return
		}
		// The origin the interface is served at is what the browser's Origin must
		// match, here and in the terminal WebSocket check: the configured public
		// URL when there is one, else the host place found and, from the edge,
		// the scheme the browser used.
		scheme := ""
		if fromEdge {
			scheme = forwardedScheme(r.Header.Get("X-Forwarded-Proto"))
		}
		if t.Public != nil {
			r.Host, scheme = t.Public.Host, t.Public.Scheme
		}
		if origin := r.Header.Get("Origin"); isWebSocket(r) && origin != "" && !domain.OriginMatches(origin, r.Host, scheme) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusForbidden, "forbidden", "cross-origin WebSocket refused"))
			return
		}
		if isFirstHuman(r) && !fromEdge {
			refuseFirstHuman(w, r)
			return
		}
		// A token names the caller and is never skipped: one that does not
		// verify gets 401 on every path, also beside a good session cookie.
		caller, sentToken, err := verifyBearer(t.Verify, r)
		if err != nil {
			core.WriteError(w, r, err)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !sentToken {
			if !domain.SameOrigin(domain.OriginCheck{
				Method: r.Method, Origin: r.Header.Get("Origin"), FetchSite: r.Header.Get("Sec-Fetch-Site"), Host: r.Host, Scheme: scheme,
			}) && !isPublicNonBrowser(r) {
				core.WriteProblem(w, r, core.NewProblem(http.StatusForbidden, "forbidden", "cross-origin request refused"))
				return
			}
		}
		// Without a token the session cookie names the caller. A bad cookie
		// is ignored where no caller is needed, so a stale cookie does not
		// lock a browser out of the sign-in page.
		if cookie, cerr := r.Cookie(CookieName); !sentToken && cerr == nil {
			c, verr := t.Verify(r.Context(), cookie.Value)
			switch {
			case verr == nil:
				caller = c
			case !needsCaller(r):
			default:
				clearBadCookie(w, r)
				core.WriteError(w, r, fmt.Errorf("credentials: %w", core.ErrUnauthorized))
				return
			}
		}
		if caller.Name == "" && needsCaller(r) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusUnauthorized, "unauthenticated", "sign in or send a token"))
			return
		}
		next.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), caller)))
	})
}

// place checks where the request came from. Loopback and the machine's own
// addresses are local: forwarded headers from them are dropped. The edge
// proxy, matched by exact address, is believed about three things only: the
// client address, the scheme and the host the browser used (the web door
// sends the daemon's own address as Host). Anyone else is refused.
// The second result is true when the peer is the edge itself.
func (t TCPIdentity) place(r *http.Request) (*http.Request, bool, error) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r, false, fmt.Errorf("peer address %q: %w", r.RemoteAddr, core.ErrForbidden)
	}
	addr := ap.Addr().Unmap()
	local := addr.IsLoopback() || (t.IsOwn != nil && t.IsOwn(addr))
	forwardedHost := r.Header.Get("X-Forwarded-Host")
	r.Header.Del("X-Forwarded-Host")
	if local || !t.isEdge(addr) {
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Proto")
		if !local {
			return r, false, fmt.Errorf("requests must come through the edge: %w", core.ErrForbidden)
		}
		return r, false, nil
	}
	if client := lastForwarded(r.Header.Get("X-Forwarded-For")); client.IsValid() {
		r.RemoteAddr = netip.AddrPortFrom(client, 0).String()
	}
	if host, ok := hostOnly(lastEntry(forwardedHost)); ok {
		r.Host = host
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		r.Header.Set(ViaHeader, "edge")
	}
	return r, true, nil
}

// isFirstHuman reports a createFirstHuman request. The first human claims
// the machine, so only a browser through the web door may send it: agents
// inside the machine reach the socket and loopback, never the door.
func isFirstHuman(r *http.Request) bool {
	route, ok := contract.Match(r.Method, r.URL.Path)
	return ok && route.OperationID == "createFirstHuman"
}

func refuseFirstHuman(w http.ResponseWriter, r *http.Request) {
	core.WriteProblem(w, r, core.NewProblem(http.StatusForbidden, "forbidden", "Open the web door to finish setup."))
}

func (t TCPIdentity) isEdge(a netip.Addr) bool {
	for _, e := range t.EdgeIPs {
		if e == a {
			return true
		}
	}
	return false
}

// lastForwarded is the address the trusted proxy itself saw: the last
// entry. Earlier entries are client-supplied and not trusted.
func lastForwarded(h string) netip.Addr {
	a, err := netip.ParseAddr(lastEntry(h))
	if err != nil {
		return netip.Addr{}
	}
	return a
}

// lastEntry is the last comma-separated entry of a forwarded header: the one
// the trusted proxy itself set.
func lastEntry(h string) string {
	parts := strings.Split(h, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}

// forwardedScheme is the scheme the edge says the browser used, or "" when
// it says nothing usable.
func forwardedScheme(h string) string {
	switch s := lastEntry(h); s {
	case "http", "https":
		return s
	}
	return ""
}

// isWebSocket reports a WebSocket upgrade. GET passes the CSRF check, so a
// browser's Origin on an upgrade is checked on its own.
func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// hostOnly accepts a bare host or host:port, nothing more.
func hostOnly(h string) (string, bool) {
	if h == "" {
		return "", false
	}
	u, err := url.Parse("http://" + h)
	if err != nil || u.Host != h || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return h, true
}

// bearer returns the token of an Authorization header in the Bearer scheme.
// sent is true whenever the header names that scheme, in any case (RFC 9110
// section 11.1), even with no token after it: an empty token is a bad one.
func bearer(r *http.Request) (token string, sent bool) {
	scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// verifyBearer returns the caller the request's bearer token names, and
// whether the request sent a token at all. A token that is empty or that
// verify refuses (bad, expired, revoked) is ErrUnauthorized, with no word of
// the token or of why.
func verifyBearer(verify Verifier, r *http.Request) (core.Caller, bool, error) {
	token, sent := bearer(r)
	if !sent {
		return core.Anonymous, false, nil
	}
	if token != "" {
		if c, err := verify(r.Context(), token); err == nil {
			return c, true, nil
		}
	}
	return core.Anonymous, true, fmt.Errorf("token: %w", core.ErrUnauthorized)
}

// isPublicNonBrowser lets signed webhooks in: they come from servers, not
// browsers, and carry no cookie.
func isPublicNonBrowser(r *http.Request) bool {
	route, ok := contract.Match(r.Method, r.URL.Path)
	return ok && route.OperationID == "receiveWebhook" && r.Header.Get("Cookie") == ""
}

// needsCaller is true for contract operations outside publicOperations.
// Other paths (the interface, unknown paths) are served to anyone.
func needsCaller(r *http.Request) bool {
	route, ok := contract.Match(r.Method, r.URL.Path)
	if !ok {
		return strings.HasPrefix(r.URL.Path, contract.BasePath+"/")
	}
	return !publicOperations[route.OperationID]
}

func clearBadCookie(w http.ResponseWriter, r *http.Request) {
	// #nosec G124 -- Secure whenever the browser is on TLS (through the edge); plain loopback cannot keep a Secure cookie
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.Header.Get(ViaHeader) == "edge",
	})
}
