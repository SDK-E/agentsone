// SPDX-License-Identifier: MIT

package httpadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"net/url"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

// Errors of the dispatcher.
var (
	// ErrNoCaller means the context carries no caller. A tool call never runs anonymously.
	ErrNoCaller = errors.New("no caller on the call")
	// ErrNotReady means the API handler is not set yet.
	ErrNotReady = errors.New("api handler not ready")
)

// inProcessAddr is the remote address an in-process request carries. It is
// not a network address, so nothing mistakes it for a local TCP peer.
const inProcessAddr = "mcp-in-process"

// HandlerDispatcher runs requests through the composed API handler without a
// network hop. The handler reads the caller from the context, so the tool
// call acts as the MCP session's caller and as nobody else.
type HandlerDispatcher struct {
	mu sync.RWMutex
	h  nethttp.Handler
}

var _ app.Dispatcher = (*HandlerDispatcher)(nil)

// NewHandlerDispatcher builds a dispatcher. h may be nil and set later.
func NewHandlerDispatcher(h nethttp.Handler) *HandlerDispatcher {
	return &HandlerDispatcher{h: h}
}

// SetHandler sets the composed API handler.
func (d *HandlerDispatcher) SetHandler(h nethttp.Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.h = h
}

// Dispatch implements app.Dispatcher.
func (d *HandlerDispatcher) Dispatch(ctx context.Context, r domain.Request) (app.Reply, error) {
	if _, ok := core.CallerFrom(ctx); !ok {
		return app.Reply{}, ErrNoCaller
	}
	d.mu.RLock()
	h := d.h
	d.mu.RUnlock()
	if h == nil {
		return app.Reply{}, ErrNotReady
	}

	target := url.URL{Path: r.Path, RawQuery: r.Query.Encode()}
	req, err := nethttp.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(r.Body))
	if err != nil {
		return app.Reply{}, err
	}
	req.RemoteAddr = inProcessAddr
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := &recorder{header: nethttp.Header{}, status: nethttp.StatusOK}
	h.ServeHTTP(rec, req)
	return app.Reply{Status: rec.status, Body: rec.body.Bytes()}, nil
}

// recorder is the smallest nethttp.ResponseWriter that keeps the answer.
type recorder struct {
	header      nethttp.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

var _ io.Writer = (*recorder)(nil)

func (r *recorder) Header() nethttp.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader, r.status = true, status
}

func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(nethttp.StatusOK)
	return r.body.Write(b)
}
