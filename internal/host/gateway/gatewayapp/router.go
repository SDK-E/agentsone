// SPDX-License-Identifier: MIT

// Package gatewayapp holds the gateway's use cases: route one model call to a
// real model under the budget, and report the caller's budget.
//
// A call goes through these steps for each ranked model until one answers:
// check the provider's breaker, reserve the estimated cost (a refusal moves on
// to the next, maybe cheaper, model), fetch the provider key, and call the
// provider. Errors, timeouts, 401, 403, 429 and 5xx answers count as provider
// failures: the reservation is released, the breaker is told, and the next
// model is tried. Any other answer goes back to the caller. Its cost is settled
// when the caller closes the body: the real usage when the provider reported
// it, else the full estimate.
package gatewayapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/host/budget/ledger"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

// Errors returned by the router and its ports.
var (
	// ErrNoRoute means no enabled, healthy provider has a model for the call.
	ErrNoRoute = errors.New("gateway: no model available for this call")
	// ErrKeyNotFound is returned by a KeyStore that holds no key for a provider.
	ErrKeyNotFound = errors.New("gateway: no key stored for provider")
	// ErrKeyRequired is returned by an Upstream asked to call a provider that
	// needs a key without one.
	ErrKeyRequired = errors.New("gateway: provider needs a key")
)

// UpstreamError reports that every provider tried failed.
type UpstreamError struct {
	// Status is the last HTTP status seen, or 0 when the last try did not get
	// an answer.
	Status int
	Err    error
}

func (e *UpstreamError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("gateway: every provider failed, last status %d", e.Status)
	}
	return fmt.Sprintf("gateway: every provider failed: %v", e.Err)
}

func (e *UpstreamError) Unwrap() error { return e.Err }

// CatalogSource gives the current catalog; it may change between calls.
type CatalogSource interface {
	Catalog() routing.Catalog
}

// KeyStore gives a provider's API key.
type KeyStore interface {
	Get(ctx context.Context, p routing.ProviderID) (string, error)
}

