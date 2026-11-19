// SPDX-License-Identifier: MIT

package claude

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/runners/sdk"
)

// The proxy is the token wall between the engine and the gateway. The
// engine gets only the loopback URL and a per-process secret; the proxy
// checks that secret and sends each call on with the agent's real token,
// read fresh for every request, so a token renewed on protocol 1.1 takes
// effect at once.
//
// This is a soft wall. The loopback port is reachable by every agent in the
// machine container, and the secret keeps them out. A root agent can read
// other processes' environments, so it could read the secret, just as it
// could read the token today. The wall keeps the real token out of the
// engine's environment, arguments, settings files and logs.

// tokenSource gives the latest tokens. *sdk.Credentials is one.
type tokenSource interface {
	GatewayToken() sdk.Secret
	MCPToken() sdk.Secret
}

// proxyConfig is what the proxy forwards to.
type proxyConfig struct {
	// ModelBaseURL is the welcome's model.baseUrl. Model calls go to its
	// root without the trailing /v1, under /anthropic.
	ModelBaseURL string
	// MCPURL is the welcome's streamable-http mcp.url. Empty answers /mcp
	// with 404.
	MCPURL string
	Tokens tokenSource
	// Log gets one line per failed upstream call. Lines never hold a
	// secret or a token.
	Log func(msg string)
}

// forwarded are the request headers the proxy passes on. Everything else,
// including X-Api-Key, cookies and the inbound Authorization, is dropped.
var forwarded = []string{
	"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta",
	"Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id",
}

// proxy is a loopback reverse proxy with a random port and secret.
type proxy struct {
	ln        net.Listener
	srv       *http.Server
	secret    string
	transport *http.Transport
}

// startProxy listens on 127.0.0.1 at a random port and serves until Close.
func startProxy(c proxyConfig) (*proxy, error) {
	if c.Tokens == nil {
		return nil, errors.New("the token proxy needs a token source")
	}
	gateway, err := anthropicRoot(c.ModelBaseURL)
	if err != nil {
		return nil, err
	}
	var mcp *url.URL
	if c.MCPURL != "" {
		if mcp, err = upstreamURL(c.MCPURL); err != nil {
			return nil, fmt.Errorf("the MCP URL: %w", err)
		}
	}
	if c.Log == nil {
		c.Log = func(string) {}
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails; it crashes the program instead
	// The outbound transport never uses the environment proxy: the gateway
	// and the MCP endpoint are on the inner network, reached directly.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	p := &proxy{secret: hex.EncodeToString(b), transport: tr}
	mux := http.NewServeMux()
	model := p.reverse(gateway, c.Tokens.GatewayToken, c.Log, true)
	mux.Handle("POST /v1/messages", model)
	mux.Handle("POST /v1/messages/count_tokens", model)
	if mcp != nil {
		mux.Handle("/mcp", p.reverse(mcp, c.Tokens.MCPToken, c.Log, false))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("the token proxy cannot listen on loopback: %w", err)
	}
	p.ln = ln
	p.srv = &http.Server{Handler: p.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.srv.Serve(ln) }() // Serve ends with ErrServerClosed on Close
	return p, nil
}

// URL is the base URL the engine calls.
func (p *proxy) URL() string { return "http://" + p.ln.Addr().String() }

// Secret is the per-process secret the engine sends as its bearer token.
func (p *proxy) Secret() string { return p.secret }

// String hides the secret.
func (p *proxy) String() string { return "token proxy at " + p.URL() }

// Close stops the proxy; calls in flight end.
func (p *proxy) Close() error {
	p.transport.CloseIdleConnections()
	return p.srv.Close() //nolint:wrapcheck // plain close error
}

// guard answers 401 to every request without the secret as its bearer
// token, compared in constant time, before any route is looked at.
func (p *proxy) guard(next http.Handler) http.Handler {
	want := []byte("Bearer " + p.secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// reverse forwards to target with the latest token. With keepPath the
// request path is put under target's path (the model routes); without it
// the request goes to target as it is (the MCP endpoint).
func (p *proxy) reverse(target *url.URL, token func() sdk.Secret, log func(string), keepPath bool) http.Handler {
	return &httputil.ReverseProxy{
		Transport:     p.transport,
		FlushInterval: -1, // pass SSE streams on at once
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := *target
			if keepPath {
				out.Path = target.Path + pr.In.URL.Path
			}
			out.RawPath = ""
			out.RawQuery = pr.In.URL.RawQuery
			pr.Out.URL = &out
			pr.Out.Host = target.Host
			h := http.Header{}
			for _, k := range forwarded {
				if v := pr.In.Header.Values(k); len(v) > 0 {
					h[k] = append([]string(nil), v...)
				}
			}
			h.Set("Authorization", "Bearer "+token().Reveal())
			pr.Out.Header = h
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// The error names the upstream URL and the cause; neither holds a token.
			log(fmt.Sprintf("token proxy: %s %s failed: %v", r.Method, r.URL.Path, err))
			http.Error(w, "the upstream is not reachable", http.StatusBadGateway)
		},
	}
}

// anthropicRoot is the gateway's Anthropic API root: model.baseUrl without
// its trailing /v1, then /anthropic (agreed with the gateway).
func anthropicRoot(baseURL string) (*url.URL, error) {
	u, err := upstreamURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("the model base URL: %w", err)
	}
	p := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/v1")
	u.Path = p + "/anthropic"
	return u, nil
}

func upstreamURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return nil, errors.New("it is not a URL")
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("it must use http or https")
	case u.Host == "":
		return nil, errors.New("it has no host")
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u, nil
}
