// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"

	contract "github.com/SDK-E/agentsone/internal/contract/api"
)

// CookieName is the human session cookie, as in the contract's cookieAuth scheme.
const CookieName = "agentsone_session"

// ViaHeader is set by the API layer, never by clients: the API layer
// deletes any incoming copy and sets "edge" on requests the trusted edge
// proxy forwarded over TLS.
const ViaHeader = "X-Agentsone-Via"

// requestInfo is what the strict handlers need from the raw request.
type requestInfo struct {
	cookie string
	remote string
	// secure is true when the browser reached us over TLS through the edge,
	// so the cookie must be Secure. Plain loopback is the only place it is not.
	secure bool
}

type infoKey struct{}

// captureRequest is a strict middleware: generated handlers see only the
// decoded body, so the cookie and peer address travel on the context.
func captureRequest[F ~func(context.Context, http.ResponseWriter, *http.Request, any) (any, error)](f F, _ string) F {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		info := requestInfo{secure: r.Header.Get(ViaHeader) == "edge" || r.TLS != nil}
		if c, err := r.Cookie(CookieName); err == nil {
			info.cookie = c.Value
		}
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			info.remote = host
		}
		return f(context.WithValue(ctx, infoKey{}, info), w, r, req)
	}
}

func infoFrom(ctx context.Context) requestInfo {
	if info, ok := ctx.Value(infoKey{}).(requestInfo); ok {
		return info
	}
	return requestInfo{} // not behind the request wrapper: no client details

}

// sessionCookie builds the Set-Cookie value. HttpOnly and SameSite=Strict
// always; Secure whenever the browser is on TLS.
func sessionCookie(value string, maxAge int, secure bool) string {
	// #nosec G124 -- Secure whenever the browser is on TLS; plain loopback cannot keep a Secure cookie
	c := &http.Cookie{
		Name: CookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure,
	}
	return c.String()
}

// contractVersion is the version of the contract this daemon serves.
var contractVersion = sync.OnceValue(func() string {
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(contract.Spec, &doc); err != nil {
		return ""
	}
	return doc.Info.Version
})
