// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/adapters/memory"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

type env struct {
	fakes   *coretest.Fakes
	repo    *memory.Repository
	rt      *agentstest.Runtime
	society *app.Society
}

func newEnv(t *testing.T) env {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	repo := memory.New()
	for _, a := range agentstest.Society() {
		if err := repo.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	rt := agentstest.NewRuntime()
	return env{fakes: fakes, repo: repo, rt: rt, society: app.New(repo, rt, ports, fakes.Clock, fakes.IDs)}
}

func asAgent(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: name, UID: 1100})
}

func asHuman(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: name, UID: 1000})
}

func spec(name string) domain.HireSpec {
	return domain.HireSpec{Name: name, DisplayName: "Mira", Kind: domain.KindChat, Runner: "echo", Role: "Reviewer"}
}

func (e env) eventTypes() []string {
	var types []string
	for _, ev := range e.fakes.Events.Published() {
		types = append(types, ev.Type)
	}
	return types
}

func (e env) auditActions() []string {
	var actions []string
	for _, a := range e.fakes.Audit.Entries() {
		actions = append(actions, a.Action)
	}
	return actions
}

func (e env) assertNothingHappened(t *testing.T) {
	t.Helper()
	if got := e.eventTypes(); len(got) != 0 {
		t.Errorf("published %v, want nothing", got)
	}
	if got := e.auditActions(); len(got) != 0 {
		t.Errorf("audited %v, want nothing", got)
	}
}

func TestHire_StoresProvisionsPublishesAndAudits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	got, err := e.society.Hire(asAgent("lead"), spec("mira"))
	if err != nil {
		t.Fatal(err)
	}

	stored, err := e.repo.GetAgent(context.Background(), "mira")
	if err != nil || stored.Manager != "lead" || stored.UID != got.UID || got.UID < 1000 {
		t.Fatalf("stored %+v %v, returned uid %d", stored, err, got.UID)
	}
	if diff := cmp.Diff([]string{"mira"}, e.rt.Provisioned()); diff != "" {
		t.Errorf("provisioned (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"agentsone.agents.hired"}, e.eventTypes()); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	ev := e.fakes.Events.Published()[0]
	if ev.Actor != "agent:lead" || ev.ChartScope != "team:lead" || ev.Source != "/agents/lead" {
		t.Errorf("event actor %q scope %q source %q", ev.Actor, ev.ChartScope, ev.Source)
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil || data["agent"] != "mira" || data["manager"] != "lead" || data["isRoot"] != false {
		t.Errorf("event data %s", ev.Data)
	}
	rec := e.fakes.Audit.Entries()[0]
	if rec.Action != "agent.hire" || rec.Target != "agent:mira" || rec.Actor != "agent:lead" {
		t.Errorf("audit %+v", rec)
	}
}

func TestHire_RefusalsChangeNothing(t *testing.T) {
	t.Parallel()
	root := spec("guard")
	root.IsRoot = true
	above := spec("mira")
	above.Manager = "founder"
	tests := []struct {
		name    string
		ctx     context.Context
		spec    domain.HireSpec
		wantErr error
	}{
		{"custom agent hires a root agent", asAgent("lead"), root, domain.ErrRootOnly},
		{"hire above self", asAgent("lead"), above, domain.ErrNotBelowActor},
		{"taken name", asAgent("lead"), spec("ops"), domain.ErrNameTaken},
		{"human hires", asHuman("ada"), spec("mira"), core.ErrForbidden},
		{"no caller", context.Background(), spec("mira"), core.ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			_, err := e.society.Hire(tt.ctx, tt.spec)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Hire() error = %v, want %v", err, tt.wantErr)
			}
			if _, err := e.repo.GetAgent(context.Background(), tt.spec.Name); tt.spec.Name != "ops" && !errors.Is(err, domain.ErrAgentNotFound) {
				t.Errorf("agent %s was stored", tt.spec.Name)
			}
			if got := e.rt.Provisioned(); len(got) != 0 {
				t.Errorf("provisioned %v", got)
			}
			e.assertNothingHappened(t)
		})
	}
}

