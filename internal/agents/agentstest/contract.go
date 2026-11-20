// SPDX-License-Identifier: MIT

package agentstest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/domain"
)

// RunRepositoryContract is the suite every domain.Repository passes: the
// SQLite repository and the in-memory one both run it, which keeps the
// fake honest.
func RunRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Helper()
	t.Run("agents", func(t *testing.T) { t.Parallel(); runAgentContract(t, newRepo) })
	t.Run("charter", func(t *testing.T) { t.Parallel(); runCharterContract(t, newRepo) })
	t.Run("goals", func(t *testing.T) { t.Parallel(); runGoalContract(t, newRepo) })
	t.Run("budget", func(t *testing.T) { t.Parallel(); runBudgetContract(t, newRepo) })
	t.Run("chat", func(t *testing.T) { t.Parallel(); runChatContract(t, newRepo) })
	t.Run("freezes", func(t *testing.T) { t.Parallel(); runFreezeContract(t, newRepo) })
	t.Run("seed record", func(t *testing.T) { t.Parallel(); runSeedRecordContract(t, newRepo) })
}

func runSeedRecordContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	applied := func(t *testing.T, repo domain.Repository) bool {
		t.Helper()
		got, err := repo.SeedApplied(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	t.Run("has no seed applied at first", func(t *testing.T) {
		t.Parallel()
		if applied(t, newRepo(t)) {
			t.Fatal("SeedApplied() = true on a new repository")
		}
	})
	t.Run("records the seed once", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		at := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
		first, err := repo.MarkSeedApplied(t.Context(), at)
		if err != nil || !first {
			t.Fatalf("first MarkSeedApplied() = %v, %v, want true", first, err)
		}
		again, err := repo.MarkSeedApplied(t.Context(), at.Add(time.Hour))
		if err != nil || again {
			t.Fatalf("second MarkSeedApplied() = %v, %v, want false", again, err)
		}
		if !applied(t, repo) {
			t.Fatal("SeedApplied() = false after MarkSeedApplied")
		}
	})
}

func runFreezeContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	freezes := func(t *testing.T, repo domain.Repository) []string {
		t.Helper()
		got, err := repo.Freezes(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	t.Run("has no freezes at first", func(t *testing.T) {
		t.Parallel()
		if got := freezes(t, newRepo(t)); len(got) != 0 {
			t.Fatalf("Freezes() = %v, want none", got)
		}
	})
	t.Run("lists added scopes sorted", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		for _, s := range []string{"team:ops", "agent:dev", "all"} {
			if err := repo.AddFreeze(t.Context(), s); err != nil {
				t.Fatal(err)
			}
		}
		if diff := cmp.Diff([]string{"agent:dev", "all", "team:ops"}, freezes(t, repo)); diff != "" {
			t.Fatalf("freezes (-want +got):\n%s", diff)
		}
	})
	t.Run("adding an active scope again changes nothing", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		for range 2 {
			if err := repo.AddFreeze(t.Context(), "agent:dev"); err != nil {
				t.Fatal(err)
			}
		}
		if diff := cmp.Diff([]string{"agent:dev"}, freezes(t, repo)); diff != "" {
			t.Fatalf("freezes (-want +got):\n%s", diff)
		}
	})
	t.Run("removes a scope", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		for _, s := range []string{"agent:dev", "all"} {
			if err := repo.AddFreeze(t.Context(), s); err != nil {
				t.Fatal(err)
			}
		}
		if err := repo.RemoveFreeze(t.Context(), "all"); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]string{"agent:dev"}, freezes(t, repo)); diff != "" {
			t.Fatalf("freezes (-want +got):\n%s", diff)
		}
	})
	t.Run("removing an inactive scope changes nothing", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.AddFreeze(t.Context(), "agent:dev"); err != nil {
			t.Fatal(err)
		}
		if err := repo.RemoveFreeze(t.Context(), "team:ops"); err != nil {
			t.Fatalf("RemoveFreeze() of an inactive scope error = %v, want nil", err)
		}
		if diff := cmp.Diff([]string{"agent:dev"}, freezes(t, repo)); diff != "" {
			t.Fatalf("freezes (-want +got):\n%s", diff)
		}
	})
}

