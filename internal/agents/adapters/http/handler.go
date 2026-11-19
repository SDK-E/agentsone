// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/api/agentsapi"
	"github.com/SDK-E/agentsone/internal/contract/api/budgetapi"
	"github.com/SDK-E/agentsone/internal/contract/api/chartapi"
	"github.com/SDK-E/agentsone/internal/contract/api/charterapi"
	"github.com/SDK-E/agentsone/internal/core"
)

const (
	defaultLimit = 50
	// heartbeatEvery is the contract's heartbeat_seconds for both streams.
	heartbeatEvery = 15 * time.Second
)

// Ticker starts a heartbeat: it returns the tick channel and a stop func.
type Ticker func() (<-chan time.Time, func())

func realTicker() (<-chan time.Time, func()) {
	t := time.NewTicker(heartbeatEvery)
	return t.C, t.Stop
}

// Handler serves the agents, chart, charter and budget groups. The agent
// template and token operations still answer 501 through the embedded
// Unimplemented type.
type Handler struct {
	agentsapi.Unimplemented
	s       *app.Society
	live    core.LiveList
	tickers Ticker
}

var (
	_ agentsapi.StrictServerInterface  = (*Handler)(nil)
	_ chartapi.StrictServerInterface   = (*Handler)(nil)
	_ charterapi.StrictServerInterface = (*Handler)(nil)
	_ budgetapi.StrictServerInterface  = (*Handler)(nil)
)

// Option changes a Handler.
type Option func(*Handler)

// WithTicker replaces the 15-second stream heartbeat, for tests.
func WithTicker(t Ticker) Option { return func(h *Handler) { h.tickers = t } }

// New returns a handler over the society. live is the router's live list,
// read for each agent's one-line.
func New(s *app.Society, live core.LiveList, opts ...Option) *Handler {
	h := &Handler{s: s, live: live, tickers: realTicker}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Groups returns the four groups the society serves.
func (h *Handler) Groups() []core.APIGroup {
	return []core.APIGroup{
		agentsapi.Group(h, problems),
		chartapi.Group(h, chartProblems),
		charterapi.Group(h, charterProblems),
		budgetapi.Group(h, budgetProblems),
	}
}

// problem turns the domain refusals that have their own contract problem
// type into that type; every other error goes on to core.ProblemFrom.
func problem(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrRootOnly):
		return core.NewProblem(http.StatusForbidden, "root-only", err.Error())
	case errors.Is(err, domain.ErrNotBelowActor):
		return core.NewProblem(http.StatusForbidden, "not-below-you", err.Error())
	}
	return err
}

type handlerFunc = func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error)

func wrap(f handlerFunc) handlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		resp, err := f(ctx, w, r, request)
		return resp, problem(err)
	}
}

func problems(f agentsapi.StrictHandlerFunc, _ string) agentsapi.StrictHandlerFunc {
	return agentsapi.StrictHandlerFunc(wrap(f))
}

func chartProblems(f chartapi.StrictHandlerFunc, _ string) chartapi.StrictHandlerFunc {
	return chartapi.StrictHandlerFunc(wrap(f))
}

func charterProblems(f charterapi.StrictHandlerFunc, _ string) charterapi.StrictHandlerFunc {
	return charterapi.StrictHandlerFunc(wrap(f))
}

func budgetProblems(f budgetapi.StrictHandlerFunc, _ string) budgetapi.StrictHandlerFunc {
	return budgetapi.StrictHandlerFunc(wrap(f))
}

func limitOf(p *int) int {
	if p == nil || *p < 1 {
		return defaultLimit
	}
	return *p
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