func TestHire_UndoesProvisioningWhenTheStoreFails(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.rt.FailProvision = nil
	boom := errors.New("disk full")
	e.repo.FailNext(boom)

	_, err := e.society.Hire(asAgent("lead"), spec("mira"))

	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
	if diff := cmp.Diff([]string{"mira"}, e.rt.Retired()); diff != "" {
		t.Errorf("retired after failed store (-want +got):\n%s", diff)
	}
	e.assertNothingHappened(t)
}

func TestHire_RefusesWhenProvisioningFails(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.rt.FailProvision = errors.New("useradd failed")

	_, err := e.society.Hire(asAgent("lead"), spec("mira"))

	if !errors.Is(err, e.rt.FailProvision) {
		t.Fatalf("error = %v", err)
	}
	if _, err := e.repo.GetAgent(context.Background(), "mira"); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Error("agent stored after failed provisioning")
	}
	e.assertNothingHappened(t)
}

func TestFound_CreatesTheFounderOnce(t *testing.T) {
	t.Parallel()
	fakes, ports := coretest.NewFakes()
	repo := memory.New()
	s := app.New(repo, agentstest.NewRuntime(), ports, fakes.Clock, fakes.IDs)
	sys := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "seed", UID: 0})
	founder := spec("founder")
	founder.Kind = domain.KindAlwaysOn

	got, err := s.Found(sys, founder)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsRoot || got.CreatedBy != "system:seed" {
		t.Fatalf("founder %+v", got)
	}
	if _, err := s.Found(sys, founder); !errors.Is(err, domain.ErrNameTaken) {
		t.Fatalf("second Found error = %v, want %v", err, domain.ErrNameTaken)
	}
	if _, err := s.Found(asAgent("x"), spec("y")); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("Found by an agent error = %v, want %v", err, core.ErrForbidden)
	}
}