// Budget is the part of the budget service the gateway uses.
type Budget interface {
	Reserve(c ledger.Caller, estimate ledger.Micros) (ledger.ReservationID, error)
	Settle(id ledger.ReservationID, actual ledger.Micros) error
	RemainingFor(c ledger.Caller) ledger.Micros
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// Op is what an upstream call does.
type Op int

// Upstream operations.
const (
	// OpGenerate asks the model for an answer; it costs money.
	OpGenerate Op = iota
	// OpCountTokens asks the Messages API to count a request's input tokens;
	// it is free.
	OpCountTokens
)

// UpstreamCall is one call to one provider.
type UpstreamCall struct {
	Op       Op
	Provider routing.ProviderID
	API      routing.API
	Model    string
	// Key is empty when none is stored.
	Key string
	// Body is the caller's request body; the upstream rewrites the model name,
	// output limit and usage options for the provider.
	Body            []byte
	Stream          bool
	MaxOutputTokens int64
	// Header holds caller headers worth passing on, such as anthropic-version.
	Header http.Header
}

// UpstreamResponse is a provider's answer.
type UpstreamResponse struct {
	Status int
	Header http.Header
	Body   io.ReadCloser
	// Usage reports the tokens used, once Body has been read to the end. The
	// second result is false when the provider did not report usage.
	Usage func() (routing.Usage, bool)
}

// Upstream sends calls to providers.
type Upstream interface {
	Call(ctx context.Context, c UpstreamCall) (*UpstreamResponse, error)
}

// Limits bound each call. Zero values take the defaults.
type Limits struct {
	// DefaultMaxOutputTokens is used when the caller sets no output limit.
	DefaultMaxOutputTokens int64
	// MaxOutputTokensCap is the largest output limit a call may have.
	MaxOutputTokensCap int64
	// MaxAttempts is how many providers one call may try.
	MaxAttempts int
}

// Default limits.
const (
	DefaultMaxOutputTokens = 4096
	DefaultMaxOutputCap    = 32768
	DefaultMaxAttempts     = 3
)

func (l Limits) withDefaults() Limits {
	if l.DefaultMaxOutputTokens <= 0 {
		l.DefaultMaxOutputTokens = DefaultMaxOutputTokens
	}
	if l.MaxOutputTokensCap <= 0 {
		l.MaxOutputTokensCap = DefaultMaxOutputCap
	}
	if l.MaxAttempts <= 0 {
		l.MaxAttempts = DefaultMaxAttempts
	}
	return l
}

// Deps are the router's ports.
type Deps struct {
	Catalog  CatalogSource
	Keys     KeyStore
	Upstream Upstream
	Budget   Budget
	Breaker  *routing.Breaker
	Clock    Clock
	Limits   Limits
}

// Router routes model calls.
type Router struct {
	d Deps
}

// NewRouter builds a router.
func NewRouter(d Deps) *Router {
	d.Limits = d.Limits.withDefaults()
	return &Router{d: d}
}

// CallRequest is one model call from an agent.
type CallRequest struct {
	Caller ledger.Caller
	Engine routing.API
	Weight routing.Weight
	Body   []byte
	Stream bool
	// MaxOutputTokens is the caller's output limit, 0 when not set.
	MaxOutputTokens int64
	Header          http.Header
}

// CallResult is the answer to pass back to the caller. Body must be closed;
// closing it settles the call's cost.
type CallResult struct {
	Model  routing.Model
	Status int
	Header http.Header
	Body   io.ReadCloser
}

// Call routes one model call.
func (r *Router) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	now := r.d.Clock.Now()
	ranked := routing.Rank(r.d.Catalog.Catalog(), routing.Request{Engine: req.Engine, Weight: req.Weight},
		func(p routing.ProviderID) bool { return r.d.Breaker.Healthy(p, now) })
	if len(ranked) == 0 {
		return nil, ErrNoRoute
	}
	maxOut := req.MaxOutputTokens
	if maxOut <= 0 {
		maxOut = r.d.Limits.DefaultMaxOutputTokens
	}
	maxOut = min(maxOut, r.d.Limits.MaxOutputTokensCap)

	var budgetErr, lastErr error
	lastStatus, attempts := 0, 0
	for _, m := range ranked {
		if attempts >= r.d.Limits.MaxAttempts {
			break
		}
		estimate := routing.Estimate(m, int64(len(req.Body)), maxOut)
		id, err := r.d.Budget.Reserve(req.Caller, estimate)
		if err != nil {
			budgetErr = err
			continue
		}
		// The key comes before the breaker, so a key store error cannot use up
		// a half-open provider's only probe.
		key, err := r.d.Keys.Get(ctx, m.Provider)
		if err != nil && !errors.Is(err, ErrKeyNotFound) {
			r.release(id)
			lastErr = err
			continue
		}
		if !r.d.Breaker.Allow(m.Provider, r.d.Clock.Now()) {
			r.release(id)
			continue
		}
		attempts++
		resp, err := r.d.Upstream.Call(ctx, UpstreamCall{
			Provider: m.Provider, API: req.Engine, Model: m.ID, Key: key, Body: req.Body,
			Stream: req.Stream, MaxOutputTokens: maxOut, Header: req.Header,
		})
		if err != nil && ctx.Err() != nil {
			// The caller went away; that says nothing about the provider.
			r.release(id)
			r.d.Breaker.Abandon(m.Provider)
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrKeyRequired) {
			r.release(id)
			// A provider without its key is not used, and not counted against
			// its health either: the human has not finished setting it up.
			r.d.Breaker.Abandon(m.Provider)
			lastErr = err
			continue
		}
		if err != nil {
			r.release(id)
			r.d.Breaker.Failure(m.Provider, r.d.Clock.Now())
			lastErr, lastStatus = err, 0
			continue
		}
		if providerFailed(resp.Status) {
			_ = resp.Body.Close()
			r.release(id)
			r.d.Breaker.Failure(m.Provider, r.d.Clock.Now())
			lastErr, lastStatus = fmt.Errorf("provider %s answered %d", m.Provider, resp.Status), resp.Status
			continue
		}
		r.d.Breaker.Success(m.Provider)
		return &CallResult{
			Model:  m,
			Status: resp.Status,
			Header: resp.Header,
			Body:   &settlingBody{rc: resp.Body, settle: r.settler(id, m, estimate, resp.Usage)},
		}, nil
	}
	if attempts == 0 && budgetErr != nil {
		return nil, budgetErr
	}
	if attempts == 0 && lastErr == nil {
		return nil, ErrNoRoute
	}
	return nil, &UpstreamError{Status: lastStatus, Err: lastErr}
}

