// SPDX-License-Identifier: MIT

// Package authtest holds hand-written fakes for the identity context's
// ports, a builder for a service wired to them, and the repository
// contract suite.
package authtest

import (
	"context"
	"sync"

	"github.com/SDK-E/agentsone/internal/auth/adapters/memory"
	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// Cheap are argon2id costs that keep tests fast. Production uses
// domain.DefaultArgon2.
var Cheap = domain.Argon2Params{Time: 1, Memory: 64, Threads: 1}

// Password is a valid password for test accounts.
const Password = "correct horse battery"

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
	Service *app.Service
	Clock   *coretest.Clock
	Store   *memory.Store
	Events  *coretest.Events
	Audit   *coretest.Audit
	Seed    *coretest.Seed
}

// DefaultLicence is the licence every Env serves.
var DefaultLicence = Licence{Engine: "claude-code", Version: "2026-11", Text: "Licence text."}

// New returns a service on in-memory stores and fakes.
func New() *Env {
	clock := coretest.NewClock(coretest.Epoch)
	store := memory.New()
	events := coretest.NewEvents(clock, coretest.NewIDs())
	audit := coretest.NewAudit()
	seed := coretest.NewSeed()
	svc, err := app.New(app.Deps{
		Clock: clock, Rand: &Counter{}, Argon2: Cheap, Sessions: store, Tokens: store, Humans: store, Setup: store,
		Licence: DefaultLicence, Events: events, Audit: audit, Seed: seed,
	})
	if err != nil {
		panic(err) // the counter never fails; a failure here is a bug in the test kit
	}
	return &Env{Service: svc, Clock: clock, Store: store, Events: events, Audit: audit, Seed: seed}
}

// Human returns a signed-in human caller.
func Human(name string) core.Caller { return core.Caller{Kind: core.CallerHuman, Name: name, UID: -1} }

// Agent returns a root agent caller.
func Agent(name string) core.Caller {
	return core.Caller{Kind: core.CallerAgent, Name: name, UID: 0, IsRoot: true}
}
