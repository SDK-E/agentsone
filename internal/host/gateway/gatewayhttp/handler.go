// SPDX-License-Identifier: MIT

// Package gatewayhttp serves the model gateway over HTTP.
//
// Endpoints:
//
//	POST /v1/chat/completions    OpenAI-compatible; routed to any enabled provider
//	GET  /v1/models              lists the one model, autoagents/one
//	POST /anthropic/v1/messages  Anthropic Messages, for the Claude engine through
//	                             ANTHROPIC_BASE_URL; routed only to Claude models
//	POST /anthropic/v1/messages/count_tokens
//	                             token counting for the Claude engine; free, so
//	                             not charged to the budget
//	GET  /v1/budget              the caller's budget, for paused runners to poll
//
// Every request carries an agent gateway token, as "Authorization: Bearer" or,
// on the Anthropic endpoint, "x-api-key". The token's identity is SOFT: root
// inside the VM can forge it. It decides the per-agent budget split and the
// defence flag, never the total budget, which the host enforces regardless.
//
// The model name must be autoagents/one, optionally with a weight suffix
// (autoagents/one:quick, :normal, :deep). Otherwise the X-Agentsone-Weight
// header gives the weight; the default is normal.
//
// Errors are application/problem+json (RFC 9457). When the budget is spent
// the answer is 402 with type urn:agentsone:problem:budget-exhausted and a
// Retry-After header.
package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/gatewaytoken"
	"github.com/SDK-E/agentsone/internal/host/budget/ledger"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

// MaxRequestBytes is the largest request body accepted.
const MaxRequestBytes = 32 << 20

// WeightHeader carries the weight hint.
const WeightHeader = "X-Agentsone-Weight"

// ModelHeader on answers names the real model that served the call.
const ModelHeader = "X-Agentsone-Model"

// Problem types.
const (
	ProblemUnauthorized     = "urn:agentsone:problem:token-invalid"
	ProblemUnknownModel     = "urn:agentsone:problem:unknown-model"
	ProblemBadWeight        = "urn:agentsone:problem:bad-weight"
	ProblemBadRequest       = "urn:agentsone:problem:bad-request"
	ProblemTooLarge         = "urn:agentsone:problem:too-large"
	ProblemBudgetExhausted  = "urn:agentsone:problem:budget-exhausted"
	ProblemModelUnavailable = "urn:agentsone:problem:model-unavailable"
	ProblemUpstreamFailed   = "urn:agentsone:problem:upstream-failed"
	ProblemInternal         = "urn:agentsone:problem:internal"
)

// Router is the part of the gateway application the handlers use.
type Router interface {
	Call(ctx context.Context, req gatewayapp.CallRequest) (*gatewayapp.CallResult, error)
	CountTokens(ctx context.Context, req gatewayapp.CountRequest) (*gatewayapp.CallResult, error)
	Budget(c ledger.Caller) gatewayapp.BudgetView
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// Options configure the handler.
type Options struct {
	Router Router
	// Key returns the current per-boot token key; nil refuses every token.
	Key   func() []byte
	Clock Clock
	// RetryAfter is sent with 402 and 503 answers.
	RetryAfter time.Duration
	Log        *slog.Logger
}

type handler struct {
	o Options
}

// New returns the gateway's HTTP handler.
func New(o Options) http.Handler {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.RetryAfter <= 0 {
		o.RetryAfter = 30 * time.Second
	}
	h := &handler{o: o}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", h.authed(false, h.call(routing.APIOpenAI)))
	mux.HandleFunc("POST /anthropic/v1/messages", h.authed(true, h.call(routing.APIAnthropic)))
	mux.HandleFunc("POST /anthropic/v1/messages/count_tokens", h.authed(true, h.countTokens))
	mux.HandleFunc("GET /v1/models", h.authed(false, h.models))
	mux.HandleFunc("GET /v1/budget", h.authed(false, h.budget))
	return mux
}

type authedFunc func(w http.ResponseWriter, r *http.Request, c gatewaytoken.Claims)

func (h *handler) authed(allowAPIKey bool, next authedFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok && allowAPIKey {
			tok = r.Header.Get("X-Api-Key")
		}
		var key []byte
		if h.o.Key != nil {
			key = h.o.Key()
		}
		claims, err := gatewaytoken.Verify(key, strings.TrimSpace(tok), h.o.Clock.Now())
		if err != nil {
			h.problem(w, http.StatusUnauthorized, ProblemUnauthorized, "Gateway token refused",
				"Send the agent gateway token the daemon issued: "+err.Error(), nil)
			return
		}
		next(w, r, claims)
	}
}

func caller(c gatewaytoken.Claims) ledger.Caller {
	return ledger.Caller{Agent: c.Agent, Defence: c.Defence}
}

// callFields are the request fields the gateway reads; the rest pass through.
type callFields struct {
	Model               string `json:"model"`
	Stream              bool   `json:"stream"`
	MaxTokens           int64  `json:"max_tokens"`
	MaxCompletionTokens int64  `json:"max_completion_tokens"`
}

