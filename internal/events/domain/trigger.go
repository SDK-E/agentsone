// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// ActionKind is what a trigger does when it fires.
type ActionKind string

// Action kinds, with the contract's values.
const (
	ActionWake       ActionKind = "wake"
	ActionCreateTask ActionKind = "create-task"
)

var agentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// Priorities a trigger may give a task, with the contract's values.
var priorities = []string{"low", "normal", "high"}

// Action is what a trigger does. Agent is the agent to wake or the task assignee.
type Action struct {
	Kind     ActionKind
	Agent    string
	Message  string // wake: the reason text
	Title    string // create-task: the task title
	Priority string // create-task
}

// TriggerRate allows at most Max firings per PerSeconds.
type TriggerRate struct {
	Max        int
	PerSeconds int
}

// Rate converts the rate to a limiter rate.
func (r TriggerRate) Rate() WakeRate {
	return WakeRate{Max: r.Max, Window: time.Duration(r.PerSeconds) * time.Second}
}

// TriggerSpec is what a creator supplies.
type TriggerSpec struct {
	Name    string
	Filter  Filter
	Action  Action
	Rate    TriggerRate
	Enabled bool
}

// Validate checks the trigger rules.
func (s TriggerSpec) Validate() error {
	if s.Name == "" || len(s.Name) > 80 {
		return fmt.Errorf("%w: name must be 1 to 80 characters", ErrInvalidTrigger)
	}
	// A filter with no field would wake an agent for every event on the machine.
	if s.Filter.IsEmpty() && s.Filter.ChartScope == "" {
		return fmt.Errorf("%w: filter must name a type, source, subject prefix or actor", ErrInvalidTrigger)
	}
	if s.Rate.Max < 1 || s.Rate.Max > 1000 || s.Rate.PerSeconds < 1 || s.Rate.PerSeconds > 86400 {
		return fmt.Errorf("%w: wake_rate max must be 1 to 1000 per 1 to 86400 seconds", ErrInvalidTrigger)
	}
	return s.Action.validate()
}

func (a Action) validate() error {
	if !agentNamePattern.MatchString(a.Agent) {
		return fmt.Errorf("%w: the action needs a valid agent name", ErrInvalidTrigger)
	}
	switch a.Kind {
	case ActionWake:
		if len(a.Message) > 2000 {
			return fmt.Errorf("%w: wake message is over 2000 characters", ErrInvalidTrigger)
		}
	case ActionCreateTask:
		if a.Title == "" || len(a.Title) > 200 {
			return fmt.Errorf("%w: task title must be 1 to 200 characters", ErrInvalidTrigger)
		}
		if !contains(priorities, a.Priority) {
			return fmt.Errorf("%w: priority must be low, normal or high", ErrInvalidTrigger)
		}
	default:
		return fmt.Errorf("%w: action kind must be wake or create-task", ErrInvalidTrigger)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Trigger turns matching events into a wake or a task, at a limited rate.
type Trigger struct {
	ID           string
	Name         string
	Owner        string // agent name
	Filter       Filter
	Action       Action
	Rate         TriggerRate
	Enabled      bool
	FiredCount   int64
	DroppedCount int64
	LastFiredAt  *time.Time
	CreatedAt    time.Time
}

// NewTrigger builds a trigger from a valid spec.
func NewTrigger(s TriggerSpec, id, owner string, now time.Time) Trigger {
	return Trigger{
		ID: id, Name: s.Name, Owner: owner, Filter: s.Filter, Action: s.Action,
		Rate: s.Rate, Enabled: s.Enabled, CreatedAt: now,
	}
}

// Matches reports whether the trigger is on and e passes its filter.
func (t Trigger) Matches(e core.Event) bool { return t.Enabled && t.Filter.Matches(e) }

// TriggerPatch changes the fields that are set.
type TriggerPatch struct {
	Name    *string
	Filter  *Filter
	Action  *Action
	Rate    *TriggerRate
	Enabled *bool
}

// Apply returns t with the patch applied, validated as a whole.
func (t Trigger) Apply(p TriggerPatch) (Trigger, error) {
	if p.Name != nil {
		t.Name = *p.Name
	}
	if p.Filter != nil {
		t.Filter = *p.Filter
	}
	if p.Action != nil {
		t.Action = *p.Action
	}
	if p.Rate != nil {
		t.Rate = *p.Rate
	}
	if p.Enabled != nil {
		t.Enabled = *p.Enabled
	}
	spec := TriggerSpec{Name: t.Name, Filter: t.Filter, Action: t.Action, Rate: t.Rate, Enabled: t.Enabled}
	if err := spec.Validate(); err != nil {
		return Trigger{}, err
	}
	return t, nil
}

// Render replaces {{type}}, {{subject}} and {{id}} with values from e. Other
// text, including unknown placeholders, stays as it is.
func Render(template string, e core.Event) string {
	return strings.NewReplacer("{{type}}", e.Type, "{{subject}}", e.Subject, "{{id}}", e.ID).Replace(template)
}

// Viewer is who is reading events, for the chart-scope rule.
type Viewer struct {
	Name string
	// All is true for viewers that see everything: root agents, humans, the system and the host.
	All      bool
	TeamRoot string
	// IsBelow reports whether the named agent is strictly below the viewer in the chart.
	IsBelow func(name string) bool
}

// CanSee applies the chart-scope rule: machine events are public, team events
// belong to the team's branch, agent events to that agent and its managers.
func (v Viewer) CanSee(e core.Event) bool {
	kind, name, _ := strings.Cut(e.ChartScope, ":")
	switch kind {
	case "team":
		return v.All || name == v.TeamRoot
	case "agent":
		return v.CanSeeAgent(name)
	default:
		return true
	}
}

// CanSeeAgent reports whether the viewer is the agent, above it, or unrestricted.
func (v Viewer) CanSeeAgent(name string) bool {
	return v.All || name == v.Name || (v.IsBelow != nil && v.IsBelow(name))
}
