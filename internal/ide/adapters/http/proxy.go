// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"io"
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
			writeError(w, r, core.NewProblem(http.StatusBadGateway, "bad-gateway", "your editor did not answer; stop it and start it again"))
		},
	}
	return p
}

// pageStyle is the only style of the proxy's own pages: the system font,
// the system's light or dark colours, the interface's 16 px gutter.
const pageStyle = "body{font:16px/1.5 system-ui,sans-serif;margin:16px}"

// errorPolicy is the policy of the proxy's own pages: nothing loads but
// pageStyle, and only the interface may frame them.
var errorPolicy = func() string {
	sum := sha256.Sum256([]byte(pageStyle))
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; " + frameAncestors
}()

// pageText says, for a browser page, why the editor did not open.
var pageText = map[int]string{
	http.StatusUnauthorized:       "Sign in to open your editor.",
	http.StatusForbidden:          "This editor belongs to someone else.",
	http.StatusServiceUnavailable: "Your editor is not running. Start it from the IDE panel.",
	http.StatusBadGateway:         "Your editor did not answer. Stop it and start it again from the IDE panel.",
}

// writeError answers why the editor did not open: a plain page the IDE
// panel's frame can show when a browser asks for one, a problem otherwise.
// The page follows the system's light or dark scheme and fits a phone.
func writeError(w http.ResponseWriter, r *http.Request, p *core.Problem) {
	w.Header().Set(csp, errorPolicy)
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		core.WriteProblem(w, r, p)
		return
	}
	text, ok := pageText[p.Status]
	if !ok {
		text = "Your editor could not open. Try again from the IDE panel."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_, _ = io.WriteString(w, `<!doctype html><html lang="en"><head><meta charset="utf-8">`+
		`<meta name="viewport" content="width=device-width, initial-scale=1">`+
		`<meta name="color-scheme" content="light dark"><title>Editor</title><style>`+pageStyle+`</style></head>`+
		`<body><p>`+html.EscapeString(text)+`</p></body></html>`) // the status is sent; a failed write means the client left
}

// Route is the proxy as a module route.
func (p *Proxy) Route() core.Route { return core.Route{Pattern: Pattern, Handler: p} }

// ServeHTTP checks the caller is the human the path names and that their
// editor runs, then forwards the request unchanged in path and query.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	human := r.PathValue("human")
	c, _ := core.CallerFrom(r.Context())
	// The interface's page policy forbids framing; everything under /ide/
	// opens in the IDE panel's frame, the error pages included.
	h := w.Header()
	h.Set("X-Frame-Options", "SAMEORIGIN")
	sock, err := p.servers.Socket(c, human)
	if err != nil {
		writeError(w, r, core.ProblemFrom(err))
		return
	}
	// The editor brings its own policy (frame-ancestors is added to it).
	h.Del(csp)
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