// readCall reads and checks a model request. On failure it has already
// written the problem answer and returns ok false.
func (h *handler) readCall(w http.ResponseWriter, r *http.Request) (body []byte, f callFields, weight routing.Weight, ok bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.problem(w, http.StatusRequestEntityTooLarge, ProblemTooLarge, "Request too large",
				"The request body is over "+strconv.Itoa(MaxRequestBytes)+" bytes.", nil)
			return nil, f, "", false
		}
		h.problem(w, http.StatusBadRequest, ProblemBadRequest, "Request not read", err.Error(), nil)
		return nil, f, "", false
	}
	if err := json.Unmarshal(body, &f); err != nil {
		h.problem(w, http.StatusBadRequest, ProblemBadRequest, "Request is not valid JSON", err.Error(), nil)
		return nil, f, "", false
	}
	weight, err = routing.ResolveWeight(f.Model, r.Header.Get(WeightHeader))
	if errors.Is(err, routing.ErrBadWeight) {
		h.problem(w, http.StatusBadRequest, ProblemBadWeight, "Unknown weight", err.Error(), nil)
		return nil, f, "", false
	}
	if err != nil {
		h.problem(w, http.StatusBadRequest, ProblemUnknownModel, "Unknown model", err.Error(), nil)
		return nil, f, "", false
	}
	return body, f, weight, true
}

// countTokens passes a Messages token count to a Claude model. Counting is
// free, so it is not charged to the budget.
func (h *handler) countTokens(w http.ResponseWriter, r *http.Request, c gatewaytoken.Claims) {
	body, _, weight, ok := h.readCall(w, r)
	if !ok {
		return
	}
	res, err := h.o.Router.CountTokens(r.Context(), gatewayapp.CountRequest{
		Weight: weight, Body: body, Header: passHeaders(r.Header),
	})
	if err != nil {
		h.callError(w, err)
		return
	}
	h.relay(w, c, res)
}

func (h *handler) call(engine routing.API) authedFunc {
	return func(w http.ResponseWriter, r *http.Request, c gatewaytoken.Claims) {
		body, f, weight, ok := h.readCall(w, r)
		if !ok {
			return
		}
		maxOut := f.MaxCompletionTokens
		if maxOut <= 0 {
			maxOut = f.MaxTokens
		}
		res, err := h.o.Router.Call(r.Context(), gatewayapp.CallRequest{
			Caller: caller(c), Engine: engine, Weight: weight, Body: body, Stream: f.Stream,
			MaxOutputTokens: max(maxOut, 0), Header: passHeaders(r.Header),
		})
		if err != nil {
			h.callError(w, err)
			return
		}
		h.relay(w, c, res)
	}
}

func passHeaders(in http.Header) http.Header {
	out := http.Header{}
	for _, k := range []string{"Anthropic-Version", "Anthropic-Beta"} {
		if v := in.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	return out
}

// relayHeaders are the provider answer headers passed back to the caller.
var relayHeaders = []string{"Content-Type", "Cache-Control", "Request-Id", "X-Request-Id", "Anthropic-Organization-Id"}

func (h *handler) relay(w http.ResponseWriter, c gatewaytoken.Claims, res *gatewayapp.CallResult) {
	defer func() {
		if err := res.Body.Close(); err != nil {
			h.o.Log.Error("gateway: settle call", "agent", c.Agent, "model", res.Model.ID, "err", err)
		}
	}()
	for _, k := range relayHeaders {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set(ModelHeader, string(res.Model.Provider)+"/"+res.Model.ID)
	w.WriteHeader(res.Status)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // the caller went away; Close still settles
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				h.o.Log.Warn("gateway: provider stream ended early", "agent", c.Agent, "model", res.Model.ID, "err", err)
			}
			return
		}
	}
}

func (h *handler) callError(w http.ResponseWriter, err error) {
	retry := int(h.o.RetryAfter / time.Second)
	var up *gatewayapp.UpstreamError
	switch {
	case errors.Is(err, ledger.ErrBudgetExhausted), errors.Is(err, ledger.ErrAllocationExhausted):
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		h.problem(w, http.StatusPaymentRequired, ProblemBudgetExhausted, "Budget exhausted",
			"No budget is left for this call. Keep the turn and retry when GET /v1/budget shows budget again.",
			map[string]any{"retry_after_seconds": retry})
	case errors.Is(err, gatewayapp.ErrNoRoute):
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		h.problem(w, http.StatusServiceUnavailable, ProblemModelUnavailable, "No model available",
			"No enabled, healthy provider has a model for this call.", map[string]any{"retry_after_seconds": retry})
	case errors.As(err, &up):
		h.problem(w, http.StatusBadGateway, ProblemUpstreamFailed, "Providers failed", up.Error(), nil)
	default:
		h.o.Log.Error("gateway: call failed", "err", err)
		h.problem(w, http.StatusInternalServerError, ProblemInternal, "Gateway error", "The gateway could not route the call.", nil)
	}
}

func (h *handler) models(w http.ResponseWriter, _ *http.Request, _ gatewaytoken.Claims) {
	writeJSON(w, http.StatusOK, "application/json", map[string]any{
		"object": "list",
		"data": []map[string]any{{
			"id": routing.PublicModel, "object": "model", "created": 0, "owned_by": "agentsone",
		}},
	})
}

func (h *handler) budget(w http.ResponseWriter, _ *http.Request, c gatewaytoken.Claims) {
	v := h.o.Router.Budget(caller(c))
	writeJSON(w, http.StatusOK, "application/json", map[string]any{
		"agent":               c.Agent,
		"defence":             v.Defence,
		"exhausted":           v.Exhausted,
		"remaining_micro_usd": int64(v.Remaining),
		"remaining_usd":       v.Remaining.String(),
		"retry_after_seconds": int(h.o.RetryAfter / time.Second),
		// The caller's identity comes from a token root in the VM can forge.
		"identity": "soft",
	})
}

func (h *handler) problem(w http.ResponseWriter, status int, typ, title, detail string, ext map[string]any) {
	p := map[string]any{"type": typ, "title": title, "status": status, "detail": detail}
	for k, v := range ext {
		p[k] = v
	}
	writeJSON(w, status, "application/problem+json", p)
}

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
