// SPDX-License-Identifier: MIT

// Package edgeadmin is the control page's client of the edge admin socket. It is the control
// service's only way to the edge, and implements every edge port of the control app: the
// passphrase hash, the allow list, the providers and their keys, the budget and the freeze-all
// switch.
//
// Routes used (served by the edge parts that own them):
//
//	GET|PUT|DELETE    /v1/control/credential          this package's credstore
//	GET|POST          /v1/net/allow-list              internet filter
//	DELETE            /v1/net/allow-list/{pattern}
//	GET               /v1/gateway/providers           model gateway
//	PUT|DELETE        /v1/gateway/providers/{id}
//	PUT|DELETE        /v1/gateway/providers/{id}/key
//	GET|PATCH         /v1/gateway/budget
//	PATCH             /v1/gateway/budget/defence
//	GET|POST|DELETE   /v1/freeze                      freeze all
package edgeadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

// DefaultTimeout bounds one call to the edge.
const DefaultTimeout = 10 * time.Second

// maxAnswer bounds an answer from the edge.
const maxAnswer = 1 << 20

// Client talks to the edge admin socket.
type Client struct {
	http *http.Client
}

// New returns a client of the admin socket at path.
func New(path string, timeout time.Duration) *Client {
	return &Client{http: &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
			MaxIdleConns: 4,
		},
	}}
}

type problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// statusError is an answer the client did not expect, kept for the callers that map it.
type statusError struct {
	status int
	slug   string
	detail string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("edge answered %d %s", e.status, e.slug)
}

// call sends in as JSON (when not nil) and reads the answer into out (when not nil). Edge
// refusals (validation, conflict, not found) become *app.Refusal with the edge's detail; an edge
// that does not answer or fails becomes app.ErrEdgeUnavailable.
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("edge admin: encode: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://edge"+path, body)
	if err != nil {
		return fmt.Errorf("edge admin: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The error names the method and path only; the body (a key, a hash) is never in it.
		return fmt.Errorf("%w: %s %s: %w", app.ErrEdgeUnavailable, method, path, unwrapURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("%w: read answer: %w", app.ErrEdgeUnavailable, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%w: %s %s: unreadable answer", app.ErrEdgeUnavailable, method, path)
		}
		return nil
	}
	var p problem
	_ = json.Unmarshal(data, &p)
	slug := strings.TrimPrefix(p.Type, "urn:agentsone:problem:")
	se := &statusError{status: resp.StatusCode, slug: slug, detail: p.Detail}
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusLocked:
		return fmt.Errorf("%w: %s %s: %w", app.ErrEdgeUnavailable, method, path, se)
	case resp.StatusCode == http.StatusNotFound && slug == "":
		// A route the edge does not have: that part of the edge is not built or not running.
		return fmt.Errorf("%w: %s %s: no such route", app.ErrEdgeUnavailable, method, path)
	default:
		detail := p.Detail
		if detail == "" {
			detail = "The edge refused this."
		}
		return errors.Join(&app.Refusal{Detail: detail}, se)
	}
}

func unwrapURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}

// --- passphrase hash ---

type credential struct {
	Hash string `json:"hash"`
}

// Get returns the stored hash, or app.ErrNoCredential.
func (c *Client) Get(ctx context.Context) (string, error) {
	var out credential
	err := c.call(ctx, http.MethodGet, "/v1/control/credential", nil, &out)
	if statusOf(err) == http.StatusNotFound {
		return "", app.ErrNoCredential
	}
	if err != nil {
		return "", err
	}
	return out.Hash, nil
}

// Create stores the first hash, or returns app.ErrCredentialSet.
func (c *Client) Create(ctx context.Context, hash string) error {
	err := c.call(ctx, http.MethodPut, "/v1/control/credential", credential{Hash: hash}, nil)
	if statusOf(err) == http.StatusConflict {
		return app.ErrCredentialSet
	}
	return err
}

// Reset clears the hash: the next visit to the control page asks for a new setup code.
func (c *Client) Reset(ctx context.Context) error {
	return c.call(ctx, http.MethodDelete, "/v1/control/credential", nil, nil)
}

// --- allow list ---

type allowEntry struct {
	Pattern string `json:"pattern"`
	Why     string `json:"why"`
}

// AllowList returns the allow list.
func (c *Client) AllowList(ctx context.Context) ([]app.AllowEntry, error) {
	var out struct {
		Entries []allowEntry `json:"entries"`
	}
	if err := c.call(ctx, http.MethodGet, "/v1/net/allow-list", nil, &out); err != nil {
		return nil, err
	}
	entries := make([]app.AllowEntry, 0, len(out.Entries))
	for _, e := range out.Entries {
		entries = append(entries, app.AllowEntry(e))
	}
	return entries, nil
}

// Allow adds an entry.
func (c *Client) Allow(ctx context.Context, pattern, why string) (app.AllowEntry, error) {
	var out allowEntry
	if err := c.call(ctx, http.MethodPost, "/v1/net/allow-list", allowEntry{Pattern: pattern, Why: why}, &out); err != nil {
		return app.AllowEntry{}, err
	}
	return app.AllowEntry(out), nil
}

// Disallow removes an entry.
func (c *Client) Disallow(ctx context.Context, pattern string) error {
	return c.call(ctx, http.MethodDelete, "/v1/net/allow-list/"+url.PathEscape(pattern), nil, nil)
}

// --- providers and keys ---

