// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func pushPool(t *testing.T, e env, total int64) {
	t.Helper()
	p := domain.Pool{Currency: "USD", TotalMicros: total, CheckIntervalSeconds: 60}
	if err := e.society.Budget().PushPool(asHost(), p); err != nil {
		t.Fatal(err)
	}
}

func TestPushPool_OnlyTheHostPushesTheTotal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := domain.Pool{Currency: "USD", TotalMicros: 10, CheckIntervalSeconds: 60}

	if err := e.society.Budget().PushPool(asAgent("founder"), p); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("agent push error = %v, want %v", err, core.ErrForbidden)
	}
	if _, err := e.society.Budget().Pool(context.Background()); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Pool() before a push error = %v, want %v", err, core.ErrUnavailable)
	}
	bad := p
	bad.Currency = "usd"
	if err := e.society.Budget().PushPool(asHost(), bad); !errors.Is(err, domain.ErrInvalidBudget) {
		t.Fatalf("bad push error = %v, want %v", err, domain.ErrInvalidBudget)
	}

	if err := e.society.Budget().PushPool(asHost(), p); err != nil {
		t.Fatal(err)
	}
	got, err := e.society.Budget().Pool(context.Background())
	if err != nil || !got.Known || got.TotalMicros != 10 {
		t.Fatalf("Pool() = %+v %v", got, err)
	}
}

func TestAllocate_SetsAShareBelowAndAudits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	pushPool(t, e, 1000)
	amount := int64(600)

	node, err := e.society.Budget().Allocate(asAgent("founder"), "lead", &amount)
	if err != nil {
		t.Fatal(err)
	}

	if node.HoldingMicros != 600 || *node.AllocatedMicros != 600 {
		t.Fatalf("node %+v", node)
	}
	got, err := e.society.Budget().Allocation(context.Background(), "intern")
	if err != nil || got.AllocatedMicros != 600 {
		t.Fatalf("Allocation(intern) = %+v %v, want the lead's 600 shared", got, err)
	}
	if diff := cmp.Diff([]string{"budget.allocate"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestAllocate_RefusalsChangeNothing(t *testing.T) {
	t.Parallel()
	over := int64(1001)
	one := int64(1)
	tests := []struct {
		name    string
		ctx     context.Context
		target  string
		amount  *int64
		wantErr error
	}{
		{"more than the manager holds", asAgent("founder"), "lead", &over, domain.ErrOverAllocated},
		{"above self", asAgent("dev"), "lead", &one, domain.ErrNotBelowActor},
		{"human", asHuman("ada"), "lead", &one, core.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			pushPool(t, e, 1000)
			_, err := e.society.Budget().Allocate(tt.ctx, tt.target, tt.amount)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Allocate() error = %v, want %v", err, tt.wantErr)
			}
			if alloc, _ := e.repo.Allocations(context.Background()); len(alloc) != 0 {
				t.Errorf("allocations %v", alloc)
			}
			e.assertNothingHappened(t)
		})
	}
}

func TestRecordUsage_CountsAgainstTheShare(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	pushPool(t, e, 1000)

	if err := e.society.Budget().RecordUsage(context.Background(), "dev", 250); err != nil {
		t.Fatal(err)
	}

	split, err := e.society.Budget().Split(context.Background())
	if err != nil || split.Node("lead").SpentMicros != 250 {
		t.Fatalf("lead spent = %d %v, want 250", split.Node("lead").SpentMicros, err)
	}
	if err := e.society.Budget().RecordUsage(context.Background(), "dev", -1); !errors.Is(err, domain.ErrInvalidBudget) {
		t.Fatalf("negative usage error = %v", err)
	}
	if err := e.society.Budget().RecordUsage(context.Background(), "ghost", 1); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Fatalf("unknown agent usage error = %v", err)
	}
	if _, err := e.society.Budget().Allocation(context.Background(), "ghost"); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Fatalf("unknown agent allocation error = %v", err)
	}
}

