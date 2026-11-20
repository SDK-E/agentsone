// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
)

// Runtime is the runner supervisor (internal/agents/supervisor): it turns
// society decisions into Linux users, systemd units and runner turns.
type Runtime interface {
	// Provision creates the agent's user and group, home and unit, with
	// exactly a.UID as uid and gid. The society hands out uids, so the
	// returned uid is only an echo. It is idempotent: it runs again for every
	// agent at start, since the container's root file system resets. Limits
	// is nil for root agents.
	Provision(ctx context.Context, a core.Agent, limits *domain.Limits) (int, error)
	// Reconfigure applies a kind, runner or limits change and restarts the unit.
	Reconfigure(ctx context.Context, a core.Agent, limits *domain.Limits) error
	// Retire stops the runner for good and keeps the home and memory.
	Retire(ctx context.Context, name string) error
	// Wake starts a turn. It returns core.ErrFrozen for a frozen agent.
	Wake(ctx context.Context, name string, reason core.WakeReason, payload json.RawMessage) error
	// Sleep ends the turn, optionally compacting the session first.
	Sleep(ctx context.Context, name string, compact bool) error
	// Freeze cancels the turns in scope and puts their runners to sleep
	// until Release.
	Freeze(ctx context.Context, scope core.FreezeScope) error
	Release(ctx context.Context, scope core.FreezeScope) error
	// Run starts an always-on agent's runner with no turn, so it waits idle
	// and makes no model call until a wake. A running runner or a frozen
	// agent is left alone.
	Run(ctx context.Context, name string) error
}

// Society runs the society use cases. Changes are serialised, so every rule
// is checked against the chart it changes.
type Society struct {
	repo  domain.Repository
	rt    Runtime
	ports *core.Ports
	clock core.Clock
	ids   core.IDs
	hub   *Hub
	log   *slog.Logger

	mu sync.Mutex
	// replies holds agent replies being written, by turn id.
	replies map[string]*reply
	// failed holds why each agent's runner cannot run, by agent name.
	failed map[string]string
}

// New returns the society over repo. Ports is read when used, so modules
// may fill it after New.
func New(repo domain.Repository, rt Runtime, ports *core.Ports, clock core.Clock, ids core.IDs) *Society {
	return &Society{
		repo: repo, rt: rt, ports: ports, clock: clock, ids: ids, hub: NewHub(), log: slog.New(slog.DiscardHandler),
		replies: map[string]*reply{}, failed: map[string]string{},
	}
}

// Hub returns the society's stream hub.
func (s *Society) Hub() *Hub { return s.hub }

var errHumansReadOnly = errors.New("humans read the society; hiring, editing and retiring are for agents")

// actor returns the calling agent. Humans may read the society and chat,
// but the society itself is changed by agents only.
func actor(ctx context.Context) (string, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok {
		return "", fmt.Errorf("no caller: %w", core.ErrUnauthorized)
	}
	if c.Kind != core.CallerAgent {
		return "", fmt.Errorf("%w: %w", core.ErrForbidden, errHumansReadOnly)
	}
	return c.Name, nil
}

func (s *Society) chart(ctx context.Context) (domain.Chart, error) {
	all, err := s.repo.ListAgents(ctx)
	if err != nil {
		return domain.Chart{}, fmt.Errorf("load chart: %w", err)
	}
	return domain.NewChart(all), nil
}

// record audits and publishes one society fact. The audit record comes
// first: it is the testimony; the event is the notification.
func (s *Society) record(ctx context.Context, actorRef, action, target string, detail any, ev event) error {
	if err := s.audit(ctx, actorRef, action, target, detail); err != nil {
		return err
	}
	return s.publish(ctx, actorRef, ev)
}

