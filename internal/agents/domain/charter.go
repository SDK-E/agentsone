// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/core"
)

// Charter and goal errors.
var (
	ErrInvalidCharter = fmt.Errorf("invalid charter: %w", core.ErrInvalid)
	ErrStaleVersion   = fmt.Errorf("charter changed since it was read: %w", core.ErrConflict)
	ErrInvalidGoal    = fmt.Errorf("invalid goal: %w", core.ErrInvalid)
	ErrGoalNotFound   = fmt.Errorf("goal: %w", core.ErrNotFound)
)

const maxCharter = 64000

// Charter is the society's one optional charter. Version 0 means none yet.
type Charter struct {
	Text      string
	Version   int
	UpdatedAt time.Time
	UpdatedBy string
}

// Rewrite returns the charter actor writes over c. Only root agents may
// rewrite it: the charter binds the whole society, and root agents are the
// ones who already hold the whole machine. expected must be c.Version.
func (c Charter) Rewrite(chart Chart, actor string, expected int, text string, at time.Time) (Charter, error) {
	writer, err := chart.active(actor)
	if err != nil {
		return Charter{}, err
	}
	if !writer.IsRoot {
		return Charter{}, fmt.Errorf("%w: %s may not rewrite the charter", ErrRootOnly, actor)
	}
	if expected != c.Version {
		return Charter{}, fmt.Errorf("%w: read version %d, current %d", ErrStaleVersion, expected, c.Version)
	}
	if n := utf8.RuneCountInString(text); n > maxCharter {
		return Charter{}, fmt.Errorf("%w: %d characters, limit %d", ErrInvalidCharter, n, maxCharter)
	}
	return Charter{Text: text, Version: c.Version + 1, UpdatedAt: at, UpdatedBy: "agent:" + actor}, nil
}

// GoalStatus is where a goal stands.
type GoalStatus string

// Goal statuses.
const (
	GoalOpen     GoalStatus = "open"
	GoalAchieved GoalStatus = "achieved"
	GoalDropped  GoalStatus = "dropped"
)

func (s GoalStatus) valid() bool { return s == GoalOpen || s == GoalAchieved || s == GoalDropped }

// Goal is something an agent works toward.
type Goal struct {
	ID        string
	Owner     string
	Title     string
	Detail    string
	Status    GoalStatus
	ParentID  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GoalSpec is a goal to set. Owner defaults to the actor.
type GoalSpec struct {
	Owner    string
	Title    string
	Detail   string
	ParentID string
}

const (
	maxGoalTitle  = 200
	maxGoalDetail = 8000
)

func checkGoalText(title, detail string) error {
	if n := utf8.RuneCountInString(title); n < 1 || n > maxGoalTitle {
		return fmt.Errorf("%w: title has %d characters, want 1..%d", ErrInvalidGoal, n, maxGoalTitle)
	}
	if n := utf8.RuneCountInString(detail); n > maxGoalDetail {
		return fmt.Errorf("%w: detail has %d characters, limit %d", ErrInvalidGoal, n, maxGoalDetail)
	}
	return nil
}

// NewGoal returns the goal actor sets. Agents set their own goals and those
// of agents below them. A parent goal must belong to an agent above the
// owner: a goal serves a goal higher up the chart.
func NewGoal(chart Chart, actor string, spec GoalSpec, parent *Goal, id string, at time.Time) (Goal, error) {
	if spec.Owner == "" {
		spec.Owner = actor
	}
	if err := chart.atOrBelow(actor, spec.Owner); err != nil {
		return Goal{}, err
	}
	if err := checkGoalText(spec.Title, spec.Detail); err != nil {
		return Goal{}, err
	}
	if parent != nil && !chart.IsBelow(spec.Owner, parent.Owner) {
		return Goal{}, fmt.Errorf("%w: parent goal %s belongs to %s, not above %s", ErrInvalidGoal, parent.ID, parent.Owner, spec.Owner)
	}
	return Goal{
		ID: id, Owner: spec.Owner, Title: spec.Title, Detail: spec.Detail, Status: GoalOpen,
		ParentID: spec.ParentID, CreatedAt: at, UpdatedAt: at,
	}, nil
}

// CanChangeGoal checks that actor may edit or delete g: its owner or an
// agent above the owner.
func CanChangeGoal(chart Chart, actor string, g Goal) error {
	return chart.atOrBelow(actor, g.Owner)
}

// GoalUpdate is an edit to a goal. Nil fields stay as they are.
type GoalUpdate struct {
	Title  *string
	Detail *string
	Status *GoalStatus
}

// Apply returns g with u applied at time at.
func (u GoalUpdate) Apply(g Goal, at time.Time) (Goal, error) {
	if u.Title != nil {
		g.Title = *u.Title
	}
	if u.Detail != nil {
		g.Detail = *u.Detail
	}
	if u.Status != nil {
		if !u.Status.valid() {
			return Goal{}, fmt.Errorf("%w: status %q", ErrInvalidGoal, *u.Status)
		}
		g.Status = *u.Status
	}
	if err := checkGoalText(g.Title, g.Detail); err != nil {
		return Goal{}, err
	}
	g.UpdatedAt = at
	return g, nil
}
