// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
)

func TestCharter_Rewrite(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	current := domain.Charter{Text: "Be kind.", Version: 3}
	tests := []struct {
		name     string
		actor    string
		expected int
		text     string
		wantErr  error
	}{
		{"root agent with the current version", "founder", 3, "Be bold.", nil},
		{"custom agent", "lead", 3, "Be bold.", domain.ErrRootOnly},
		{"stale version", "founder", 2, "Be bold.", domain.ErrStaleVersion},
		{"text of 64000 characters", "founder", 3, strings.Repeat("é", 64000), nil},
		{"text of 64001 characters", "founder", 3, strings.Repeat("é", 64001), domain.ErrInvalidCharter},
		{"unknown agent", "ghost", 3, "x", domain.ErrAgentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := current.Rewrite(chart, tt.actor, tt.expected, tt.text, agentstest.At)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Rewrite() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCharter_RewriteBumpsTheVersion(t *testing.T) {
	t.Parallel()

	got, err := domain.Charter{}.Rewrite(agentstest.Chart(), "founder", 0, "Be kind.", agentstest.At)
	if err != nil {
		t.Fatal(err)
	}

	want := domain.Charter{Text: "Be kind.", Version: 1, UpdatedAt: agentstest.At, UpdatedBy: "agent:founder"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("charter (-want +got):\n%s", diff)
	}
}

func goalSpec(owner string) domain.GoalSpec {
	return domain.GoalSpec{Owner: owner, Title: "Ship the docs"}
}

func TestNewGoal_OwnerRule(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name, actor, owner string
		wantErr            error
	}{
		{"own goal", "dev", "dev", nil},
		{"own goal by default", "dev", "", nil},
		{"child", "lead", "dev", nil},
		{"grandchild", "lead", "intern", nil},
		{"peer", "lead", "ops", domain.ErrNotBelowActor},
		{"ancestor", "dev", "lead", domain.ErrNotBelowActor},
		{"unrelated branch", "dev", "watcher", domain.ErrNotBelowActor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewGoal(chart, tt.actor, goalSpec(tt.owner), nil, "gol_1", agentstest.At)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewGoal() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewGoal_Fields(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name    string
		spec    domain.GoalSpec
		wantErr error
	}{
		{"title of 200 characters", domain.GoalSpec{Title: strings.Repeat("t", 200)}, nil},
		{"title of 201 characters", domain.GoalSpec{Title: strings.Repeat("t", 201)}, domain.ErrInvalidGoal},
		{"empty title", domain.GoalSpec{Title: ""}, domain.ErrInvalidGoal},
		{"detail of 8001 characters", domain.GoalSpec{Title: "t", Detail: strings.Repeat("d", 8001)}, domain.ErrInvalidGoal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewGoal(chart, "dev", tt.spec, nil, "gol_1", agentstest.At)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewGoal() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewGoal_ParentMustBelongAboveTheOwner(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name, parentOwner string
		wantErr           error
	}{
		{"manager's goal", "lead", nil},
		{"top goal", "founder", nil},
		{"own goal", "dev", domain.ErrInvalidGoal},
		{"peer's goal", "ops", domain.ErrInvalidGoal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parent := domain.Goal{ID: "gol_parent", Owner: tt.parentOwner, Status: domain.GoalOpen}
			spec := goalSpec("dev")
			spec.ParentID = parent.ID
			_, err := domain.NewGoal(chart, "dev", spec, &parent, "gol_1", agentstest.At)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewGoal() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewGoal_StartsOpen(t *testing.T) {
	t.Parallel()

	got, err := domain.NewGoal(agentstest.Chart(), "lead", goalSpec("dev"), nil, "gol_1", agentstest.At)
	if err != nil {
		t.Fatal(err)
	}

	want := domain.Goal{ID: "gol_1", Owner: "dev", Title: "Ship the docs", Status: domain.GoalOpen, CreatedAt: agentstest.At, UpdatedAt: agentstest.At}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("goal (-want +got):\n%s", diff)
	}
}

func TestCanChangeGoal_OwnerOrAbove(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	goal := domain.Goal{ID: "gol_1", Owner: "dev", Status: domain.GoalOpen}
	tests := []struct {
		name, actor string
		wantErr     error
	}{
		{"owner", "dev", nil},
		{"manager", "lead", nil},
		{"top", "founder", nil},
		{"report of the owner", "intern", domain.ErrNotBelowActor},
		{"other branch", "ops", domain.ErrNotBelowActor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := domain.CanChangeGoal(chart, tt.actor, goal)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanChangeGoal() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestGoalUpdate_Apply(t *testing.T) {
	t.Parallel()
	achieved := domain.GoalAchieved
	unknown := domain.GoalStatus("maybe")
	long := strings.Repeat("t", 201)
	goal := domain.Goal{ID: "gol_1", Owner: "dev", Title: "Old", Status: domain.GoalOpen}

	got, err := domain.GoalUpdate{Status: &achieved}.Apply(goal, agentstest.At)
	if err != nil || got.Status != domain.GoalAchieved || !got.UpdatedAt.Equal(agentstest.At) {
		t.Fatalf("Apply(status) = %+v, %v", got, err)
	}
	if _, err := (domain.GoalUpdate{Status: &unknown}).Apply(goal, agentstest.At); !errors.Is(err, domain.ErrInvalidGoal) {
		t.Fatalf("unknown status error = %v, want %v", err, domain.ErrInvalidGoal)
	}
	if _, err := (domain.GoalUpdate{Title: &long}).Apply(goal, agentstest.At); !errors.Is(err, domain.ErrInvalidGoal) {
		t.Fatalf("long title error = %v, want %v", err, domain.ErrInvalidGoal)
	}
}
