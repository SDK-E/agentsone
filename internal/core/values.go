// SPDX-License-Identifier: MIT

package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// agentName is the contract's AgentName pattern.
var agentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// FreezeKind is the contract's FreezeScope value.
type FreezeKind string

// Freeze kinds. All is a hard wall held by the host; Team and Agent are soft.
const (
	FreezeAll   FreezeKind = "all"
	FreezeTeam  FreezeKind = "team"
	FreezeAgent FreezeKind = "agent"
)

// FreezeScope is what a freeze covers: everything, one team (by the agent at
// its head) or one agent. Target is empty for FreezeAll. Its text form is
// "all", "team:<head>" or "agent:<name>".
type FreezeScope struct {
	Kind   FreezeKind
	Target string
}

// ParseFreezeScope reads the text form of a scope.
func ParseFreezeScope(s string) (FreezeScope, error) {
	kind, target, hasTarget := strings.Cut(s, ":")
	scope := FreezeScope{Kind: FreezeKind(kind), Target: target}
	if err := scope.validate(hasTarget); err != nil {
		return FreezeScope{}, fmt.Errorf("freeze scope %q: %w", s, err)
	}
	return scope, nil
}

func (s FreezeScope) validate(hasTarget bool) error {
	switch s.Kind {
	case FreezeAll:
		if hasTarget {
			return fmt.Errorf("all takes no target: %w", ErrInvalid)
		}
		return nil
	case FreezeTeam, FreezeAgent:
		if !agentName.MatchString(s.Target) {
			return fmt.Errorf("target must be an agent name: %w", ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("kind must be all, team or agent: %w", ErrInvalid)
	}
}

// String returns the text form.
func (s FreezeScope) String() string {
	if s.Kind == FreezeAll {
		return string(FreezeAll)
	}
	return string(s.Kind) + ":" + s.Target
}

// BusMessage is one message on the bus.
type BusMessage struct {
	ID string
	// From is the sender as "<kind>:<name>", for example "agent:founder".
	From string
	// To is "agent:<name>" or "channel:<name>".
	To   string
	Body string
	Sent time.Time
}

// Validate checks the fields every message needs.
func (m BusMessage) Validate() error {
	if m.From == "" || m.To == "" || m.Body == "" {
		return fmt.Errorf("bus message needs from, to and body: %w", ErrInvalid)
	}
	return nil
}

// Final task statuses, with the contract's TaskStatus values.
const (
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"
)

// TaskOutcome is how a task ended.
type TaskOutcome struct {
	// Status is TaskDone, TaskFailed or TaskCancelled.
	Status string
	Result string
}

// Task priorities, with the contract's TaskPriority values.
const (
	TaskPriorityLow    = "low"
	TaskPriorityNormal = "normal"
	TaskPriorityHigh   = "high"
)

// TaskPriority checks p and returns TaskPriorityNormal for "".
func TaskPriority(p string) (string, error) {
	switch p {
	case "":
		return TaskPriorityNormal, nil
	case TaskPriorityLow, TaskPriorityNormal, TaskPriorityHigh:
		return p, nil
	default:
		return "", fmt.Errorf("task priority %q must be low, normal or high: %w", p, ErrInvalid)
	}
}

// Validate checks that Status is a final status.
func (o TaskOutcome) Validate() error {
	switch o.Status {
	case TaskDone, TaskFailed, TaskCancelled:
		return nil
	default:
		return fmt.Errorf("task outcome %q must be done, failed or cancelled: %w", o.Status, ErrInvalid)
	}
}

// Token is an issued bearer token.
type Token struct {
	Value   string
	Expires time.Time
}

// AgentTemplate is one agent template from a plugin.
type AgentTemplate struct {
	ID     string
	Name   string
	Kind   AgentKind
	IsRoot bool
	Runner string
	// Raw is the agent-template document as installed.
	Raw json.RawMessage
}