func TestCharter_RootAgentsRewriteIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	got, err := e.society.RewriteCharter(asAgent("founder"), 0, "Be kind.")
	if err != nil {
		t.Fatal(err)
	}

	if got.Version != 1 {
		t.Fatalf("version = %d", got.Version)
	}
	read, _ := e.society.Charter(asHuman("ada"))
	if read.Text != "Be kind." {
		t.Fatalf("read %+v", read)
	}
	if diff := cmp.Diff([]string{"charter.rewrite"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestCharter_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ctx      context.Context
		expected int
		wantErr  error
	}{
		{"custom agent", asAgent("lead"), 0, domain.ErrRootOnly},
		{"stale version", asAgent("founder"), 3, domain.ErrStaleVersion},
		{"human", asHuman("ada"), 0, core.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			_, err := e.society.RewriteCharter(tt.ctx, tt.expected, "x")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RewriteCharter() error = %v, want %v", err, tt.wantErr)
			}
			if c, _ := e.repo.Charter(context.Background()); c.Version != 0 {
				t.Errorf("charter saved: %+v", c)
			}
			e.assertNothingHappened(t)
		})
	}
}

func TestGoals_Lifecycle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	top, err := e.society.SetGoal(asAgent("lead"), domain.GoalSpec{Title: "Ship docs"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := e.society.SetGoal(asAgent("lead"), domain.GoalSpec{Owner: "dev", Title: "Write layout", ParentID: top.ID})
	if err != nil {
		t.Fatal(err)
	}
	achieved := domain.GoalAchieved

	updated, err := e.society.UpdateGoal(asAgent("dev"), sub.ID, domain.GoalUpdate{Status: &achieved})
	if err != nil || updated.Status != domain.GoalAchieved {
		t.Fatalf("UpdateGoal() = %+v %v", updated, err)
	}
	if err := e.society.DeleteGoal(asAgent("lead"), sub.ID); err != nil {
		t.Fatal(err)
	}

	list, _ := e.society.Goals(asHuman("ada"), domain.GoalFilter{})
	if len(list) != 1 || list[0].ID != top.ID {
		t.Fatalf("goals %+v", list)
	}
	if got, _ := e.society.Goal(asHuman("ada"), top.ID); got.Owner != "lead" {
		t.Fatalf("Goal() = %+v", got)
	}
	if diff := cmp.Diff([]string{"goal.set", "goal.set", "goal.edit", "goal.delete"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"agentsone.agents.edited", "agentsone.agents.edited", "agentsone.agents.edited", "agentsone.agents.edited"}, e.eventTypes()); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestGoals_Refusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	devGoal, err := e.society.SetGoal(asAgent("dev"), domain.GoalSpec{Title: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	title := "Theirs"

	if _, err := e.society.SetGoal(asAgent("dev"), domain.GoalSpec{Owner: "lead", Title: "x"}); !errors.Is(err, domain.ErrNotBelowActor) {
		t.Errorf("goal above self: %v", err)
	}
	if _, err := e.society.SetGoal(asAgent("dev"), domain.GoalSpec{Title: "x", ParentID: "gol_missing"}); !errors.Is(err, domain.ErrInvalidGoal) {
		t.Errorf("missing parent: %v", err)
	}
	if _, err := e.society.UpdateGoal(asAgent("ops"), devGoal.ID, domain.GoalUpdate{Title: &title}); !errors.Is(err, domain.ErrNotBelowActor) {
		t.Errorf("edit from another branch: %v", err)
	}
	if err := e.society.DeleteGoal(asAgent("intern"), devGoal.ID); !errors.Is(err, domain.ErrNotBelowActor) {
		t.Errorf("delete by a report: %v", err)
	}
	if err := e.society.DeleteGoal(asAgent("dev"), "gol_missing"); !errors.Is(err, domain.ErrGoalNotFound) {
		t.Errorf("delete unknown: %v", err)
	}
	if _, err := e.society.SetGoal(asHuman("ada"), domain.GoalSpec{Title: "x"}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("human sets a goal: %v", err)
	}
	if got := e.auditActions(); len(got) != 1 {
		t.Errorf("audit %v, want only the first goal.set", got)
	}
}
