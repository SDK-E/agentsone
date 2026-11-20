// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// Pattern is the proxy's route: everything under /ide/<human>/. The API
// layer already sends /ide/ to the modules.
const Pattern = "/ide/{human}/"

const csp = "Content-Security-Policy"

// frameAncestors lets the interface embed the editor in a same-origin
// frame, and nobody else.
const frameAncestors = "frame-ancestors 'self'"

// Proxy carries a signed-in human's browser to their own editor server on
// its Unix socket. The editor never sees the human's session cookie or
// token: it trusts the socket, and the daemon checks the human.
type Proxy struct {
	servers Servers
	rp      *httputil.ReverseProxy
}

type socketKey struct{}

// NewProxy returns the proxy over servers. Upstream errors are logged at
// debug level: a browser that leaves mid-answer is not a fault.
func NewProxy(servers Servers, log *slog.Logger) *Proxy {
	var d net.Dialer
	transport := &http.Transport{
		// Connections are pooled per human (the request host is the human),
		// and each dials that human's socket.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			sock, ok := ctx.Value(socketKey{}).(string)
			if !ok {
				return nil, fmt.Errorf("editor socket unknown: %w", core.ErrUnavailable)
			}
			return d.DialContext(ctx, "unix", sock)
		},
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: time.Minute,
	}
	p := &Proxy{servers: servers}
	p.rp = &httputil.ReverseProxy{
		Rewrite:        rewrite,
		Transport:      transport,
		ModifyResponse: allowSameOriginFrames,
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelDebug),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.LogAttrs(r.Context(), slog.LevelDebug, "editor proxy", slog.String("error", err.Error()))
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadGateway, "bad-gateway", "your editor did not answer; stop it and start it again"))
		},
	}
	return p
}

// Route is the proxy as a module route.
func (p *Proxy) Route() core.Route { return core.Route{Pattern: Pattern, Handler: p} }

// ServeHTTP checks the caller is the human the path names and that their
// editor runs, then forwards the request unchanged in path and query.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	human := r.PathValue("human")
	c, _ := core.CallerFrom(r.Context())
	sock, err := p.servers.Socket(c, human)
	if err != nil {
		core.WriteError(w, r, err)
		return
	}
	// The interface's page policy forbids framing and inline code; the
	// editor brings its own policy, and must open in the IDE panel's frame.
	h := w.Header()
	h.Del(csp)
	h.Set("X-Frame-Options", "SAMEORIGIN")
	p.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), socketKey{}, sock)))
}

// rewrite keeps the host the browser used (the editor builds its WebSocket
// address from it) and drops every credential of the interface.
func rewrite(pr *httputil.ProxyRequest) {
	pr.Out.URL.Scheme = "http"
	pr.Out.URL.Host = pr.In.PathValue("human")
	pr.Out.Host = pr.In.Host
	pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
	pr.Out.Header.Del("Authorization")
	pr.Out.Header.Del("X-Agentsone-Via")
	keepEditorCookies(pr.Out)
}

// keepEditorCookies passes on only the editor's own cookies (vscode-*).
func keepEditorCookies(r *http.Request) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if strings.HasPrefix(c.Name, "vscode-") {
			r.AddCookie(c)
		}
	}
}

// allowSameOriginFrames adds frame-ancestors 'self' to the editor's policy
// and drops any frame header of its own: the proxy set SAMEORIGIN.
func allowSameOriginFrames(res *http.Response) error {
	res.Header.Del("X-Frame-Options")
	policies := res.Header.Values(csp)
	if len(policies) == 0 {
		res.Header.Set(csp, frameAncestors)
		return nil
	}
	out := make([]string, 0, len(policies))
	for _, v := range policies {
		if !strings.Contains(v, "frame-ancestors") {
			v = strings.TrimRight(v, "; ") + "; " + frameAncestors
		}
		out = append(out, v)
	}
	res.Header[csp] = out
	return nil
}
