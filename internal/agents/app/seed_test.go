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

const seedCharter = "We work for the humans of this machine."

var seedPlan = app.SeedPlan{
	Founder: app.SeedAgent{Template: "small-seed/founder", DisplayName: "Founder", Role: "Leads the society"},
	Team: []app.SeedAgent{
		{Template: "small-seed/router", DisplayName: "Router", Role: "Sends each request to the right agent"},
		{Template: "small-seed/waker", DisplayName: "Waker", Role: "Wakes agents on schedules and triggers"},
	},
}

type seedEnv struct {
	fakes   *coretest.Fakes
	repo    *memory.Repository
	society *app.Society
}

// newSeedEnv is an empty society with the small seed's templates installed.
func newSeedEnv(t *testing.T) seedEnv {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	for _, tpl := range []struct {
		name, kind string
		root       bool
		extra      map[string]any
	}{
		{"founder", "always-on", true, map[string]any{"charter": seedCharter, "oneLine": "Goal: a useful machine.", "goals": []string{"Learn what humans need"}}},
		{"router", "chat", false, nil},
		{"waker", "waker", false, nil},
	} {
		doc := map[string]any{"name": tpl.name, "kind": tpl.kind, "isRoot": tpl.root}
		for k, v := range tpl.extra {
			doc[k] = v
		}
		raw, _ := json.Marshal(doc)
		fakes.Templates.Add(core.AgentTemplate{
			ID: "small-seed/" + tpl.name, Name: tpl.name, Kind: core.AgentKind(tpl.kind), IsRoot: tpl.root, Runner: "openai", Raw: raw,
		})
	}
	repo := memory.New()
	return seedEnv{fakes: fakes, repo: repo, society: app.New(repo, agentstest.NewRuntime(), ports, fakes.Clock, fakes.IDs)}
}

func asSystem() context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "seed", UID: 0, IsRoot: true})
}

func (e seedEnv) actions() []string {
	var out []string
	for _, a := range e.fakes.Audit.Entries() {
		out = append(out, a.Actor+" "+a.Action+" "+a.Target)
	}
	return out
}

func TestSeed_FoundsTheSocietyWithItsTeamAndCharter(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)

	created, err := e.society.Seed(asSystem(), seedPlan)

	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if diff := cmp.Diff([]string{"founder", "router", "waker"}, created); diff != "" {
		t.Errorf("created (-want +got):\n%s", diff)
	}
	founder, _ := e.society.Get(context.Background(), "founder")
	if !founder.IsRoot || founder.Manager != "" || founder.Kind != domain.KindAlwaysOn || founder.Runner != "openai" ||
		founder.Template != "small-seed/founder" || founder.Role != "Leads the society" || founder.DisplayName != "Founder" {
		t.Errorf("founder = %+v", founder)
	}
	for name, kind := range map[string]domain.Kind{"router": domain.KindChat, "waker": domain.KindWaker} {
		a, err := e.society.Get(context.Background(), name)
		if err != nil || a.Manager != "founder" || a.IsRoot || a.Kind != kind || a.Template != "small-seed/"+name || a.Role == "" {
			t.Errorf("%s = %+v, %v", name, a, err)
		}
	}
	charter, _ := e.society.Charter(context.Background())
	if charter.Version != 1 || charter.Text != seedCharter || charter.UpdatedBy != "agent:founder" {
		t.Errorf("charter = %+v", charter)
	}
	line, err := e.society.OneLine(context.Background(), "founder")
	if err != nil || line.Line != "Goal: a useful machine." {
		t.Errorf("founder one-line = %+v, %v", line, err)
	}
	goals, _ := e.society.Goals(context.Background(), domain.GoalFilter{Owner: "founder"})
	if len(goals) != 1 || goals[0].Title != "Learn what humans need" {
		t.Errorf("founder goals = %+v", goals)
	}
	want := []string{
		"system:seed agent.found agent:founder",
		"system:seed agent.hire agent:router",
		"system:seed agent.hire agent:waker",
		"system:seed charter.rewrite charter:society",
	}
	if diff := cmp.Diff(want, e.actions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestSeed_TheFounderCanHire(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)
	if _, err := e.society.Seed(asSystem(), seedPlan); err != nil {
		t.Fatal(err)
	}

	got, err := e.society.Hire(asAgent("founder"), spec("scribe"))

	if err != nil || got.Manager != "founder" {
		t.Fatalf("Hire() = %+v, %v", got, err)
	}
	if team, _ := e.society.Hire(asAgent("router"), spec("helper")); team.Manager != "router" {
		t.Errorf("router hire = %+v, want below the router", team)
	}
}

