// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Domain errors. The app layer maps them to core errors.
var (
	// ErrInvalid is a field that breaks a rule of the model.
	ErrInvalid = errors.New("invalid")
	// ErrClosed is a change to a task that is already done, cancelled or failed.
	ErrClosed = errors.New("task is closed")
	// ErrNotPermitted is an actor that may not do this.
	ErrNotPermitted = errors.New("not permitted")
	// ErrNotFound is a task or schedule that does not exist.
	ErrNotFound = errors.New("not found")
)

// Field limits, in characters, from the contract.
const (
	maxTitle  = 200
	maxDetail = 16000
	maxResult = 16000
)

// Status is where a task stands, with the contract's values.
type Status string

// Task statuses.
const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in-progress"
	StatusBlocked    Status = "blocked"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
	StatusFailed     Status = "failed"
)

// Closed reports whether no further change is allowed.
func (s Status) Closed() bool {
	return s == StatusDone || s == StatusCancelled || s == StatusFailed
}

// Priority is how urgent a task is.
type Priority string

// Task priorities.
const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

func (p Priority) valid() bool {
	return p == PriorityLow || p == PriorityNormal || p == PriorityHigh
}

// Source is how a task came to exist.
type Source string

// Task sources.
const (
	SourceHuman    Source = "human"
	SourceAgent    Source = "agent"
	SourceRouter   Source = "router"
	SourceTrigger  Source = "trigger"
	SourceSchedule Source = "schedule"
)

func (s Source) valid() bool {
	switch s {
	case SourceHuman, SourceAgent, SourceRouter, SourceTrigger, SourceSchedule:
		return true
	}
	return false
}

// ActorKind is the kind of principal behind a change.
type ActorKind string

// Actor kinds, as in core.CallerKind.
const (
	ActorAgent  ActorKind = "agent"
	ActorHuman  ActorKind = "human"
	ActorSystem ActorKind = "system"
	ActorHost   ActorKind = "host"
	ActorPlugin ActorKind = "plugin"
)

// Actor is who did something, written "<kind>:<name>".
type Actor struct {
	Kind ActorKind
	Name string
}

// ParseActor reads "<kind>:<name>".
func ParseActor(s string) (Actor, error) {
	kind, name, ok := strings.Cut(s, ":")
	switch ActorKind(kind) {
	case ActorAgent, ActorHuman, ActorSystem, ActorHost, ActorPlugin:
	default:
		ok = false
	}
	if !ok || name == "" {
		return Actor{}, fmt.Errorf("%w: actor %q is not <kind>:<name>", ErrInvalid, s)
	}
	return Actor{Kind: ActorKind(kind), Name: name}, nil
}

// String is "<kind>:<name>".
func (a Actor) String() string { return string(a.Kind) + ":" + a.Name }

// Privileged is true for everything but an agent. Humans, the host tool and
// daemon parts act with the owner's authority and are not bound by the chart.
func (a Actor) Privileged() bool { return a.Kind != ActorAgent }

// Standing says how an actor stands to one agent, as the chart sees it.
// The app layer reads the chart and fills it in.
type Standing struct {
	// Self is true when the actor is that agent.
	Self bool
	// Above is true when the actor is strictly above that agent.
	Above bool
}

func (s Standing) allows() bool { return s.Self || s.Above }

