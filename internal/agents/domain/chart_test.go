// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func hireSpec(name string) domain.HireSpec {
	return domain.HireSpec{Name: name, DisplayName: "New", Kind: domain.KindChat, Runner: "echo", Role: "Helper"}
}

func TestFounder_IsTheAgentWithoutAManager(t *testing.T) {
	t.Parallel()
	if got, ok := agentstest.Chart().Founder(); !ok || got.Name != "founder" {
		t.Errorf("Founder() = %+v, %v", got, ok)
	}
	if got, ok := domain.NewChart(nil).Founder(); ok {
		t.Errorf("empty chart Founder() = %+v, want none", got)
	}
}

func TestCanManage_AllowsOnlyAgentsBelowTheActor(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name    string
		actor   string
		target  string
		wantErr error
	}{
		{"child", "lead", "dev", nil},
		{"grandchild", "lead", "intern", nil},
		{"self", "dev", "dev", domain.ErrNotBelowActor},
		{"peer", "lead", "ops", domain.ErrNotBelowActor},
		{"ancestor", "dev", "lead", domain.ErrNotBelowActor},
		{"unrelated branch", "dev", "watcher", domain.ErrNotBelowActor},
		{"unknown target", "lead", "ghost", domain.ErrAgentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := chart.CanManage(tt.actor, tt.target)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanManage(%s, %s) error = %v, want %v", tt.actor, tt.target, err, tt.wantErr)
			}
		})
	}
}

func TestCanManage_RefusesARetiredActor(t *testing.T) {
	t.Parallel()
	agents := agentstest.Society()
	agents[1].State = domain.StateRetired // lead
	chart := domain.NewChart(agents)

	err := chart.CanManage("lead", "dev")

	if !errors.Is(err, domain.ErrRetired) {
		t.Fatalf("error = %v, want %v", err, domain.ErrRetired)
	}
}

func TestDomainErrors_MapToCoreSentinels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want error
	}{
		{domain.ErrNotBelowActor, core.ErrForbidden},
		{domain.ErrRootOnly, core.ErrForbidden},
		{domain.ErrAgentNotFound, core.ErrNotFound},
		{domain.ErrNameTaken, core.ErrConflict},
		{domain.ErrRetired, core.ErrConflict},
		{domain.ErrCycle, core.ErrConflict},
		{domain.ErrStaleVersion, core.ErrConflict},
		{domain.ErrOverAllocated, core.ErrConflict},
		{domain.ErrInvalidAgent, core.ErrInvalid},
		{domain.ErrGoalNotFound, core.ErrNotFound},
	}
	for _, tt := range tests {
		if !errors.Is(tt.err, tt.want) {
			t.Errorf("%v does not wrap %v", tt.err, tt.want)
		}
	}
}

