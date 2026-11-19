// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/core"
)

// Domain errors. Each wraps the core sentinel the API maps to a status.
var (
	ErrInvalidAgent  = fmt.Errorf("invalid agent: %w", core.ErrInvalid)
	ErrAgentNotFound = fmt.Errorf("agent: %w", core.ErrNotFound)
	ErrNotBelowActor = fmt.Errorf("target is not below the actor: %w", core.ErrForbidden)
	ErrRootOnly      = fmt.Errorf("only root agents create root agents: %w", core.ErrForbidden)
	ErrNameTaken     = fmt.Errorf("agent name is taken: %w", core.ErrConflict)
	ErrRetired       = fmt.Errorf("agent is retired: %w", core.ErrConflict)
	ErrCycle         = fmt.Errorf("move would put an agent below itself: %w", core.ErrConflict)
)

// Kind is how an agent lives.
type Kind string

// Agent kinds.
const (
	KindAlwaysOn Kind = Kind(core.AgentAlwaysOn)
	KindChat     Kind = Kind(core.AgentChat)
	KindWaker    Kind = Kind(core.AgentWaker)
)

func (k Kind) valid() bool { return k == KindAlwaysOn || k == KindChat || k == KindWaker }

// State is an agent's life state.
type State string

// Agent states.
const (
	StateSleeping State = "sleeping"
	StateWaking   State = "waking"
	StateAwake    State = "awake"
	StateWorking  State = "working"
	StateFrozen   State = "frozen"
	StateFailed   State = "failed"
	StateRetired  State = "retired"
)

// Limits are the cgroup limits of a custom agent. Root agents have none.
type Limits struct {
	CPUPercent int
	MemoryMB   int
	Tasks      int
}

// DefaultLimits apply to a custom agent hired without limits.
var DefaultLimits = Limits{CPUPercent: 100, MemoryMB: 1024, Tasks: 256}

// Limit bounds, from the contract.
const (
	minCPUPercent = 1
	maxCPUPercent = 6400
	minMemoryMB   = 64
	minTasks      = 8
)

func (l Limits) validate() error {
	switch {
	case l.CPUPercent < minCPUPercent || l.CPUPercent > maxCPUPercent:
		return fmt.Errorf("%w: cpu_percent %d outside %d..%d", ErrInvalidAgent, l.CPUPercent, minCPUPercent, maxCPUPercent)
	case l.MemoryMB < minMemoryMB:
		return fmt.Errorf("%w: memory_mb %d below %d", ErrInvalidAgent, l.MemoryMB, minMemoryMB)
	case l.Tasks < minTasks:
		return fmt.Errorf("%w: tasks %d below %d", ErrInvalidAgent, l.Tasks, minTasks)
	}
	return nil
}

// Agent is one member of the society.
type Agent struct {
	Name        string
	DisplayName string
	Kind        Kind
	IsRoot      bool
	Runner      string
	// Manager is the agent directly above; empty only for the founder.
	Manager     string
	Role        string
	Description string
	State       State
	// Limits is nil for root agents.
	Limits *Limits
	// UID is the Linux uid: 0 for root agents, -1 until provisioned.
	UID        int
	Home       string
	Template   string
	CreatedAt  time.Time
	CreatedBy  string
	LastTurnAt *time.Time
}

// Active reports whether the agent is still part of the society.
func (a Agent) Active() bool { return a.State != StateRetired }

// Unit is the agent's systemd unit.
func (a Agent) Unit() string { return "agent@" + a.Name + ".service" }

// Core returns the directory view of the agent.
func (a Agent) Core() core.Agent {
	return core.Agent{
		Name: a.Name, Kind: core.AgentKind(a.Kind), IsRoot: a.IsRoot, Manager: a.Manager,
		Runner: a.Runner, UID: a.UID, Home: a.Home,
	}
}

func homeFor(name string, root bool) string {
	if root {
		return "/root/agents/" + name
	}
	return "/home/agents/" + name
}

var (
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	runnerPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

// ValidName reports whether s is a valid agent name (a Linux username).
func ValidName(s string) bool { return namePattern.MatchString(s) }

// Field lengths, in characters, from the contract.
const (
	maxDisplayName = 80
	maxRole        = 120
	maxDescription = 4000
)

func checkText(field, s string, minLen, maxLen int) error {
	n := utf8.RuneCountInString(s)
	if n < minLen || n > maxLen {
		return fmt.Errorf("%w: %s has %d characters, want %d..%d", ErrInvalidAgent, field, n, minLen, maxLen)
	}
	return nil
}

// HireSpec is what a hirer asks for.
type HireSpec struct {
	Name        string
	DisplayName string
	Kind        Kind
	IsRoot      bool
	Runner      string
	// Manager defaults to the hirer.
	Manager     string
	Role        string
	Description string
	Limits      *Limits
	Template    string
}

func (s HireSpec) validate() error {
	if !ValidName(s.Name) {
		return fmt.Errorf("%w: name %q is not a Linux username", ErrInvalidAgent, s.Name)
	}
	if !s.Kind.valid() {
		return fmt.Errorf("%w: kind %q", ErrInvalidAgent, s.Kind)
	}
	if !runnerPattern.MatchString(s.Runner) {
		return fmt.Errorf("%w: runner %q", ErrInvalidAgent, s.Runner)
	}
	return errors.Join(
		checkText("display_name", s.DisplayName, 1, maxDisplayName),
		checkText("role", s.Role, 1, maxRole),
		checkText("description", s.Description, 0, maxDescription),
		validLimits(s.Limits),
	)
}

func validLimits(l *Limits) error {
	if l == nil {
		return nil
	}
	return l.validate()
}

// Update is an edit to an agent. Nil fields stay as they are.
type Update struct {
	DisplayName *string
	Kind        *Kind
	Runner      *string
	Role        *string
	Description *string
	Limits      *Limits
}

// Restarts reports whether the edit changes how the agent's unit runs.
func (u Update) Restarts() bool { return u.Kind != nil || u.Runner != nil || u.Limits != nil }

// Apply returns a with u applied, or an error when u is invalid for a.
func (u Update) Apply(a Agent) (Agent, error) {
	if u.DisplayName != nil {
		if err := checkText("display_name", *u.DisplayName, 1, maxDisplayName); err != nil {
			return Agent{}, err
		}
		a.DisplayName = *u.DisplayName
	}
	if u.Kind != nil {
		if !u.Kind.valid() {
			return Agent{}, fmt.Errorf("%w: kind %q", ErrInvalidAgent, *u.Kind)
		}
		a.Kind = *u.Kind
	}
	if u.Runner != nil {
		if !runnerPattern.MatchString(*u.Runner) {
			return Agent{}, fmt.Errorf("%w: runner %q", ErrInvalidAgent, *u.Runner)
		}
		a.Runner = *u.Runner
	}
	if u.Role != nil {
		if err := checkText("role", *u.Role, 1, maxRole); err != nil {
			return Agent{}, err
		}
		a.Role = *u.Role
	}
	if u.Description != nil {
		if err := checkText("description", *u.Description, 0, maxDescription); err != nil {
			return Agent{}, err
		}
		a.Description = *u.Description
	}
	if u.Limits != nil {
		if a.IsRoot {
			return Agent{}, fmt.Errorf("%w: root agents run without limits", ErrInvalidAgent)
		}
		if err := u.Limits.validate(); err != nil {
			return Agent{}, err
		}
		l := *u.Limits
		a.Limits = &l
	}
	return a, nil
}
