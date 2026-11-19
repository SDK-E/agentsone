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

// ViaHeader tells modules a request came through the edge proxy. Only this
// layer sets it; any copy a client sends is deleted first.
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
	// TrustedProxies are the edge proxy addresses. X-Forwarded-* headers
	// are read only from them; other non-loopback peers are refused.
	TrustedProxies []netip.Prefix
}

// Wrap returns next behind the TCP identity rules.
func (t TCPIdentity) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(ViaHeader)
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
				clearBadCookie(w, fromCookie)
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

// place checks where the request came from. Loopback is local. A trusted
// edge proxy is believed about the client address and host, and the
// request is marked as coming through the edge.
func (t TCPIdentity) place(r *http.Request) (*http.Request, error) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r, fmt.Errorf("peer address %q: %w", r.RemoteAddr, core.ErrForbidden)
	}
	addr := ap.Addr().Unmap()
	if addr.IsLoopback() && r.Header.Get("X-Forwarded-For") == "" {
		return r, nil
	}
	if !t.trusted(addr) {
		return r, fmt.Errorf("requests must come through the edge: %w", core.ErrForbidden)
	}
	r = r.Clone(r.Context())
	r.Header.Set(ViaHeader, "edge")
	if client := lastForwarded(r.Header.Get("X-Forwarded-For")); client.IsValid() {
		r.RemoteAddr = netip.AddrPortFrom(client, 0).String()
	}
	if host := r.Header.Get("X-Forwarded-Host"); host != "" {
		r.Host = host
	}
	return r, nil
}

func (t TCPIdentity) trusted(a netip.Addr) bool {
	for _, p := range t.TrustedProxies {
		if p.Contains(a) {
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

func clearBadCookie(w http.ResponseWriter, fromCookie bool) {
	if fromCookie {
		http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
}
