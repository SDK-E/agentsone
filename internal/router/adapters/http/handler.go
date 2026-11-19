// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/routerapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/app"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	// heartbeatEvery is the contract's heartbeat_seconds for the stream.
	heartbeatEvery = 15 * time.Second
	// reconcileEvery is how often a stream rereads the society to catch state
	// changes, new agents and retired ones.
	reconcileEvery = 3 * time.Second
)

// Live is the live list use cases the handler needs.
type Live interface {
	Page(ctx context.Context, cursor string, limit int) ([]domain.Entry, string, error)
	Watch(ctx context.Context, ticks <-chan time.Time) <-chan app.Change
}

// Routing is the routing use cases the handler needs.
type Routing interface {
	Route(ctx context.Context, text, router string) (domain.Assignment, error)
	Get(ctx context.Context, id string) (domain.Assignment, error)
}

// Ticker starts a periodic tick: it returns the channel and a stop func.
type Ticker func(every time.Duration) (<-chan time.Time, func())

func realTicker(every time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(every)
	return t.C, t.Stop
}

// Handler serves the router group.
type Handler struct {
	routerapi.Unimplemented
	live    Live
	routing Routing
	clock   core.Clock
	tick    Ticker
}

var _ routerapi.StrictServerInterface = (*Handler)(nil)

// Option changes a Handler.
type Option func(*Handler)

// WithTicker replaces the real timers behind the stream, for tests.
func WithTicker(t Ticker) Option { return func(h *Handler) { h.tick = t } }

// New returns a handler over the use cases.
func New(live Live, routing Routing, clock core.Clock, opts ...Option) *Handler {
	h := &Handler{live: live, routing: routing, clock: clock, tick: realTicker}
	for _, o := range opts {
		o(h)
	}
	return h
}

// ListLiveList returns one page of the live list.
func (h *Handler) ListLiveList(ctx context.Context, req routerapi.ListLiveListRequestObject) (routerapi.ListLiveListResponseObject, error) {
	limit := defaultLimit
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if limit < 1 || limit > maxLimit {
		return nil, fmt.Errorf("limit %d must be 1 to %d: %w", limit, maxLimit, core.ErrInvalid)
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	entries, next, err := h.live.Page(ctx, cursor, limit)
	if err != nil {
		return nil, err
	}
	return routerapi.ListLiveList200JSONResponse(toLiveList(entries, next)), nil
}

// RouteRequest assigns a human request to an agent's chat.
func (h *Handler) RouteRequest(ctx context.Context, req routerapi.RouteRequestRequestObject) (routerapi.RouteRequestResponseObject, error) {
	if req.Body == nil {
		return nil, fmt.Errorf("the request body is missing: %w", core.ErrInvalid)
	}
	router := ""
	if req.Body.Router != nil {
		router = *req.Body.Router
	}
	a, err := h.routing.Route(ctx, req.Body.Text, router)
	if err != nil {
		return nil, err
	}
	return routerapi.RouteRequest201JSONResponse(toAssignment(a)), nil
}

// GetAssignment returns one routing result.
func (h *Handler) GetAssignment(ctx context.Context, req routerapi.GetAssignmentRequestObject) (routerapi.GetAssignmentResponseObject, error) {
	a, err := h.routing.Get(ctx, req.AssignmentId)
	if err != nil {
		return nil, err
	}
	return routerapi.GetAssignment200JSONResponse(toAssignment(a)), nil
}