// providerFailed reports answers that mean the provider, not the caller, has a
// problem.
func providerFailed(status int) bool {
	return status >= 500 || status == http.StatusTooManyRequests ||
		status == http.StatusUnauthorized || status == http.StatusForbidden
}

func (r *Router) release(id ledger.ReservationID) {
	// The reservation was made just now by this call; releasing it cannot
	// meet an unknown id.
	_ = r.d.Budget.Settle(id, 0)
}

func (r *Router) settler(id ledger.ReservationID, m routing.Model, estimate ledger.Micros,
	usage func() (routing.Usage, bool),
) func() error {
	return func() error {
		cost := estimate
		if usage != nil {
			if u, ok := usage(); ok {
				cost = m.Cost(u)
			}
		}
		return r.d.Budget.Settle(id, cost)
	}
}

// settlingBody settles the call's cost once, when closed.
type settlingBody struct {
	rc     io.ReadCloser
	settle func() error
	once   sync.Once
	err    error
}

func (b *settlingBody) Read(p []byte) (int, error) { return b.rc.Read(p) }

func (b *settlingBody) Close() error {
	b.once.Do(func() {
		b.err = errors.Join(b.rc.Close(), b.settle())
	})
	return b.err
}

// CountRequest asks for a Messages request's input token count.
type CountRequest struct {
	Weight routing.Weight
	Body   []byte
	Header http.Header
}

// CountTokens passes a token count request to the Claude model the same call
// would be routed to. Counting is free, so no budget is reserved, and its
// answers do not change provider health or use a half-open probe; providers
// that fail are skipped for the next.
func (r *Router) CountTokens(ctx context.Context, req CountRequest) (*CallResult, error) {
	now := r.d.Clock.Now()
	ranked := routing.Rank(r.d.Catalog.Catalog(), routing.Request{Engine: routing.APIAnthropic, Weight: req.Weight},
		func(p routing.ProviderID) bool { return r.d.Breaker.State(p, now) == routing.BreakerClosed })
	if len(ranked) == 0 {
		return nil, ErrNoRoute
	}
	var lastErr error
	lastStatus := 0
	for i, m := range ranked {
		if i >= r.d.Limits.MaxAttempts {
			break
		}
		key, err := r.d.Keys.Get(ctx, m.Provider)
		if err != nil && !errors.Is(err, ErrKeyNotFound) {
			lastErr = err
			continue
		}
		resp, err := r.d.Upstream.Call(ctx, UpstreamCall{
			Op: OpCountTokens, Provider: m.Provider, API: routing.APIAnthropic, Model: m.ID,
			Key: key, Body: req.Body, Header: req.Header,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr, lastStatus = err, 0
			continue
		}
		if providerFailed(resp.Status) {
			_ = resp.Body.Close()
			lastErr, lastStatus = fmt.Errorf("provider %s answered %d", m.Provider, resp.Status), resp.Status
			continue
		}
		return &CallResult{Model: m, Status: resp.Status, Header: resp.Header, Body: resp.Body}, nil
	}
	return nil, &UpstreamError{Status: lastStatus, Err: lastErr}
}

// BudgetView is what a caller may know about its budget.
type BudgetView struct {
	Exhausted bool
	Remaining ledger.Micros
	Defence   bool
}

// Budget reports the caller's remaining budget, so paused runners can poll it.
func (r *Router) Budget(c ledger.Caller) BudgetView {
	left := r.d.Budget.RemainingFor(c)
	return BudgetView{Exhausted: left <= 0, Remaining: left, Defence: c.Defence}
}
