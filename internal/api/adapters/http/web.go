// SPDX-License-Identifier: MIT

package httpadapter

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// ContentSecurityPolicy is the interface's policy: everything from our own
// origin, no inline script. Panel plugins load in sandboxed same-origin
// iframes (frame-src 'self'); nothing may frame us.
const ContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// SecurityHeaders sets headers every response carries.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, contract.BasePath+"/") {
			h.Set("Cache-Control", "no-store")
		} else {
			h.Set("Content-Security-Policy", ContentSecurityPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

// Split sends /v1/ and /ide/ to api (the composed modules) and every other
// path to ui.
func Split(api, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, contract.BasePath+"/") || strings.HasPrefix(r.URL.Path, "/ide/") {
			api.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}

// UI serves the built interface from dir. A GET for a path that is not a
// file gets index.html, so client routes such as /agents/ada/chat load.
func UI(dir fs.FS) http.Handler {
	files := http.FileServerFS(dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			core.WriteProblem(w, r, core.NewProblem(http.StatusMethodNotAllowed, "method-not-allowed", "the interface is read only"))
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if st, err := fs.Stat(dir, name); err == nil && !st.IsDir() || name == "." {
			files.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(dir, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusNotFound, "not-found", "the interface is not installed"))
			return
		}
		if err != nil {
			core.WriteError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index) //nolint:errcheck // the status is sent; a failed write means the client left
	})
}

// statusRecorder remembers the status for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b) //nolint:wrapcheck // pass-through writer
}

// Flush sends what was written so far. Event streams need it: the
// generated stream handlers look for http.Flusher on the writer itself, and
// without it they copy into the server's buffer, which holds each frame
// until 4 KB have piled up or the stream ends.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// RequestLog logs one line per request: listener, method, path, status and
// duration. It never logs headers, query strings or bodies, so tokens,
// cookies and passwords stay out of logs.
func RequestLog(log *slog.Logger, listener string, clock core.Clock, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := clock.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("listener", listener), slog.String("method", r.Method), slog.String("path", r.URL.Path),
			slog.Int("status", rec.status), slog.Duration("took", clock.Now().Sub(start).Round(time.Microsecond)),
		)
	})
}