func fullAgent() domain.Agent {
	turn := time.Date(2026, 11, 19, 18, 38, 55, 123456789, time.UTC)
	a := New("builder", Below("founder"))
	a.DisplayName = "Builder"
	a.Kind = domain.KindChat
	a.Runner = "claude"
	a.Role = "Builder"
	a.Description = "Builds the docs site."
	a.State = domain.StateWorking
	a.Limits = &domain.Limits{CPUPercent: 200, MemoryMB: 2048, Tasks: 512}
	a.UID = 1103
	a.Template = "small-seed/builder"
	a.CreatedAt = time.Date(2026, 11, 19, 8, 20, 31, 5, time.UTC)
	a.CreatedBy = "agent:founder"
	a.LastTurnAt = &turn
	return a
}

func createAgents(t *testing.T, repo domain.Repository, agents ...domain.Agent) {
	t.Helper()
	for _, a := range agents {
		if err := repo.CreateAgent(t.Context(), a); err != nil {
			t.Fatalf("CreateAgent(%s): %v", a.Name, err)
		}
	}
}

func runAgentContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Run("returns not found for an unknown agent", func(t *testing.T) {
		t.Parallel()
		_, err := newRepo(t).GetAgent(t.Context(), "ghost")
		if !errors.Is(err, domain.ErrAgentNotFound) {
			t.Fatalf("GetAgent() error = %v, want %v", err, domain.ErrAgentNotFound)
		}
	})
	t.Run("round trips every field of a custom agent", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		want := fullAgent()
		createAgents(t, repo, want)
		got, err := repo.GetAgent(t.Context(), want.Name)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("agent (-want +got):\n%s", diff)
		}
	})
	t.Run("round trips a root agent without limits or turns", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		want := New("founder", Root())
		createAgents(t, repo, want)
		got, err := repo.GetAgent(t.Context(), "founder")
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("agent (-want +got):\n%s", diff)
		}
	})
	t.Run("refuses a taken name", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createAgents(t, repo, New("dev"))
		err := repo.CreateAgent(t.Context(), New("dev"))
		if !errors.Is(err, domain.ErrNameTaken) {
			t.Fatalf("CreateAgent() error = %v, want %v", err, domain.ErrNameTaken)
		}
	})
	t.Run("lists agents sorted by name", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createAgents(t, repo, New("ops"), New("dev"), New("lead"))
		all, err := repo.ListAgents(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, a := range all {
			names = append(names, a.Name)
		}
		if diff := cmp.Diff([]string{"dev", "lead", "ops"}, names); diff != "" {
			t.Fatalf("names (-want +got):\n%s", diff)
		}
	})
	t.Run("lists nothing when empty", func(t *testing.T) {
		t.Parallel()
		all, err := newRepo(t).ListAgents(t.Context())
		if err != nil || len(all) != 0 {
			t.Fatalf("ListAgents() = %v, %v, want none", all, err)
		}
	})
	t.Run("saves changes to several agents", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createAgents(t, repo, New("dev"), New("ops"))
		dev, ops := New("dev", Below("ops"), WithState(domain.StateRetired)), New("ops", Root())
		if err := repo.SaveAgents(t.Context(), dev, ops); err != nil {
			t.Fatal(err)
		}
		for _, want := range []domain.Agent{dev, ops} {
			got, err := repo.GetAgent(t.Context(), want.Name)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("%s (-want +got):\n%s", want.Name, diff)
			}
		}
	})
	t.Run("saves none when one agent is missing", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createAgents(t, repo, New("dev"))
		err := repo.SaveAgents(t.Context(), New("dev", WithState(domain.StateRetired)), New("ghost"))
		if !errors.Is(err, domain.ErrAgentNotFound) {
			t.Fatalf("SaveAgents() error = %v, want %v", err, domain.ErrAgentNotFound)
		}
		got, err := repo.GetAgent(t.Context(), "dev")
		if err != nil || got.State != domain.StateSleeping {
			t.Fatalf("dev after failed save = %+v, %v, want unchanged", got, err)
		}
	})
	t.Run("does not share memory with the caller", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		a := fullAgent()
		createAgents(t, repo, a)
		a.Limits.Tasks = 9
		got, err := repo.GetAgent(t.Context(), a.Name)
		if err != nil {
			t.Fatal(err)
		}
		got.Limits.CPUPercent = 1
		again, err := repo.GetAgent(t.Context(), a.Name)
		if err != nil {
			t.Fatal(err)
		}
		if again.Limits.Tasks != 512 || again.Limits.CPUPercent != 200 {
			t.Fatalf("stored limits changed through an alias: %+v", *again.Limits)
		}
	})
}

func runCharterContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Run("has no charter at first", func(t *testing.T) {
		t.Parallel()
		got, err := newRepo(t).Charter(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(domain.Charter{}, got); diff != "" {
			t.Fatalf("charter (-want +got):\n%s", diff)
		}
	})
	t.Run("stores the next version", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		first := domain.Charter{Text: "Be kind.", Version: 1, UpdatedAt: At, UpdatedBy: "agent:founder"}
		second := domain.Charter{Text: "Be bold.", Version: 2, UpdatedAt: At.Add(time.Minute), UpdatedBy: "agent:guard"}
		for _, c := range []domain.Charter{first, second} {
			if err := repo.SaveCharter(t.Context(), c); err != nil {
				t.Fatalf("SaveCharter(v%d): %v", c.Version, err)
			}
		}
		got, err := repo.Charter(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(second, got); diff != "" {
			t.Fatalf("charter (-want +got):\n%s", diff)
		}
	})
	t.Run("refuses a version that does not follow the stored one", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.SaveCharter(t.Context(), domain.Charter{Text: "a", Version: 1, UpdatedAt: At, UpdatedBy: "agent:founder"}); err != nil {
			t.Fatal(err)
		}
		for _, v := range []int{1, 3} {
			err := repo.SaveCharter(t.Context(), domain.Charter{Text: "b", Version: v, UpdatedAt: At, UpdatedBy: "agent:founder"})
			if !errors.Is(err, domain.ErrStaleVersion) {
				t.Errorf("SaveCharter(v%d) error = %v, want %v", v, err, domain.ErrStaleVersion)
			}
		}
		got, err := repo.Charter(t.Context())
		if err != nil || got.Text != "a" {
			t.Fatalf("charter after refusals = %+v, %v, want text a", got, err)
		}
	})
}

func goal(id, owner string, status domain.GoalStatus) domain.Goal {
	return domain.Goal{
		ID: id, Owner: owner, Title: "Goal " + id, Detail: "Detail.", Status: status,
		CreatedAt: At, UpdatedAt: At.Add(time.Second),
	}
}

func createGoals(t *testing.T, repo domain.Repository, goals ...domain.Goal) {
	t.Helper()
	for _, g := range goals {
		if err := repo.CreateGoal(t.Context(), g); err != nil {
			t.Fatalf("CreateGoal(%s): %v", g.ID, err)
		}
	}
}

func goalIDs(goals []domain.Goal) []string {
	ids := []string{}
	for _, g := range goals {
		ids = append(ids, g.ID)
	}
	return ids
}

func runGoalContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Run("returns not found for an unknown goal", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if _, err := repo.GetGoal(t.Context(), "gol_missing"); !errors.Is(err, domain.ErrGoalNotFound) {
			t.Errorf("GetGoal() error = %v, want %v", err, domain.ErrGoalNotFound)
		}
		if err := repo.SaveGoal(t.Context(), goal("gol_missing", "dev", domain.GoalOpen)); !errors.Is(err, domain.ErrGoalNotFound) {
			t.Errorf("SaveGoal() error = %v, want %v", err, domain.ErrGoalNotFound)
		}
		if err := repo.DeleteGoal(t.Context(), "gol_missing"); !errors.Is(err, domain.ErrGoalNotFound) {
			t.Errorf("DeleteGoal() error = %v, want %v", err, domain.ErrGoalNotFound)
		}
	})
	t.Run("round trips every field", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		want := goal("gol_1", "dev", domain.GoalOpen)
		want.ParentID = "gol_0"
		createGoals(t, repo, want)
		got, err := repo.GetGoal(t.Context(), "gol_1")
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("goal (-want +got):\n%s", diff)
		}
	})
	t.Run("saves an edit", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createGoals(t, repo, goal("gol_1", "dev", domain.GoalOpen))
		want := goal("gol_1", "dev", domain.GoalAchieved)
		want.Title = "Shipped"
		if err := repo.SaveGoal(t.Context(), want); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetGoal(t.Context(), "gol_1")
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("goal (-want +got):\n%s", diff)
		}
	})
	t.Run("deletes a goal", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createGoals(t, repo, goal("gol_1", "dev", domain.GoalOpen))
		if err := repo.DeleteGoal(t.Context(), "gol_1"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.GetGoal(t.Context(), "gol_1"); !errors.Is(err, domain.ErrGoalNotFound) {
			t.Fatalf("GetGoal() after delete error = %v, want %v", err, domain.ErrGoalNotFound)
		}
	})
	t.Run("filters goals", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createGoals(t, repo,
			goal("gol_3", "dev", domain.GoalOpen),
			goal("gol_1", "dev", domain.GoalAchieved),
			goal("gol_2", "ops", domain.GoalOpen),
			goal("gol_4", "dev", domain.GoalOpen),
		)
		tests := []struct {
			name   string
			filter domain.GoalFilter
			want   []string
		}{
			{"everything, sorted by id", domain.GoalFilter{}, []string{"gol_1", "gol_2", "gol_3", "gol_4"}},
			{"by owner", domain.GoalFilter{Owner: "dev"}, []string{"gol_1", "gol_3", "gol_4"}},
			{"by status", domain.GoalFilter{Status: domain.GoalOpen}, []string{"gol_2", "gol_3", "gol_4"}},
			{"by owner and status", domain.GoalFilter{Owner: "dev", Status: domain.GoalOpen}, []string{"gol_3", "gol_4"}},
			{"after an id", domain.GoalFilter{AfterID: "gol_2"}, []string{"gol_3", "gol_4"}},
			{"with a limit", domain.GoalFilter{Limit: 2}, []string{"gol_1", "gol_2"}},
			{"no match", domain.GoalFilter{Owner: "ghost"}, []string{}},
		}
		for _, tt := range tests {
			got, err := repo.ListGoals(t.Context(), tt.filter)
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if diff := cmp.Diff(tt.want, goalIDs(got)); diff != "" {
				t.Errorf("%s (-want +got):\n%s", tt.name, diff)
			}
		}
	})
}

func runBudgetContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Run("has no pool before the first push", func(t *testing.T) {
		t.Parallel()
		got, err := newRepo(t).Pool(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got.Known {
			t.Fatalf("Pool() = %+v, want Known false", got)
		}
	})
	t.Run("round trips the pushed pool", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		want := domain.Pool{
			Currency: "USD", TotalMicros: 50_000_000, SpentMicros: 1_250_000,
			Defence:              domain.Defence{Enabled: true, ReservedMicros: 5_000_000, SpentMicros: 10},
			PeriodStart:          time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			PeriodEnd:            time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
			CheckIntervalSeconds: 60, AsOf: At.Add(123 * time.Nanosecond), Known: true,
		}
		for _, p := range []domain.Pool{{Currency: "EUR", Known: true, CheckIntervalSeconds: 1}, want} {
			if err := repo.SavePool(t.Context(), p); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.Pool(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("pool (-want +got):\n%s", diff)
		}
	})
	t.Run("sets and removes allocations", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		six, four, two := int64(600), int64(400), int64(200)
		for _, step := range []struct {
			agent  string
			amount *int64
		}{{"lead", &six}, {"ops", &four}, {"lead", &two}, {"ops", nil}, {"ghost", nil}} {
			if err := repo.SetAllocation(t.Context(), step.agent, step.amount); err != nil {
				t.Fatalf("SetAllocation(%s): %v", step.agent, err)
			}
		}
		got, err := repo.Allocations(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(map[string]int64{"lead": 200}, got); diff != "" {
			t.Fatalf("allocations (-want +got):\n%s", diff)
		}
	})
	t.Run("has no allocations or usage at first", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		alloc, err := repo.Allocations(t.Context())
		if err != nil || len(alloc) != 0 {
			t.Fatalf("Allocations() = %v, %v, want empty", alloc, err)
		}
		usage, err := repo.Usage(t.Context())
		if err != nil || len(usage) != 0 {
			t.Fatalf("Usage() = %v, %v, want empty", usage, err)
		}
	})
	t.Run("adds usage up per agent", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		for _, step := range []struct {
			agent  string
			micros int64
		}{{"dev", 100}, {"ops", 7}, {"dev", 50}} {
			if err := repo.AddUsage(t.Context(), step.agent, step.micros); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.Usage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(map[string]int64{"dev": 150, "ops": 7}, got); diff != "" {
			t.Fatalf("usage (-want +got):\n%s", diff)
		}
	})
	t.Run("does not share allocation maps with the caller", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		one := int64(1)
		if err := repo.SetAllocation(t.Context(), "dev", &one); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Allocations(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		got["dev"] = 99
		again, err := repo.Allocations(t.Context())
		if err != nil || again["dev"] != 1 {
			t.Fatalf("allocation changed through an alias: %v, %v", again, err)
		}
	})
}

func chat(id, agent string, at time.Time) domain.ChatMessage {
	return domain.ChatMessage{ID: id, Agent: agent, Role: domain.ChatHuman, Author: "human:ada", Text: "Hi " + id, CreatedAt: at}
}

func runChatContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Run("round trips every field", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		want := chat("cht_1", "dev", At.Add(7*time.Nanosecond))
		want.Role, want.Author, want.AssignmentID = domain.ChatAgent, "agent:dev", "asg_1"
		if err := repo.AppendChat(t.Context(), want); err != nil {
			t.Fatal(err)
		}
		got, err := repo.ListChat(t.Context(), "dev", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]domain.ChatMessage{want}, got); diff != "" {
			t.Fatalf("chat (-want +got):\n%s", diff)
		}
	})
	t.Run("lists one agent's chat newest first", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		for i, m := range []domain.ChatMessage{
			chat("cht_1", "dev", At), chat("cht_2", "ops", At), chat("cht_3", "dev", At), chat("cht_4", "dev", At),
		} {
			m.CreatedAt = m.CreatedAt.Add(time.Duration(i) * time.Second)
			if err := repo.AppendChat(t.Context(), m); err != nil {
				t.Fatal(err)
			}
		}
		tests := []struct {
			name     string
			beforeID string
			limit    int
			want     []string
		}{
			{"all", "", 10, []string{"cht_4", "cht_3", "cht_1"}},
			{"with a limit", "", 2, []string{"cht_4", "cht_3"}},
			{"before an id", "cht_4", 10, []string{"cht_3", "cht_1"}},
			{"before the oldest", "cht_1", 10, []string{}},
		}
		for _, tt := range tests {
			got, err := repo.ListChat(t.Context(), "dev", tt.beforeID, tt.limit)
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			ids := []string{}
			for _, m := range got {
				ids = append(ids, m.ID)
			}
			if diff := cmp.Diff(tt.want, ids); diff != "" {
				t.Errorf("%s (-want +got):\n%s", tt.name, diff)
			}
		}
	})
}