type keyState struct {
	Set         bool   `json:"set"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type model struct {
	ID   string `json:"id"`
	Tier string `json:"tier"`
}

type provider struct {
	ID          string   `json:"id"`
	Enabled     bool     `json:"enabled"`
	KeyRequired bool     `json:"key_required"`
	Key         keyState `json:"key"`
	Models      []model  `json:"models"`
}

// Providers returns the providers with their key state.
func (c *Client) Providers(ctx context.Context) ([]app.Provider, error) {
	var out struct {
		Providers []provider `json:"providers"`
	}
	if err := c.call(ctx, http.MethodGet, "/v1/gateway/providers", nil, &out); err != nil {
		return nil, err
	}
	ps := make([]app.Provider, 0, len(out.Providers))
	for _, p := range out.Providers {
		models := make([]app.Model, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, app.Model(m))
		}
		ps = append(ps, app.Provider{ID: p.ID, Enabled: p.Enabled, KeyRequired: p.KeyRequired, Key: app.KeyState(p.Key), Models: models})
	}
	return ps, nil
}

func providerPath(id string) string { return "/v1/gateway/providers/" + url.PathEscape(id) }

// Enable turns a provider on.
func (c *Client) Enable(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPut, providerPath(id), nil, nil)
}

// Disable turns a provider off.
func (c *Client) Disable(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, providerPath(id), nil, nil)
}

// SetKey sends the key to the edge. Only the key state comes back.
func (c *Client) SetKey(ctx context.Context, id, key string) (app.KeyState, error) {
	var out keyState
	if err := c.call(ctx, http.MethodPut, providerPath(id)+"/key", struct {
		Key string `json:"key"`
	}{Key: key}, &out); err != nil {
		return app.KeyState{}, err
	}
	return app.KeyState(out), nil
}

// ClearKey removes a key.
func (c *Client) ClearKey(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, providerPath(id)+"/key", nil, nil)
}

// --- budget ---

type defence struct {
	Enabled         bool  `json:"enabled"`
	SharePct        int   `json:"share_pct"`
	ReservedMicros  int64 `json:"reserved_micros"`
	RemainingMicros int64 `json:"remaining_micros"`
}

type budget struct {
	Currency        string    `json:"currency"`
	TotalMicros     int64     `json:"total_micros"`
	SpentMicros     int64     `json:"spent_micros"`
	ReservedMicros  int64     `json:"reserved_micros"`
	RemainingMicros int64     `json:"remaining_micros"`
	Exhausted       bool      `json:"exhausted"`
	Period          string    `json:"period"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	Defence         defence   `json:"defence"`
}

func (b budget) app() app.Budget {
	return app.Budget{
		Currency: b.Currency, TotalMicros: b.TotalMicros, SpentMicros: b.SpentMicros,
		ReservedMicros: b.ReservedMicros, RemainingMicros: b.RemainingMicros, Exhausted: b.Exhausted,
		Period: domain.Period(b.Period), PeriodStart: b.PeriodStart, PeriodEnd: b.PeriodEnd,
		Defence: app.Defence(b.Defence),
	}
}

// Budget returns the budget.
func (c *Client) Budget(ctx context.Context) (app.Budget, error) {
	var out budget
	if err := c.call(ctx, http.MethodGet, "/v1/gateway/budget", nil, &out); err != nil {
		return app.Budget{}, err
	}
	return out.app(), nil
}

// SetBudget changes the total or the period.
func (c *Client) SetBudget(ctx context.Context, ch app.BudgetChange) (app.Budget, error) {
	in := struct {
		TotalMicros *int64  `json:"total_micros,omitempty"`
		Period      *string `json:"period,omitempty"`
	}{TotalMicros: ch.TotalMicros}
	if ch.Period != nil {
		p := string(*ch.Period)
		in.Period = &p
	}
	var out budget
	if err := c.call(ctx, http.MethodPatch, "/v1/gateway/budget", in, &out); err != nil {
		return app.Budget{}, err
	}
	return out.app(), nil
}

// SetDefence changes the defence share.
func (c *Client) SetDefence(ctx context.Context, ch app.DefenceChange) (app.Budget, error) {
	in := struct {
		Enabled  *bool `json:"enabled,omitempty"`
		SharePct *int  `json:"share_pct,omitempty"`
	}{Enabled: ch.Enabled, SharePct: ch.SharePct}
	var out budget
	if err := c.call(ctx, http.MethodPatch, "/v1/gateway/budget/defence", in, &out); err != nil {
		return app.Budget{}, err
	}
	return out.app(), nil
}

// --- freeze all ---

type freezeState struct {
	Frozen bool       `json:"frozen"`
	Since  *time.Time `json:"since,omitempty"`
}

// Status returns the freeze-all switch.
func (c *Client) Status(ctx context.Context) (app.FreezeState, error) {
	var out freezeState
	if err := c.call(ctx, http.MethodGet, "/v1/freeze", nil, &out); err != nil {
		return app.FreezeState{}, err
	}
	s := app.FreezeState{Frozen: out.Frozen}
	if out.Since != nil {
		s.Since = *out.Since
	}
	return s, nil
}

// Freeze turns the switch on.
func (c *Client) Freeze(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/v1/freeze", nil, nil)
}

// Release turns the switch off.
func (c *Client) Release(ctx context.Context) error {
	return c.call(ctx, http.MethodDelete, "/v1/freeze", nil, nil)
}
