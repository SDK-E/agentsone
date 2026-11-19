// SPDX-License-Identifier: MIT

package coretest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// Bus is an in-memory core.BusSender.
type Bus struct {
	mu    sync.Mutex
	clock core.Clock
	ids   core.IDs
	sent  []core.BusMessage
}

var _ core.BusSender = (*Bus)(nil)

// NewBus returns a bus that stamps messages with clock and ids.
func NewBus(clock core.Clock, ids core.IDs) *Bus { return &Bus{clock: clock, ids: ids} }

// Send stores a valid message with a "msg" ID and the send time.
func (b *Bus) Send(_ context.Context, m core.BusMessage) (core.BusMessage, error) {
	if err := m.Validate(); err != nil {
		return core.BusMessage{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	m.ID = b.ids.New("msg")
	m.Sent = b.clock.Now()
	b.sent = append(b.sent, m)
	return m, nil
}

// Sent returns sent messages in order.
func (b *Bus) Sent() []core.BusMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.sent)
}

// Freezer is an in-memory core.Freezer.
type Freezer struct {
	mu     sync.Mutex
	active []core.FreezeScope
}

var _ core.Freezer = (*Freezer)(nil)

// NewFreezer returns a freezer with nothing frozen.
func NewFreezer() *Freezer { return &Freezer{} }

// Freeze activates scope; freezing an active scope again changes nothing.
func (f *Freezer) Freeze(_ context.Context, scope core.FreezeScope) error {
	if _, err := core.ParseFreezeScope(scope.String()); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Contains(f.active, scope) {
		f.active = append(f.active, scope)
	}
	return nil
}

// Release ends an active freeze.
func (f *Freezer) Release(_ context.Context, scope core.FreezeScope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.Index(f.active, scope)
	if i < 0 {
		return fmt.Errorf("freeze %s is not active: %w", scope, core.ErrNotFound)
	}
	f.active = slices.Delete(f.active, i, i+1)
	return nil
}

// State returns active freezes sorted by their text form.
func (f *Freezer) State(context.Context) ([]core.FreezeScope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := slices.Clone(f.active)
	sort.Slice(state, func(i, j int) bool { return state[i].String() < state[j].String() })
	return state, nil
}

// Tokens is an in-memory core.TokenIssuer that expires tokens by its clock.
type Tokens struct {
	mu     sync.Mutex
	clock  core.Clock
	ids    core.IDs
	issued map[string]issuedToken
}

type issuedToken struct {
	caller  core.Caller
	expires time.Time
}

var _ core.TokenIssuer = (*Tokens)(nil)

// NewTokens returns an issuer that stamps tokens with clock and ids.
func NewTokens(clock core.Clock, ids core.IDs) *Tokens {
	return &Tokens{clock: clock, ids: ids, issued: map[string]issuedToken{}}
}

// Issue makes a token for subject holding scopes until ttl passes.
func (t *Tokens) Issue(_ context.Context, subject core.Caller, scopes []string, ttl time.Duration) (core.Token, error) {
	if ttl <= 0 {
		return core.Token{}, fmt.Errorf("token lifetime %s must be positive: %w", ttl, core.ErrInvalid)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	subject.Scopes = slices.Clone(scopes)
	tok := core.Token{Value: t.ids.New("tok"), Expires: t.clock.Now().Add(ttl)}
	t.issued[tok.Value] = issuedToken{caller: subject, expires: tok.Expires}
	return tok, nil
}

// Verify returns the token's caller while it is known and unexpired.
func (t *Tokens) Verify(_ context.Context, value string) (core.Caller, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tok, ok := t.issued[value]
	if !ok || !t.clock.Now().Before(tok.expires) {
		return core.Caller{}, fmt.Errorf("token: %w", core.ErrUnauthorized)
	}
	return tok.caller, nil
}

// Templates is an in-memory core.TemplateCatalog.
type Templates struct {
	mu   sync.Mutex
	byID map[string]core.AgentTemplate
}

var _ core.TemplateCatalog = (*Templates)(nil)

// NewTemplates returns an empty catalog.
func NewTemplates() *Templates { return &Templates{byID: map[string]core.AgentTemplate{}} }

// Add stores tpl, replacing any template with the same ID.
func (c *Templates) Add(tpl core.AgentTemplate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byID[tpl.ID] = tpl
}

// List returns templates sorted by ID.
func (c *Templates) List(context.Context) ([]core.AgentTemplate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := make([]core.AgentTemplate, 0, len(c.byID))
	for _, tpl := range c.byID {
		list = append(list, tpl)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// Get returns the template with id.
func (c *Templates) Get(_ context.Context, id string) (core.AgentTemplate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tpl, ok := c.byID[id]
	if !ok {
		return core.AgentTemplate{}, fmt.Errorf("agent template %q: %w", id, core.ErrNotFound)
	}
	return tpl, nil
}
