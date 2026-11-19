// SPDX-License-Identifier: MIT

// Package authtest holds hand-written fakes for the identity context's
// ports and a builder for a service wired to them.
package authtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/auth/adapters/memory"
	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// Accounts is a fake Linux user database that also checks passwords.
type Accounts struct {
	mu        sync.Mutex
	passwords map[string]string
	nextUID   int
	// Fail makes every change fail with this error when set.
	Fail error
}

var (
	_ app.Accounts      = (*Accounts)(nil)
	_ app.Authenticator = (*Accounts)(nil)
)

// NewAccounts returns an empty user database whose first uid is 1000.
func NewAccounts() *Accounts { return &Accounts{passwords: map[string]string{}, nextUID: 1000} }

// CreateUser adds a user.
func (a *Accounts) CreateUser(_ context.Context, name, password string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Fail != nil {
		return 0, a.Fail
	}
	if _, ok := a.passwords[name]; ok {
		return 0, fmt.Errorf("user %s: %w", name, core.ErrConflict)
	}
	a.passwords[name] = password
	a.nextUID++
	return a.nextUID - 1, nil
}

// SetPassword changes a password.
func (a *Accounts) SetPassword(_ context.Context, name, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Fail != nil {
		return a.Fail
	}
	a.passwords[name] = password
	return nil
}

// DeleteUser removes a user.
func (a *Accounts) DeleteUser(_ context.Context, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Fail != nil {
		return a.Fail
	}
	delete(a.passwords, name)
	return nil
}

// HasUser reports whether the Linux user exists.
func (a *Accounts) HasUser(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.passwords[name]
	return ok
}

// Authenticate checks the password.
func (a *Accounts) Authenticate(_ context.Context, name, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.passwords[name]; !ok || p != password {
		return fmt.Errorf("pam %s: %w", name, core.ErrUnauthorized)
	}
	return nil
}

// Licence is a fixed engine licence.
type Licence struct{ Engine, Version, Text string }

// EngineLicence returns the licence, or ErrUnavailable when Version is empty.
func (l Licence) EngineLicence(context.Context) (string, string, string, error) {
	if l.Version == "" {
		return "", "", "", core.ErrUnavailable
	}
	return l.Engine, l.Version, l.Text, nil
}

// Counter is a deterministic random source: every byte read is the next
// value of a counter, so each token and session differs.
type Counter struct {
	mu sync.Mutex
	n  byte
}

// Read fills p with counter bytes.
func (c *Counter) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range p {
		c.n++
		p[i] = c.n
	}
	return len(p), nil
}

// Env is a service with its fakes.
type Env struct {
	Service  *app.Service
	Clock    *coretest.Clock
	Store    *memory.Store
	Accounts *Accounts
	Events   *coretest.Events
	Audit    *coretest.Audit
}

// DefaultLicence is the licence every Env serves.
var DefaultLicence = Licence{Engine: "claude-code", Version: "2026-11", Text: "Licence text."}

// New returns a service on in-memory stores and fakes.
func New() *Env {
	clock := coretest.NewClock(coretest.Epoch)
	store := memory.New()
	accounts := NewAccounts()
	events := coretest.NewEvents(clock, coretest.NewIDs())
	audit := coretest.NewAudit()
	svc := app.New(app.Deps{
		Clock: clock, Rand: &Counter{}, Sessions: store, Tokens: store, Humans: store, Setup: store,
		Auth: accounts, Accounts: accounts, Licence: DefaultLicence, Events: events, Audit: audit,
	})
	return &Env{Service: svc, Clock: clock, Store: store, Accounts: accounts, Events: events, Audit: audit}
}

// Human returns a signed-in human caller.
func Human(name string) core.Caller { return core.Caller{Kind: core.CallerHuman, Name: name, UID: 1000} }

// Agent returns a root agent caller.
func Agent(name string) core.Caller {
	return core.Caller{Kind: core.CallerAgent, Name: name, UID: 0, IsRoot: true}
}