// Task is a piece of work for an agent.
type Task struct {
	ID        string
	Title     string
	Detail    string
	Status    Status
	Priority  Priority
	Assignee  string // agent name, empty when unassigned
	Source    Source
	CreatedBy Actor
	ParentID  string
	TriggerID string
	Result    string // empty until done or failed
	DueAt     *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewTaskParams are the fields of a new task.
type NewTaskParams struct {
	ID        string
	Title     string
	Detail    string
	Priority  Priority // empty means normal
	Assignee  string
	Source    Source
	CreatedBy Actor
	ParentID  string
	TriggerID string
	DueAt     *time.Time
	Now       time.Time
}

// NewTask builds an open task.
func NewTask(p NewTaskParams) (Task, error) {
	if p.Priority == "" {
		p.Priority = PriorityNormal
	}
	if !p.Source.valid() {
		return Task{}, fmt.Errorf("%w: unknown source %q", ErrInvalid, p.Source)
	}
	t := Task{
		ID: p.ID, Title: strings.TrimSpace(p.Title), Detail: p.Detail, Status: StatusOpen,
		Priority: p.Priority, Assignee: p.Assignee, Source: p.Source, CreatedBy: p.CreatedBy,
		ParentID: p.ParentID, TriggerID: p.TriggerID, DueAt: p.DueAt,
		CreatedAt: p.Now.UTC(), UpdatedAt: p.Now.UTC(),
	}
	if err := t.validateWording(); err != nil {
		return Task{}, err
	}
	return t, nil
}

func (t Task) validateWording() error {
	switch {
	case t.Title == "" || utf8.RuneCountInString(t.Title) > maxTitle:
		return fmt.Errorf("%w: title needs 1 to %d characters", ErrInvalid, maxTitle)
	case utf8.RuneCountInString(t.Detail) > maxDetail:
		return fmt.Errorf("%w: detail is over %d characters", ErrInvalid, maxDetail)
	case !t.Priority.valid():
		return fmt.Errorf("%w: unknown priority %q", ErrInvalid, t.Priority)
	}
	return nil
}

// Assign gives the task to an agent and puts it back to open.
func (t *Task) Assign(agent string, now time.Time) error {
	if t.Status.Closed() {
		return ErrClosed
	}
	t.Assignee, t.Status, t.UpdatedAt = agent, StatusOpen, now.UTC()
	return nil
}

// Complete closes the task as done or failed with a result.
func (t *Task) Complete(outcome Status, result string, now time.Time) error {
	if t.Status.Closed() {
		return ErrClosed
	}
	if outcome != StatusDone && outcome != StatusFailed {
		return fmt.Errorf("%w: outcome must be done or failed, not %q", ErrInvalid, outcome)
	}
	result = strings.TrimSpace(result)
	if result == "" || utf8.RuneCountInString(result) > maxResult {
		return fmt.Errorf("%w: result needs 1 to %d characters", ErrInvalid, maxResult)
	}
	t.Status, t.Result, t.UpdatedAt = outcome, result, now.UTC()
	return nil
}

// Cancel closes the task as cancelled.
func (t *Task) Cancel(now time.Time) error {
	if t.Status.Closed() {
		return ErrClosed
	}
	t.Status, t.UpdatedAt = StatusCancelled, now.UTC()
	return nil
}

// TaskEdit is a change to a task's wording and priority. Nil fields stay.
type TaskEdit struct {
	Title    *string
	Detail   *string
	Priority *Priority
}

// Edit applies e. A refused edit leaves the task untouched.
func (t *Task) Edit(e TaskEdit, now time.Time) error {
	if t.Status.Closed() {
		return ErrClosed
	}
	next := *t
	if e.Title != nil {
		next.Title = strings.TrimSpace(*e.Title)
	}
	if e.Detail != nil {
		next.Detail = *e.Detail
	}
	if e.Priority != nil {
		next.Priority = *e.Priority
	}
	if err := next.validateWording(); err != nil {
		return err
	}
	next.UpdatedAt = now.UTC()
	*t = next
	return nil
}

// CanAssign says whether actor may give a task to an agent, where to is how
// the actor stands to that agent: itself or an agent below it, or anyone for
// a human.
func CanAssign(actor Actor, to Standing) error {
	if actor.Privileged() || to.allows() {
		return nil
	}
	return fmt.Errorf("%w: %s may assign only to itself or to agents below it", ErrNotPermitted, actor)
}

// CanComplete says whether actor may report the outcome: the holder, an
// agent above the holder, or a human. holder is the actor's standing to the
// assignee.
func (t Task) CanComplete(actor Actor, holder Standing) error {
	if actor.Privileged() || (t.Assignee != "" && holder.allows()) {
		return nil
	}
	return fmt.Errorf("%w: only the holder or an agent above may complete this task", ErrNotPermitted)
}

// CanCancel says whether actor may cancel: the creator, an agent above the
// creator, or a human. creator is the actor's standing to the creator.
func (t Task) CanCancel(actor Actor, creator Standing) error {
	if actor.Privileged() || creator.allows() {
		return nil
	}
	return fmt.Errorf("%w: only the creator or an agent above may cancel this task", ErrNotPermitted)
}

// CanEdit says whether actor may edit: the creator, the holder, an agent
// above either, or a human.
func (t Task) CanEdit(actor Actor, creator, holder Standing) error {
	if actor.Privileged() || creator.allows() || (t.Assignee != "" && holder.allows()) {
		return nil
	}
	return fmt.Errorf("%w: only the creator, the holder or an agent above may edit this task", ErrNotPermitted)
}