func TestHire_RootRule(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name    string
		actor   string
		isRoot  bool
		wantErr error
	}{
		{"root creates root", "founder", true, nil},
		{"non-root creates root", "lead", true, domain.ErrRootOnly},
		{"root creates non-root", "founder", false, nil},
		{"non-root creates non-root", "lead", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := hireSpec("mira")
			spec.IsRoot = tt.isRoot
			_, err := chart.Hire(tt.actor, spec, agentstest.At)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Hire() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHire_PlacesTheAgentBelowTheActorByDefault(t *testing.T) {
	t.Parallel()

	got, err := agentstest.Chart().Hire("lead", hireSpec("mira"), agentstest.At)
	if err != nil {
		t.Fatal(err)
	}

	want := domain.Agent{
		Name: "mira", DisplayName: "New", Kind: domain.KindChat, Runner: "echo", Manager: "lead",
		Role: "Helper", State: domain.StateSleeping, Limits: &domain.DefaultLimits, UID: domain.FirstAgentUID,
		Home: "/home/agents/mira", CreatedAt: agentstest.At, CreatedBy: "agent:lead",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("hired agent (-want +got):\n%s", diff)
	}
}

func TestHire_GivesRootAgentsUIDZeroAndNoLimits(t *testing.T) {
	t.Parallel()
	spec := hireSpec("guard")
	spec.IsRoot = true
	spec.Limits = &domain.Limits{CPUPercent: 100, MemoryMB: 512, Tasks: 64}

	got, err := agentstest.Chart().Hire("founder", spec, agentstest.At)
	if err != nil {
		t.Fatal(err)
	}

	if got.UID != 0 || got.Limits != nil || got.Home != "/root/agents/guard" {
		t.Fatalf("root agent uid=%d limits=%v home=%s, want 0, nil, /root/agents/guard", got.UID, got.Limits, got.Home)
	}
}

func TestHire_ManagerRule(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name    string
		manager string
		wantErr error
	}{
		{"the actor itself", "lead", nil},
		{"child of the actor", "dev", nil},
		{"grandchild of the actor", "intern", nil},
		{"peer of the actor", "ops", domain.ErrNotBelowActor},
		{"manager of the actor", "founder", domain.ErrNotBelowActor},
		{"unrelated branch", "watcher", domain.ErrNotBelowActor},
		{"unknown agent", "ghost", domain.ErrAgentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := hireSpec("mira")
			spec.Manager = tt.manager
			_, err := chart.Hire("lead", spec, agentstest.At)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Hire() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHire_RefusesATakenName(t *testing.T) {
	t.Parallel()

	_, err := agentstest.Chart().Hire("lead", hireSpec("ops"), agentstest.At)

	if !errors.Is(err, domain.ErrNameTaken) {
		t.Fatalf("error = %v, want %v", err, domain.ErrNameTaken)
	}
}

func TestHire_RefusesTheNameOfARetiredAgent(t *testing.T) {
	t.Parallel()
	agents := append(agentstest.Society(), agentstest.New("old", agentstest.Below("lead"), agentstest.WithState(domain.StateRetired)))

	_, err := domain.NewChart(agents).Hire("lead", hireSpec("old"), agentstest.At)

	if !errors.Is(err, domain.ErrNameTaken) {
		t.Fatalf("error = %v, want %v", err, domain.ErrNameTaken)
	}
}

func TestHireSpec_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*domain.HireSpec)
		valid  bool
	}{
		{"valid", func(*domain.HireSpec) {}, true},
		{"name with upper case", func(s *domain.HireSpec) { s.Name = "Mira" }, false},
		{"name starting with a digit", func(s *domain.HireSpec) { s.Name = "1mira" }, false},
		{"name of 31 characters", func(s *domain.HireSpec) { s.Name = "m" + strings.Repeat("a", 30) }, true},
		{"name of 32 characters", func(s *domain.HireSpec) { s.Name = "m" + strings.Repeat("a", 31) }, false},
		{"empty display name", func(s *domain.HireSpec) { s.DisplayName = "" }, false},
		{"display name of 81 characters", func(s *domain.HireSpec) { s.DisplayName = strings.Repeat("é", 81) }, false},
		{"display name of 80 multi-byte characters", func(s *domain.HireSpec) { s.DisplayName = strings.Repeat("é", 80) }, true},
		{"unknown kind", func(s *domain.HireSpec) { s.Kind = "sleepy" }, false},
		{"bad runner", func(s *domain.HireSpec) { s.Runner = "Echo" }, false},
		{"empty role", func(s *domain.HireSpec) { s.Role = "" }, false},
		{"role of 121 characters", func(s *domain.HireSpec) { s.Role = strings.Repeat("r", 121) }, false},
		{"description of 4001 characters", func(s *domain.HireSpec) { s.Description = strings.Repeat("d", 4001) }, false},
		{"cpu at 0", func(s *domain.HireSpec) { s.Limits = &domain.Limits{CPUPercent: 0, MemoryMB: 64, Tasks: 8} }, false},
		{"limits at their minimum", func(s *domain.HireSpec) { s.Limits = &domain.Limits{CPUPercent: 1, MemoryMB: 64, Tasks: 8} }, true},
		{"cpu at 6400", func(s *domain.HireSpec) { s.Limits = &domain.Limits{CPUPercent: 6400, MemoryMB: 64, Tasks: 8} }, true},
		{"cpu at 6401", func(s *domain.HireSpec) { s.Limits = &domain.Limits{CPUPercent: 6401, MemoryMB: 64, Tasks: 8} }, false},
		{"memory at 63", func(s *domain.HireSpec) { s.Limits = &domain.Limits{CPUPercent: 1, MemoryMB: 63, Tasks: 8} }, false},
		{"tasks at 7", func(s *domain.HireSpec) { s.Limits = &domain.Limits{CPUPercent: 1, MemoryMB: 64, Tasks: 7} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := hireSpec("mira")
			tt.change(&spec)
			_, err := agentstest.Chart().Hire("founder", spec, agentstest.At)
			if tt.valid && err != nil {
				t.Fatalf("Hire() error = %v, want nil", err)
			}
			if !tt.valid && !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Hire() error = %v, want %v", err, domain.ErrInvalidAgent)
			}
		})
	}
}