func TestUpdate_EditsBelowAndRestartsTheUnit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	runner := "openai"

	got, err := e.society.Update(asAgent("lead"), "intern", domain.Update{Runner: &runner})
	if err != nil {
		t.Fatal(err)
	}

	if got.Runner != "openai" {
		t.Fatalf("runner = %s", got.Runner)
	}
	if diff := cmp.Diff([]string{"intern"}, e.rt.Reconfigured()); diff != "" {
		t.Errorf("reconfigured (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"agentsone.agents.edited"}, e.eventTypes()); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"agent.edit"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestUpdate_DoesNotRestartForAWordingEdit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	role := "Reviewer"

	if _, err := e.society.Update(asAgent("lead"), "intern", domain.Update{Role: &role}); err != nil {
		t.Fatal(err)
	}

	if got := e.rt.Reconfigured(); len(got) != 0 {
		t.Fatalf("reconfigured %v for a role edit", got)
	}
}

func TestUpdate_RefusesAboveSelf(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	role := "Boss"

	_, err := e.society.Update(asAgent("dev"), "lead", domain.Update{Role: &role})

	if !errors.Is(err, domain.ErrNotBelowActor) {
		t.Fatalf("error = %v, want %v", err, domain.ErrNotBelowActor)
	}
	if a, _ := e.repo.GetAgent(context.Background(), "lead"); a.Role == "Boss" {
		t.Error("lead was edited")
	}
	e.assertNothingHappened(t)
}

func TestRetire_StopsTheRunnerAndMovesReportsUp(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	if err := e.society.Retire(asAgent("lead"), "dev"); err != nil {
		t.Fatal(err)
	}

	dev, _ := e.repo.GetAgent(context.Background(), "dev")
	intern, _ := e.repo.GetAgent(context.Background(), "intern")
	if dev.State != domain.StateRetired || intern.Manager != "lead" {
		t.Fatalf("dev state %s, intern manager %s", dev.State, intern.Manager)
	}
	if diff := cmp.Diff([]string{"dev"}, e.rt.Retired()); diff != "" {
		t.Errorf("runtime retired (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"agentsone.agents.retired"}, e.eventTypes()); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"agent.retire"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestRetire_RefusesAnAncestor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	err := e.society.Retire(asAgent("dev"), "lead")

	if !errors.Is(err, domain.ErrNotBelowActor) {
		t.Fatalf("error = %v, want %v", err, domain.ErrNotBelowActor)
	}
	if got := e.rt.Retired(); len(got) != 0 {
		t.Errorf("runtime retired %v", got)
	}
	e.assertNothingHappened(t)
}

func TestMove_MovesTheBranch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	if err := e.society.Move(asAgent("founder"), "dev", "ops"); err != nil {
		t.Fatal(err)
	}

	dev, _ := e.repo.GetAgent(context.Background(), "dev")
	if dev.Manager != "ops" {
		t.Fatalf("dev manager = %s, want ops", dev.Manager)
	}
	if diff := cmp.Diff([]string{"agent.move"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestMove_RefusesACycle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	err := e.society.Move(asAgent("founder"), "lead", "intern")

	if !errors.Is(err, domain.ErrCycle) {
		t.Fatalf("error = %v, want %v", err, domain.ErrCycle)
	}
	e.assertNothingHappened(t)
}

func TestReads_AreOpenToHumans(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	list, err := e.society.List(asHuman("ada"), app.ListFilter{Team: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range list {
		names = append(names, a.Name)
	}
	if diff := cmp.Diff([]string{"dev", "intern", "lead"}, names); diff != "" {
		t.Errorf("team lead (-want +got):\n%s", diff)
	}
	if _, err := e.society.Chart(asHuman("ada")); err != nil {
		t.Errorf("Chart() error = %v", err)
	}
	if _, err := e.society.Get(asHuman("ada"), "ghost"); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Errorf("Get(ghost) error = %v", err)
	}
}

func TestList_Filters(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.society.Retire(asAgent("lead"), "intern"); err != nil {
		t.Fatal(err)
	}
	yes := true
	tests := []struct {
		name   string
		filter app.ListFilter
		want   []string
	}{
		{"active by default", app.ListFilter{Manager: "dev"}, nil},
		{"retired on request", app.ListFilter{State: domain.StateRetired}, []string{"intern"}},
		{"by kind", app.ListFilter{Kind: domain.KindWaker}, []string{"watcher"}},
		{"root only", app.ListFilter{IsRoot: &yes}, []string{"founder"}},
		{"direct reports", app.ListFilter{Manager: "founder"}, []string{"lead", "ops"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, err := e.society.List(asHuman("ada"), tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, a := range list {
				names = append(names, a.Name)
			}
			if diff := cmp.Diff(tt.want, names); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestDirectory_ReadsTheChart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	d := e.society.Directory()

	below, err := d.IsBelow(ctx, "intern", "lead")
	if err != nil || !below {
		t.Fatalf("IsBelow = %v %v", below, err)
	}
	if team, _ := d.TeamRoot(ctx, "intern"); team != "lead" {
		t.Errorf("TeamRoot(intern) = %s, want lead", team)
	}
	if m, _ := d.Manager(ctx, "dev"); m != "lead" {
		t.Errorf("Manager(dev) = %s", m)
	}
	if a, _ := d.GetAgent(ctx, "founder"); !a.IsRoot {
		t.Errorf("GetAgent(founder) = %+v", a)
	}
	if all, _ := d.ListAgents(ctx); len(all) != 6 {
		t.Errorf("ListAgents = %d agents, want 6", len(all))
	}
	if _, err := d.Manager(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Manager(ghost) error = %v", err)
	}
}
