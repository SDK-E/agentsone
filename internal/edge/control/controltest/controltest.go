// SPDX-License-Identifier: MIT

// Package controltest holds in-memory fakes of the control page's ports, for tests and for the
// local preview of the page.
package controltest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/app"
)

// Clock is a clock moved by hand.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock at now.
func NewClock(now time.Time) *Clock { return &Clock{now: now} }

// Now tells the time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock on by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Hasher is a fast, fake hasher: never use it outside tests.
type Hasher struct{}

// Hash returns a recognisable fake hash.
func (Hasher) Hash(pass string) (string, error) { return "fake$" + pass, nil }

// Verify checks a fake hash.
func (Hasher) Verify(hash, pass string) (bool, error) { return hash == "fake$"+pass, nil }

// Credentials is the edge's credential store in memory.
type Credentials struct {
	mu   sync.Mutex
	hash string
	// Err, when set, is returned by every call: the edge is down.
	Err error
}

// Get returns the hash or app.ErrNoCredential.
func (c *Credentials) Get(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return "", c.Err
	}
	if c.hash == "" {
		return "", app.ErrNoCredential
	}
	return c.hash, nil
}

// Create stores the first hash.
func (c *Credentials) Create(_ context.Context, hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return c.Err
	}
	if c.hash != "" {
		return app.ErrCredentialSet
	}
	c.hash = hash
	return nil
}

// Reset clears the hash, as the reset command does.
func (c *Credentials) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hash = ""
}

// Hash is the stored hash.
func (c *Credentials) Hash() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hash
}

// Edge is the edge's powers in memory: allow list, providers and keys, budget and freeze.
type Edge struct {
	mu        sync.Mutex
	allow     []app.AllowEntry
	providers []app.Provider
	keys      map[string]string
	budget    app.Budget
	freeze    app.FreezeState
	now       func() time.Time
	// Err, when set, is returned by every call: the edge is down.
	Err error
}

// NewEdge returns an edge with the given providers and an empty allow list.
func NewEdge(now func() time.Time, providers ...app.Provider) *Edge {
	return &Edge{
		providers: providers, keys: map[string]string{}, now: now,
		budget: app.Budget{Currency: "USD", Period: "month"},
	}
}

func refuse(format string) error { return &app.Refusal{Detail: format} }

// AllowList returns the entries.
func (e *Edge) AllowList(context.Context) ([]app.AllowEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return nil, e.Err
	}
	return slices.Clone(e.allow), nil
}

// Allow adds an entry. A pattern with a space or no dot is refused, like the edge does.
func (e *Edge) Allow(_ context.Context, pattern, why string) (app.AllowEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return app.AllowEntry{}, e.Err
	}
	if strings.ContainsAny(pattern, " /") || !strings.Contains(pattern, ".") {
		return app.AllowEntry{}, refuse("This is not a name the edge can allow.")
	}
	if slices.ContainsFunc(e.allow, func(a app.AllowEntry) bool { return a.Pattern == pattern }) {
		return app.AllowEntry{}, refuse("This name is already on the list.")
	}
	entry := app.AllowEntry{Pattern: pattern, Why: why}
	e.allow = append(e.allow, entry)
	return entry, nil
}

// Disallow removes an entry.
func (e *Edge) Disallow(_ context.Context, pattern string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return e.Err
	}
	i := slices.IndexFunc(e.allow, func(a app.AllowEntry) bool { return a.Pattern == pattern })
	if i < 0 {
		return refuse("This name is not on the list.")
	}
	e.allow = slices.Delete(e.allow, i, i+1)
	return nil
}

// Providers returns the providers with their key state.
func (e *Edge) Providers(context.Context) ([]app.Provider, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return nil, e.Err
	}
	return slices.Clone(e.providers), nil
}

func (e *Edge) provider(id string) (*app.Provider, error) {
	i := slices.IndexFunc(e.providers, func(p app.Provider) bool { return p.ID == id })
	if i < 0 {
		return nil, refuse("The gateway does not know this provider.")
	}
	return &e.providers[i], nil
}

func (e *Edge) setEnabled(id string, on bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return e.Err
	}
	p, err := e.provider(id)
	if err != nil {
		return err
	}
	p.Enabled = on
	return nil
}

// Enable turns a provider on.
func (e *Edge) Enable(_ context.Context, id string) error { return e.setEnabled(id, true) }

// Disable turns a provider off.
func (e *Edge) Disable(_ context.Context, id string) error { return e.setEnabled(id, false) }

// Fingerprint is the first 8 hex digits of the key's SHA-256, as the edge shows it.
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

// SetKey stores a key.
func (e *Edge) SetKey(_ context.Context, id, key string) (app.KeyState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return app.KeyState{}, e.Err
	}
	p, err := e.provider(id)
	if err != nil {
		return app.KeyState{}, err
	}
	e.keys[id] = key
	p.Key = app.KeyState{Set: true, Fingerprint: Fingerprint(key)}
	return p.Key, nil
}

// ClearKey removes a key.
func (e *Edge) ClearKey(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return e.Err
	}
	p, err := e.provider(id)
	if err != nil {
		return err
	}
	delete(e.keys, id)
	p.Key = app.KeyState{}
	return nil
}

// Key is the stored key, for tests that prove the key reached the edge.
func (e *Edge) Key(id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.keys[id]
}

// Budget returns the budget.
func (e *Edge) Budget(context.Context) (app.Budget, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return app.Budget{}, e.Err
	}
	return e.budget, nil
}

// SeedBudget replaces the budget.
func (e *Edge) SeedBudget(b app.Budget) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.budget = b
}

// SetBudget changes the total or period.
func (e *Edge) SetBudget(_ context.Context, c app.BudgetChange) (app.Budget, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return app.Budget{}, e.Err
	}
	if c.TotalMicros != nil {
		e.budget.TotalMicros = *c.TotalMicros
		e.budget.RemainingMicros = *c.TotalMicros - e.budget.SpentMicros
	}
	if c.Period != nil {
		e.budget.Period = *c.Period
	}
	return e.budget, nil
}

// SetDefence changes the defence share.
func (e *Edge) SetDefence(_ context.Context, c app.DefenceChange) (app.Budget, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return app.Budget{}, e.Err
	}
	if c.Enabled != nil {
		e.budget.Defence.Enabled = *c.Enabled
	}
	if c.SharePct != nil {
		e.budget.Defence.SharePct = *c.SharePct
	}
	return e.budget, nil
}

// Status returns the freeze switch.
func (e *Edge) Status(context.Context) (app.FreezeState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return app.FreezeState{}, e.Err
	}
	return e.freeze, nil
}

func (e *Edge) setFrozen(on bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return e.Err
	}
	if e.freeze.Frozen != on {
		e.freeze = app.FreezeState{Frozen: on, Since: e.now()}
	}
	return nil
}

// Freeze turns the switch on.
func (e *Edge) Freeze(context.Context) error { return e.setFrozen(true) }

// Release turns the switch off.
func (e *Edge) Release(context.Context) error { return e.setFrozen(false) }

// ErrDown is a stand-in for an edge that does not answer.
var ErrDown = errors.Join(app.ErrEdgeUnavailable, errors.New("dial unix: no such file"))