func (s *Society) audit(ctx context.Context, actorRef, action, target string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode %s detail: %w", action, err)
	}
	if err := s.ports.Audit.Append(ctx, core.AuditEntry{Actor: actorRef, Action: action, Target: target, Detail: raw}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// event is the event of one society fact. Its data is a generated contract
// type, so the payload is the catalog's.
type event struct {
	source, scope string
	data          eventdata.Data
}

// emitter is who announces society facts on the bus: the society itself,
// the daemon's "agents" emitter. Built-in agent events state facts only the
// society can vouch for, so the events module takes them from no agent
// directly, and an agent reached by token is never root. It stays in this
// package: no port, API operation or MCP tool hands it out.
var emitter = core.Caller{Kind: core.CallerSystem, Name: "agents", UID: -1}

// publish announces a fact the society has already checked and stored. It is
// called only after the change's own chart rules passed, so it publishes as
// the society's emitter and names the agent that acted as the actor.
func (s *Society) publish(ctx context.Context, actorRef string, ev event) error {
	_, err := core.PublishData(core.WithCaller(ctx, emitter), s.ports.Events, core.Publication{
		Source: ev.source, Actor: actorRef, ChartScope: ev.scope, Data: ev.data,
	})
	return err //nolint:wrapcheck // PublishData names the event type
}

// Found creates the founder of an empty society. Only the daemon or the
// host (for example the seed plugin at first boot) may found it.
func (s *Society) Found(ctx context.Context, spec domain.HireSpec) (domain.Agent, error) {
	c, _ := core.CallerFrom(ctx)
	if c.Kind != core.CallerSystem && c.Kind != core.CallerHost {
		return domain.Agent{}, fmt.Errorf("found the society: %w", core.ErrForbidden)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	by := string(c.Kind) + ":" + c.Name
	spec, seed, err := s.applyTemplate(ctx, spec)
	if err != nil {
		return domain.Agent{}, err
	}
	founder, err := chart.Found(spec, by, s.clock.Now())
	if err != nil {
		return domain.Agent{}, err
	}
	data := eventdata.AgentsHired{Agent: founder.Name, Manager: founder.Name, Kind: eventdata.AgentsHiredKind(founder.Kind), IsRoot: true, Template: founder.Template}
	hired, err := s.outbound(by, event{"/daemon", "team:" + founder.Name, data})
	if err != nil {
		return domain.Agent{}, err
	}
	if err := s.provisionAndHire(ctx, &founder, hired); err != nil {
		return domain.Agent{}, err
	}
	// Committed: the founder is on the record before anything else can fail.
	defer s.flushAfterChange(ctx)
	if err := s.audit(ctx, by, "agent.found", "agent:"+founder.Name, data); err != nil {
		return domain.Agent{}, err
	}
	if err := s.seedFromTemplate(ctx, founder.Name, domain.NewChart([]domain.Agent{founder}), founder, seed); err != nil {
		return domain.Agent{}, err
	}
	return founder, nil
}

// Hire hires an agent below the calling agent. An always-on agent's runner
// starts at once, idle.
func (s *Society) Hire(ctx context.Context, spec domain.HireSpec) (domain.Agent, error) {
	name, err := actor(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	a, err := s.hire(ctx, name, "agent:"+name, spec)
	if err != nil {
		return domain.Agent{}, err
	}
	if a.Kind == domain.KindAlwaysOn {
		// A runner that cannot start does not undo the hire: the
		// supervisor records it in the audit trail and puts the reason on
		// the agent's status.
		_ = s.rt.Run(ctx, a.Name)
	}
	return a, nil
}

// hire hires below hirer. by is who acts, as audited: the hirer itself, or
// the daemon seeding the society on the hirer's behalf.
func (s *Society) hire(ctx context.Context, hirer, by string, spec domain.HireSpec) (domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	spec, seed, err := s.applyTemplate(ctx, spec)
	if err != nil {
		return domain.Agent{}, err
	}
	a, err := chart.Hire(hirer, spec, s.clock.Now())
	if err != nil {
		return domain.Agent{}, err
	}
	after := domain.NewChart(append(chart.Agents(), a))
	source := "/agents/" + hirer
	if by != "agent:"+hirer {
		source = "/daemon"
	}
	data := eventdata.AgentsHired{Agent: a.Name, Manager: a.Manager, Kind: eventdata.AgentsHiredKind(a.Kind), IsRoot: a.IsRoot, Template: a.Template}
	hired, err := s.outbound(by, event{source, "team:" + after.Team(a.Name), data})
	if err != nil {
		return domain.Agent{}, err
	}
	if err := s.provisionAndHire(ctx, &a, hired); err != nil {
		return domain.Agent{}, err
	}
	// Committed: the hire is on the record before anything else can fail.
	defer s.flushAfterChange(ctx)
	if err := s.audit(ctx, by, "agent.hire", "agent:"+a.Name, data); err != nil {
		return domain.Agent{}, err
	}
	if err := s.seedFromTemplate(ctx, hirer, after, a, seed); err != nil {
		return domain.Agent{}, err
	}
	return a, nil
}

// provisionAndHire gives a its Linux user and unit, then stores it with
// the event that announces it, in one transaction. When the store fails the
// provisioning is undone, so a refused or failed hire leaves nothing.
func (s *Society) provisionAndHire(ctx context.Context, a *domain.Agent, hired domain.Outbound) error {
	if _, err := s.rt.Provision(ctx, a.Core(), a.Limits); err != nil {
		return fmt.Errorf("provision %s: %w", a.Name, err)
	}
	if err := s.repo.HireAgent(ctx, *a, hired); err != nil {
		if undo := s.rt.Retire(ctx, a.Name); undo != nil {
			return fmt.Errorf("store %s: %w (undo provisioning: %w)", a.Name, err, undo)
		}
		return fmt.Errorf("store %s: %w", a.Name, err)
	}
	return nil
}

// Update edits an agent below the calling agent. Kind, runner and limits
// changes restart its unit.
func (s *Society) Update(ctx context.Context, target string, u domain.Update) (domain.Agent, error) {
	name, err := actor(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	if err := chart.CanManage(name, target); err != nil {
		return domain.Agent{}, err
	}
	fields := editedFields(u)
	if len(fields) == 0 {
		return domain.Agent{}, fmt.Errorf("%w: the edit changes no field", domain.ErrInvalidAgent)
	}
	current, _ := chart.Get(target)
	next, err := u.Apply(current)
	if err != nil {
		return domain.Agent{}, err
	}
	if u.Restarts() {
		if err := s.rt.Reconfigure(ctx, next.Core(), next.Limits); err != nil {
			return domain.Agent{}, fmt.Errorf("reconfigure %s: %w", target, err)
		}
	}
	if err := s.repo.SaveAgents(ctx, next); err != nil {
		return domain.Agent{}, fmt.Errorf("save %s: %w", target, err)
	}
	data := eventdata.AgentsEdited{Agent: target, Fields: fields}
	return next, s.record(ctx, "agent:"+name, "agent.edit", "agent:"+target, data,
		event{"/agents/" + name, "agent:" + target, data})
}

// editedFields names the changed fields in the event catalog's words. The
// catalog has no word for display name, role or description, so a wording
// edit is reported as the agent's profile line ("one_line").
func editedFields(u domain.Update) []eventdata.AgentsEditedFieldsElem {
	var fields []eventdata.AgentsEditedFieldsElem
	if u.Limits != nil {
		fields = append(fields, eventdata.AgentsEditedFieldsElemLimits)
	}
	if u.Runner != nil || u.Kind != nil {
		fields = append(fields, eventdata.AgentsEditedFieldsElemRunner)
	}
	if u.DisplayName != nil || u.Role != nil || u.Description != nil {
		fields = append(fields, eventdata.AgentsEditedFieldsElemOneLine)
	}
	return fields
}

// Retire retires an agent below the calling agent: its runner stops for
// good, its audit and memory stay, and its direct reports move up to the
// caller.
func (s *Society) Retire(ctx context.Context, target string) error {
	name, err := actor(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return err
	}
	plan, err := chart.Retire(name, target)
	if err != nil {
		return err
	}
	team := chart.Team(target)
	if err := s.rt.Retire(ctx, target); err != nil {
		return fmt.Errorf("stop %s: %w", target, err)
	}
	retired, _ := chart.Get(target)
	retired.State = domain.StateRetired
	changed := []domain.Agent{retired}
	for _, m := range plan.Moved {
		report, _ := chart.Get(m)
		report.Manager = plan.NewManager
		changed = append(changed, report)
	}
	if err := s.repo.SaveAgents(ctx, changed...); err != nil {
		return fmt.Errorf("save retirement of %s: %w", target, err)
	}
	detail := map[string]any{"agent": target, "by": name, "moved": plan.Moved}
	return s.record(ctx, "agent:"+name, "agent.retire", "agent:"+target, detail,
		event{"/agents/" + name, "team:" + team, eventdata.AgentsRetired{Agent: target, By: name}})
}

// Move moves an agent and its branch below a new manager.
func (s *Society) Move(ctx context.Context, target, newManager string) error {
	name, err := actor(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chart, err := s.chart(ctx)
	if err != nil {
		return err
	}
	if err := chart.CanMove(name, target, newManager); err != nil {
		return err
	}
	moved, _ := chart.Get(target)
	from := moved.Manager
	moved.Manager = newManager
	if err := s.repo.SaveAgents(ctx, moved); err != nil {
		return fmt.Errorf("save move of %s: %w", target, err)
	}
	raw, err := json.Marshal(map[string]string{"agent": target, "from": from, "to": newManager})
	if err != nil {
		return fmt.Errorf("encode move detail: %w", err)
	}
	if err := s.ports.Audit.Append(ctx, core.AuditEntry{Actor: "agent:" + name, Action: "agent.move", Target: "agent:" + target, Detail: raw}); err != nil {
		return fmt.Errorf("audit agent.move: %w", err)
	}
	return nil
}

// Get returns one agent. Every caller may read the society.
func (s *Society) Get(ctx context.Context, name string) (domain.Agent, error) {
	a, err := s.repo.GetAgent(ctx, name)
	if err != nil {
		return domain.Agent{}, err
	}
	return s.withRunState(a), nil
}

// Chart returns the org chart. Every caller may read it.
func (s *Society) Chart(ctx context.Context) (domain.Chart, error) {
	c, err := s.chart(ctx)
	if err != nil {
		return domain.Chart{}, err
	}
	return s.withRunStates(c), nil
}

// ListFilter selects agents. Empty fields match everything; retired agents
// are left out unless State asks for them.
type ListFilter struct {
	Kind    domain.Kind
	State   domain.State
	Manager string
	Team    string
	IsRoot  *bool
}

func (f ListFilter) match(chart domain.Chart, a domain.Agent) bool {
	switch {
	case f.State == "" && !a.Active(), f.State != "" && a.State != f.State:
		return false
	case f.Kind != "" && a.Kind != f.Kind:
		return false
	case f.Manager != "" && a.Manager != f.Manager:
		return false
	case f.Team != "" && chart.Team(a.Name) != f.Team:
		return false
	case f.IsRoot != nil && a.IsRoot != *f.IsRoot:
		return false
	}
	return true
}

// List returns matching agents sorted by name.
func (s *Society) List(ctx context.Context, f ListFilter) ([]domain.Agent, error) {
	chart, err := s.Chart(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.Agent
	for _, a := range chart.Agents() {
		if f.match(chart, a) {
			out = append(out, a)
		}
	}
	return out, nil
}