func TestSeedInstalled_OnceTheWholeSeedWasCreated(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)
	status := e.society.SeedStatus(seedPlan)
	ctx := context.Background()
	if got, err := status.SeedInstalled(ctx); err != nil || got {
		t.Fatalf("before the seed = %v, %v, want false", got, err)
	}
	half := seedPlan
	half.Team = nil
	if _, err := e.society.Seed(asSystem(), half); err != nil {
		t.Fatal(err)
	}
	if got, _ := status.SeedInstalled(ctx); got {
		t.Fatal("installed with only the founder, want false")
	}

	if _, err := e.society.Seed(asSystem(), seedPlan); err != nil {
		t.Fatal(err)
	}
	if got, err := status.SeedInstalled(ctx); err != nil || !got {
		t.Fatalf("after the seed = %v, %v, want true", got, err)
	}
	if err := e.society.Retire(asAgent("founder"), "router"); err != nil {
		t.Fatal(err)
	}
	if got, _ := status.SeedInstalled(ctx); !got {
		t.Error("retiring the router undid the seed, want still installed")
	}
}

func TestSeed_TwiceChangesNothing(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)
	if _, err := e.society.Seed(asSystem(), seedPlan); err != nil {
		t.Fatal(err)
	}
	audited, published := len(e.fakes.Audit.Entries()), len(e.fakes.Events.Published())

	created, err := e.society.Seed(asSystem(), seedPlan)

	if err != nil || len(created) != 0 {
		t.Fatalf("second Seed() = %v, %v, want nothing created", created, err)
	}
	if len(e.fakes.Audit.Entries()) != audited || len(e.fakes.Events.Published()) != published {
		t.Errorf("second Seed() audited or published")
	}
}

func TestSeed_FinishesAHalfDoneSeedAndKeepsWhatAgentsChanged(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)
	founder := agentstest.New("founder", agentstest.Root())
	router := agentstest.New("router", agentstest.Below("founder"), agentstest.WithState(domain.StateRetired))
	for _, a := range []domain.Agent{founder, router} {
		if err := e.repo.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.society.RewriteCharter(asAgent("founder"), 0, "Our own words."); err != nil {
		t.Fatal(err)
	}
	before := len(e.fakes.Audit.Entries())

	created, err := e.society.Seed(asSystem(), seedPlan)

	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if diff := cmp.Diff([]string{"waker"}, created); diff != "" {
		t.Errorf("created (-want +got):\n%s", diff)
	}
	if r, _ := e.society.Get(context.Background(), "router"); r.State != domain.StateRetired {
		t.Errorf("router = %+v, want it left retired", r)
	}
	if c, _ := e.society.Charter(context.Background()); c.Text != "Our own words." || c.Version != 1 {
		t.Errorf("charter = %+v, want the agents' own", c)
	}
	if got := e.actions()[before:]; len(got) != 1 || got[0] != "system:seed agent.hire agent:waker" {
		t.Errorf("audit = %v", got)
	}
}

func TestSeedOnce_AppliesTheSeedAndRecordsIt(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)

	created, applied, err := e.society.SeedOnce(asSystem(), seedPlan)

	if err != nil || !applied {
		t.Fatalf("SeedOnce() applied = %v, error = %v, want applied", applied, err)
	}
	if diff := cmp.Diff([]string{"founder", "router", "waker"}, created); diff != "" {
		t.Errorf("created (-want +got):\n%s", diff)
	}
	if ok, _ := e.repo.SeedApplied(context.Background()); !ok {
		t.Error("the seed was not recorded")
	}
}