func TestTeam_IsTheBranchBelowTheFounder(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := map[string]string{"founder": "founder", "lead": "lead", "dev": "lead", "intern": "lead", "watcher": "ops"}
	for name, want := range tests {
		if got := chart.Team(name); got != want {
			t.Errorf("Team(%s) = %q, want %q", name, got, want)
		}
	}
}

func TestIsBelow_IsStrict(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name, ancestor string
		want           bool
	}{
		{"intern", "founder", true},
		{"intern", "dev", true},
		{"dev", "dev", false},
		{"lead", "dev", false},
		{"watcher", "lead", false},
		{"ghost", "founder", false},
	}
	for _, tt := range tests {
		if got := chart.IsBelow(tt.name, tt.ancestor); got != tt.want {
			t.Errorf("IsBelow(%s, %s) = %v, want %v", tt.name, tt.ancestor, got, tt.want)
		}
	}
}

func TestRetire_MovesDirectReportsUpToTheActor(t *testing.T) {
	t.Parallel()

	plan, err := agentstest.Chart().Retire("lead", "dev")
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(domain.RetirePlan{Agent: "dev", NewManager: "lead", Moved: []string{"intern"}}, plan); diff != "" {
		t.Fatalf("plan (-want +got):\n%s", diff)
	}
}

func TestRetire_Rule(t *testing.T) {
	t.Parallel()
	agents := append(agentstest.Society(), agentstest.New("old", agentstest.Below("lead"), agentstest.WithState(domain.StateRetired)))
	chart := domain.NewChart(agents)
	tests := []struct {
		name, actor, target string
		wantErr             error
	}{
		{"child", "lead", "dev", nil},
		{"grandchild", "lead", "intern", nil},
		{"self", "lead", "lead", domain.ErrNotBelowActor},
		{"peer", "lead", "ops", domain.ErrNotBelowActor},
		{"ancestor", "dev", "lead", domain.ErrNotBelowActor},
		{"unrelated branch", "dev", "watcher", domain.ErrNotBelowActor},
		{"the founder", "lead", "founder", domain.ErrNotBelowActor},
		{"already retired", "lead", "old", domain.ErrRetired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := chart.Retire(tt.actor, tt.target)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Retire(%s, %s) error = %v, want %v", tt.actor, tt.target, err, tt.wantErr)
			}
		})
	}
}

func TestMove_Rule(t *testing.T) {
	t.Parallel()
	chart := agentstest.Chart()
	tests := []struct {
		name, actor, agent, newManager string
		wantErr                        error
	}{
		{"under the actor itself", "founder", "intern", "founder", nil},
		{"under another agent below the actor", "founder", "intern", "watcher", nil},
		{"agent not below the actor", "lead", "watcher", "dev", domain.ErrNotBelowActor},
		{"new manager not below the actor", "lead", "intern", "ops", domain.ErrNotBelowActor},
		{"the actor itself", "lead", "lead", "dev", domain.ErrNotBelowActor},
		{"under itself", "founder", "dev", "dev", domain.ErrCycle},
		{"under its own report", "founder", "dev", "intern", domain.ErrCycle},
		{"unknown new manager", "founder", "dev", "ghost", domain.ErrAgentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := chart.CanMove(tt.actor, tt.agent, tt.newManager)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanMove() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTree_ListsActiveAgentsParentsFirst(t *testing.T) {
	t.Parallel()
	agents := append(agentstest.Society(), agentstest.New("old", agentstest.Below("lead"), agentstest.WithState(domain.StateRetired)))

	tree := domain.NewChart(agents).Tree()

	var names []string
	var walk func(nodes []domain.Node)
	walk = func(nodes []domain.Node) {
		for _, n := range nodes {
			names = append(names, n.Agent.Name)
			walk(n.Children)
		}
	}
	walk(tree)
	if diff := cmp.Diff([]string{"founder", "lead", "dev", "intern", "ops", "watcher"}, names); diff != "" {
		t.Fatalf("tree order (-want +got):\n%s", diff)
	}
}
