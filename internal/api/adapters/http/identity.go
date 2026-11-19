// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
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

// WithPeerCaller stores the caller resolved from socket peer credentials
// when the connection was accepted. Use it in http.Server.ConnContext.
func WithPeerCaller(ctx context.Context, c core.Caller) context.Context {
	return context.WithValue(ctx, peerKey{}, c)
}

// SocketIdentity sets the caller of every request on the Unix socket from
// the peer credentials stored by WithPeerCaller. No token is needed: the
// kernel vouches for the peer.
func SocketIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(ViaHeader)
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Proto")
		dropIdentityHeaders(r.Header)
		c, ok := r.Context().Value(peerKey{}).(core.Caller)
		if !ok {
			core.WriteProblem(w, r, core.NewProblem(http.StatusUnauthorized, "unauthorized", "peer credentials unavailable"))
			return
		}
		next.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
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
		r, err := t.place(r)
		if err != nil {
			core.WriteError(w, r, err)
			return
		}
		value, fromCookie := credential(r)
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !isBearer(r) {
			if !domain.SameOrigin(domain.OriginCheck{
				Method: r.Method, Origin: r.Header.Get("Origin"), FetchSite: r.Header.Get("Sec-Fetch-Site"), Host: r.Host,
			}) && !isPublicNonBrowser(r) {
				core.WriteProblem(w, r, core.NewProblem(http.StatusForbidden, "forbidden", "cross-origin request refused"))
				return
			}
		}
		caller := core.Anonymous
		if value != "" {
			c, err := t.Verify(r.Context(), value)
			switch {
			case err == nil:
				caller = c
			case !needsCaller(r):
			default:
				clearBadCookie(w, r, fromCookie)
				core.WriteError(w, r, fmt.Errorf("credentials: %w", core.ErrUnauthorized))
				return
			}
		}
		if caller.Name == "" && needsCaller(r) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusUnauthorized, "unauthorized", "sign in or send a token"))
			return
		}
		next.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), caller)))
	})
}

// place checks where the request came from. Loopback and the machine's own
// addresses are local: forwarded headers from them are dropped. The edge
// proxy, matched by exact address, is believed about two things only: the
// client address and the scheme. Anyone else is refused.
func (t TCPIdentity) place(r *http.Request) (*http.Request, error) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r, fmt.Errorf("peer address %q: %w", r.RemoteAddr, core.ErrForbidden)
	}
	addr := ap.Addr().Unmap()
	local := addr.IsLoopback() || (t.IsOwn != nil && t.IsOwn(addr))
	if local || !t.isEdge(addr) {
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Proto")
		if !local {
			return r, fmt.Errorf("requests must come through the edge: %w", core.ErrForbidden)
		}
		return r, nil
	}
	if client := lastForwarded(r.Header.Get("X-Forwarded-For")); client.IsValid() {
		r.RemoteAddr = netip.AddrPortFrom(client, 0).String()
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		r.Header.Set(ViaHeader, "edge")
	}
	return r, nil
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
	parts := strings.Split(h, ",")
	a, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1]))
	if err != nil {
		return netip.Addr{}
	}
	return a
}

// credential returns the bearer token, else the session cookie.
func credential(r *http.Request) (string, bool) {
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v), false
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value, true
	}
	return "", false
}

func isBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
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

func clearBadCookie(w http.ResponseWriter, r *http.Request, fromCookie bool) {
	if fromCookie {
		// #nosec G124 -- Secure whenever the browser is on TLS (through the edge); plain loopback cannot keep a Secure cookie
		http.SetCookie(w, &http.Cookie{
			Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode,
			Secure: r.Header.Get(ViaHeader) == "edge",
		})
	}
}