// Once applied, the seed never runs again on this machine, even when the
// plan could no longer be met: humans may retire the society or remove the
// small-seed plugin on purpose.
func TestSeedOnce_DoesNothingOnceTheSeedWasApplied(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)
	if _, _, err := e.society.SeedOnce(asSystem(), seedPlan); err != nil {
		t.Fatal(err)
	}
	audited := len(e.fakes.Audit.Entries())
	gone := app.SeedPlan{Founder: app.SeedAgent{Template: "small-seed/ghost", DisplayName: "G", Role: "G"}}

	created, applied, err := e.society.SeedOnce(asSystem(), gone)

	if err != nil || applied || len(created) != 0 {
		t.Fatalf("second SeedOnce() = %v, %v, %v, want nothing done", created, applied, err)
	}
	if got := len(e.fakes.Audit.Entries()); got != audited {
		t.Errorf("second SeedOnce() audited %d entries", got-audited)
	}
}

// A seed cut short is not recorded, so the next try finishes it without
// creating anyone twice.
func TestSeedOnce_FinishesASeedCutShort(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)
	short := seedPlan
	short.Team = append([]app.SeedAgent{seedPlan.Team[0]}, app.SeedAgent{Template: "small-seed/ghost", DisplayName: "G", Role: "G"})
	if _, applied, err := e.society.SeedOnce(asSystem(), short); err == nil || applied {
		t.Fatalf("SeedOnce() with a missing template = applied %v, error %v, want a failure", applied, err)
	}
	if ok, _ := e.repo.SeedApplied(context.Background()); ok {
		t.Fatal("a failed seed was recorded")
	}

	created, applied, err := e.society.SeedOnce(asSystem(), seedPlan)

	if err != nil || !applied {
		t.Fatalf("SeedOnce() = applied %v, error %v", applied, err)
	}
	if diff := cmp.Diff([]string{"waker"}, created); diff != "" {
		t.Errorf("created (-want +got):\n%s", diff)
	}
}

func TestSeedOnce_RefusesAgentsAndHumans(t *testing.T) {
	t.Parallel()
	for _, ctx := range []context.Context{asAgent("founder"), asHuman("ada")} {
		e := newSeedEnv(t)
		if _, _, err := e.society.SeedOnce(ctx, seedPlan); !errors.Is(err, core.ErrForbidden) {
			t.Errorf("SeedOnce() error = %v, want ErrForbidden", err)
		}
	}
}

func TestSeed_Refusals(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		ctx  context.Context
		plan app.SeedPlan
		want error
	}{
		"an agent":           {asAgent("founder"), seedPlan, core.ErrForbidden},
		"a human":            {asHuman("ada"), seedPlan, core.ErrForbidden},
		"no founder plan":    {asSystem(), app.SeedPlan{}, domain.ErrInvalidAgent},
		"a missing template": {asSystem(), app.SeedPlan{Founder: app.SeedAgent{Template: "small-seed/ghost", DisplayName: "G", Role: "G"}}, domain.ErrInvalidAgent},
	}
	for name, tt := range tests {
		e := newSeedEnv(t)
		if _, err := e.society.Seed(tt.ctx, tt.plan); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.want)
		}
		if got := e.actions(); len(got) != 0 {
			t.Errorf("%s: audited %v", name, got)
		}
	}
}

func TestSeed_ATeamTemplateThatIsMissingStopsAfterTheFounder(t *testing.T) {
	t.Parallel()
	e := newSeedEnv(t)
	plan := seedPlan
	plan.Team = []app.SeedAgent{{Template: "small-seed/ghost", DisplayName: "Ghost", Role: "Haunts"}}

	_, err := e.society.Seed(asSystem(), plan)

	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("Seed() error = %v, want ErrInvalidAgent", err)
	}
	if _, err := e.society.Get(context.Background(), "founder"); err != nil {
		t.Errorf("founder: %v, want it kept so a later seed finishes the work", err)
	}
}
