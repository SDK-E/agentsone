// SPDX-License-Identifier: MIT

// Package agentstest holds test builders and the shared repository contract
// suite for the society context.
package agentstest

import (
	"time"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// Option changes a built agent.
type Option func(*domain.Agent)

// New returns a valid sleeping custom agent named name, below nobody. Use
// Below to place it in a chart and Root to make it a root agent.
func New(name string, opts ...Option) domain.Agent {
	a := domain.Agent{
		Name:        name,
		DisplayName: name,
		Kind:        domain.KindAlwaysOn,
		Runner:      "echo",
		Role:        "Worker",
		State:       domain.StateSleeping,
		Limits:      &domain.Limits{CPUPercent: 100, MemoryMB: 512, Tasks: 64},
		UID:         1100,
		Home:        "/home/agents/" + name,
		CreatedAt:   coretest.Epoch,
		CreatedBy:   "system:daemon",
	}
	for _, o := range opts {
		o(&a)
	}
	return a
}

// Root makes the agent a root agent: uid 0, no limits.
func Root() Option {
	return func(a *domain.Agent) {
		a.IsRoot, a.UID, a.Limits, a.Home = true, 0, nil, "/root/agents/"+a.Name
	}
}

// Below places the agent directly below manager.
func Below(manager string) Option { return func(a *domain.Agent) { a.Manager = manager } }

// WithKind sets the agent kind.
func WithKind(k domain.Kind) Option { return func(a *domain.Agent) { a.Kind = k } }

// WithState sets the agent state.
func WithState(s domain.State) Option { return func(a *domain.Agent) { a.State = s } }

// Society is a small chart used across tests:
//
//	founder (root)
//	├── lead
//	│   └── dev
//	│       └── intern
//	└── ops
//	    └── watcher (waker)
func Society() []domain.Agent {
	return []domain.Agent{
		New("founder", Root()),
		New("lead", Below("founder")),
		New("dev", Below("lead")),
		New("intern", Below("dev")),
		New("ops", Below("founder")),
		New("watcher", Below("ops"), WithKind(domain.KindWaker)),
	}
}

// Chart returns the Society chart.
func Chart() domain.Chart { return domain.NewChart(Society()) }

// At is a fixed time for tests.
var At = time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
