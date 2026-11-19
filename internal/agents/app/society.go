// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Runtime is the runner supervisor (internal/agents/supervisor): it turns
// society decisions into Linux users, systemd units and runner turns.
type Runtime interface {
	// Provision creates the agent's user, home and unit, and returns its
	// uid (0 for root agents). Limits is nil for root agents.
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

	mu sync.Mutex
	// replies holds agent replies being written, by turn id.
	replies map[string]*reply
}

// New returns the society over repo. Ports is read when used, so modules
// may fill it after New.
func New(repo domain.Repository, rt Runtime, ports *core.Ports, clock core.Clock, ids core.IDs) *Society {
	return &Society{repo: repo, rt: rt, ports: ports, clock: clock, ids: ids, hub: NewHub(), replies: map[string]*reply{}}
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
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode %s detail: %w", action, err)
	}
	if err := s.ports.Audit.Append(ctx, core.AuditEntry{Actor: actorRef, Action: action, Target: target, Detail: raw}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return s.publish(ctx, actorRef, ev)
}

type event struct {
	typ, source, scope string
	data               any
}

func (s *Society) publish(ctx context.Context, actorRef string, ev event) error {
	data, err := json.Marshal(ev.data)
	if err != nil {
		return fmt.Errorf("encode %s: %w", ev.typ, err)
	}
	_, err = s.ports.Events.Publish(ctx, core.Event{
		SpecVersion: "1.0", Type: ev.typ, Source: ev.source, DataContentType: "application/json",
		Data: data, Actor: actorRef, ChartScope: ev.scope,
	})
	if err != nil {
		return fmt.Errorf("publish %s: %w", ev.typ, err)
	}
	return nil
}

type hiredData struct {
	Agent    string `json:"agent"`
	Manager  string `json:"manager"`
	Kind     string `json:"kind"`
	IsRoot   bool   `json:"isRoot"`
	Template string `json:"template"`
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
	founder, err := chart.Found(spec, by, s.clock.Now())
	if err != nil {
		return domain.Agent{}, err
	}
	if err := s.provisionAndStore(ctx, &founder); err != nil {
		return domain.Agent{}, err
	}
	data := hiredData{Agent: founder.Name, Manager: founder.Name, Kind: string(founder.Kind), IsRoot: true, Template: founder.Template}
	return founder, s.record(ctx, by, "agent.found", "agent:"+founder.Name, data,
		event{"agentsone.agents.hired", "/daemon", "team:" + founder.Name, data})
}

// Hire hires an agent below the calling agent.
func (s *Society) Hire(ctx context.Context, spec domain.HireSpec) (domain.Agent, error) {
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
	a, err := chart.Hire(name, spec, s.clock.Now())
	if err != nil {
		return domain.Agent{}, err
	}
	if err := s.provisionAndStore(ctx, &a); err != nil {
		return domain.Agent{}, err
	}
	team := domain.NewChart(append(chart.Agents(), a)).Team(a.Name)
	data := hiredData{Agent: a.Name, Manager: a.Manager, Kind: string(a.Kind), IsRoot: a.IsRoot, Template: a.Template}
	return a, s.record(ctx, "agent:"+name, "agent.hire", "agent:"+a.Name, data,
		event{"agentsone.agents.hired", "/agents/" + name, "team:" + team, data})
}

// provisionAndStore gives a its Linux user and unit, then stores it. When
// the store fails the provisioning is undone.
func (s *Society) provisionAndStore(ctx context.Context, a *domain.Agent) error {
	uid, err := s.rt.Provision(ctx, a.Core(), a.Limits)
	if err != nil {
		return fmt.Errorf("provision %s: %w", a.Name, err)
	}
	a.UID = uid
	if err := s.repo.CreateAgent(ctx, *a); err != nil {
		if undo := s.rt.Retire(ctx, a.Name); undo != nil {
			return fmt.Errorf("store %s: %w (undo provisioning: %w)", a.Name, err, undo)
		}
		return fmt.Errorf("store %s: %w", a.Name, err)
	}
	return nil
}

type editedData struct {
	Agent  string   `json:"agent"`
	Fields []string `json:"fields"`
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
	data := editedData{Agent: target, Fields: editedFields(u)}
	return next, s.record(ctx, "agent:"+name, "agent.edit", "agent:"+target, data,
		event{"agentsone.agents.edited", "/agents/" + name, "agent:" + target, data})
}

// editedFields names the changed fields in the event catalog's words. The
// catalog has no word for display name, role or description, so a wording
// edit is reported as the agent's profile line ("oneLine").
func editedFields(u domain.Update) []string {
	var fields []string
	if u.Limits != nil {
		fields = append(fields, "limits")
	}
	if u.Runner != nil || u.Kind != nil {
		fields = append(fields, "runner")
	}
	if u.DisplayName != nil || u.Role != nil || u.Description != nil {
		fields = append(fields, "oneLine")
	}
	return fields
}

type retiredData struct {
	Agent string `json:"agent"`
	By    string `json:"by"`
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
		event{"agentsone.agents.retired", "/agents/" + name, "team:" + team, retiredData{Agent: target, By: name}})
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
	return s.repo.GetAgent(ctx, name)
}

// Chart returns the org chart. Every caller may read it.
func (s *Society) Chart(ctx context.Context) (domain.Chart, error) { return s.chart(ctx) }

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
	chart, err := s.chart(ctx)
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
