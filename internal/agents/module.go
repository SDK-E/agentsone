// SPDX-License-Identifier: MIT

package agents

import (
	"context"
	"encoding/json"
	"fmt"

	httpadapter "github.com/SDK-E/agentsone/internal/agents/adapters/http"
	"github.com/SDK-E/agentsone/internal/agents/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// Module wires the society context into the daemon.
type Module struct {
	core.Base
	db      *store.DB
	rt      app.Runtime
	ports   *core.Ports
	repo    domain.Repository
	society *app.Society
}

var _ core.Module = (*Module)(nil)

// Option configures the module.
type Option func(*Module)

// WithRuntime sets the runner supervisor. Without one, hiring and waking
// answer core.ErrUnavailable.
func WithRuntime(rt app.Runtime) Option { return func(m *Module) { m.rt = rt } }

// New builds the agents module on deps.Stores.Core.
func New(deps core.Deps, opts ...Option) *Module {
	m := &Module{db: deps.Stores.Core, rt: noRuntime{}, ports: deps.Ports}
	for _, o := range opts {
		o(m)
	}
	m.repo = sqlite.New(m.db)
	m.society = app.New(m.repo, m.rt, deps.Ports, deps.Clock, deps.IDs)
	return m
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "agents" }

// Society returns the use cases, for the daemon to hand to other parts (the
// supervisor's output sink, the host door's budget push).
func (m *Module) Society() *app.Society { return m.society }

// Provide sets the Agents, Waker, Budget and Freezer ports.
func (m *Module) Provide(p *core.Ports) {
	p.Agents = m.society.Directory()
	p.Waker = m.society.Waker()
	p.Budget = m.society.Budget()
	p.Freezer = m.society.Freezer()
}

// Migrate creates the society tables.
func (m *Module) Migrate(ctx context.Context) error { return sqlite.Migrate(ctx, m.db) }

// Start brings the machine in line with core.db before any agent wakes: the
// container's root file system resets on restart, so every active agent's
// Linux user is created again with its stored uid, and active freezes are
// replayed. Without a supervisor there is nothing to bring in line.
func (m *Module) Start(ctx context.Context) error {
	if _, ok := m.rt.(noRuntime); ok {
		return nil
	}
	all, err := m.repo.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("reconcile agents: %w", err)
	}
	for _, a := range all {
		if !a.Active() {
			continue
		}
		if _, err := m.rt.Provision(ctx, a.Core(), a.Limits); err != nil {
			return fmt.Errorf("reconcile %s: %w", a.Name, err)
		}
	}
	scopes, err := m.society.Freezer().State(ctx)
	if err != nil {
		return fmt.Errorf("reconcile freezes: %w", err)
	}
	for _, s := range scopes {
		if err := m.rt.Freeze(ctx, s); err != nil {
			return fmt.Errorf("replay freeze %s: %w", s, err)
		}
	}
	return nil
}

// API serves the agents, chart, charter and budget tags.
func (m *Module) API() core.API {
	return core.API{Groups: httpadapter.New(m.society, liveList{m.ports}).Groups()}
}

// liveList reads the router's live list when used, since the router fills
// its port after this module is built.
type liveList struct{ ports *core.Ports }

func (l liveList) UpdateLine(ctx context.Context, agent, line string) error {
	return l.ports.Live.UpdateLine(ctx, agent, line)
}

func (l liveList) Lines(ctx context.Context) ([]core.LiveLine, error) {
	return l.ports.Live.Lines(ctx)
}

var errNoRuntime = fmt.Errorf("runner supervisor not wired: %w", core.ErrUnavailable)

// noRuntime stands in until the daemon wires the supervisor.
type noRuntime struct{}

func (noRuntime) Provision(context.Context, core.Agent, *domain.Limits) (int, error) {
	return 0, errNoRuntime
}
func (noRuntime) Reconfigure(context.Context, core.Agent, *domain.Limits) error { return errNoRuntime }
func (noRuntime) Retire(context.Context, string) error                          { return errNoRuntime }
func (noRuntime) Wake(context.Context, string, core.WakeReason, json.RawMessage) error {
	return errNoRuntime
}
func (noRuntime) Sleep(context.Context, string, bool) error       { return errNoRuntime }
func (noRuntime) Freeze(context.Context, core.FreezeScope) error  { return errNoRuntime }
func (noRuntime) Release(context.Context, core.FreezeScope) error { return errNoRuntime }
