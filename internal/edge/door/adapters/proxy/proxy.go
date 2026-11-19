// SPDX-License-Identifier: MIT

// Package proxy is the web door's reverse proxy to the machine's daemon. It passes HTTP, server
// sent event streams (flushed at once, never buffered) and WebSocket upgrades (terminals, the
// desktop VNC bridge). Before a request leaves, every forwarding and identity header the client
// sent is removed and the door sets exactly two of its own: X-Forwarded-For (the peer that
// connected to the door) and X-Forwarded-Proto. It never sends a human's identity.
package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/SDK-E/agentsone/internal/edge/door/domain"
)

// ProblemUnavailable is the problem type when the machine does not answer.
const ProblemUnavailable = "urn:agentsone:problem:unavailable"

// New returns the reverse proxy to upstream (scheme and host of the daemon, such as
// http://machine:7700).
func New(upstream *url.URL, log *slog.Logger) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Rewrite has already dropped Forwarded and X-Forwarded-* from Out; the claim is read
			// from In before it is gone from view.
			proto := domain.Proto(pr.In.TLS != nil, pr.In.Header.Values("X-Forwarded-Proto"))
			pr.SetURL(upstream)
			pr.Out.Host = upstream.Host
			domain.Strip(pr.Out.Header)
			if ip := domain.ClientIP(pr.In.RemoteAddr); ip != "" {
				pr.Out.Header.Set("X-Forwarded-For", ip)
			}
			pr.Out.Header.Set("X-Forwarded-Proto", proto)
		},
		// Flush every write: event streams and long polls reach the browser as they happen.
		FlushInterval: -1,
		ErrorLog:      slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("door: machine did not answer", "path", r.URL.Path, "err", err)
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": ProblemUnavailable, "title": "Machine unavailable", "status": http.StatusBadGateway,
				"detail": "The machine did not answer. It may be starting; try again shortly.",
			})
		},
	}
}
