// SPDX-License-Identifier: MIT

package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModule_OwnsItsTags(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps)
	if m.Name() != "agents" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "agents", "chart", "charter", "budget")
}

func TestModule_ProvidesItsPorts(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	p := core.NewPorts()
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	m.Provide(p)

	if _, err := p.Agents.ListAgents(ctx); err != nil {
		t.Errorf("Agents port: %v", err)
	}
	if _, err := p.Freezer.State(ctx); err != nil {
		t.Errorf("Freezer port: %v", err)
	}
	if _, err := p.Budget.Allocation(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Budget port: %v, want not found", err)
	}
	if err := p.Waker.Wake(ctx, "ghost", core.WakeEvent, nil); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Waker port: %v, want not found", err)
	}
	if _, err := p.Profiles.Profiles(ctx); err != nil {
		t.Errorf("Profiles port: %v", err)
	}
	human := core.WithCaller(ctx, core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1})
	if _, err := p.Chat.PostChat(human, "ghost", "hi", ""); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Chat port: %v, want not found", err)
	}
}

func TestModule_StartReprovisionsEveryActiveAgentWithItsUID(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	rt := agentstest.NewRuntime()
	m := agents.New(deps, agents.WithRuntime(rt))
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "seed"})
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	founder := domain.HireSpec{Name: "founder", DisplayName: "Founder", Kind: domain.KindAlwaysOn, Runner: "echo", Role: "Founder"}
	if _, err := m.Society().Found(ctx, founder); err != nil {
		t.Fatal(err)
	}
	if err := m.Society().Freezer().Freeze(ctx, core.FreezeScope{Kind: core.FreezeAgent, Target: "founder"}); err != nil {
		t.Fatal(err)
	}
	restarted := agentstest.NewRuntime()
	again := agents.New(deps, agents.WithRuntime(restarted))

	if err := again.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"founder"}, restarted.Provisioned()); diff != "" {
		t.Errorf("reprovisioned (-want +got):\n%s", diff)
	}
	if !restarted.Frozen("agent:founder") {
		t.Error("freeze was not replayed on start")
	}
}

// At start, and once the society is seeded, always-on agents' runners
// start idle; chat agents and wakers wait for a wake.
func TestModule_StartsAlwaysOnRunnersAtStartAndAfterTheSeed(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	rt := agentstest.NewRuntime()
	m := agents.New(deps, agents.WithRuntime(rt))
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	firstHuman(t, fakes)
	waitSeed(t, m)
	if diff := cmp.Diff([]string{"founder"}, rt.Ran()); diff != "" {
		t.Errorf("after the seed (-want +got):\n%s", diff)
	}

	restarted := agentstest.NewRuntime()
	again := agents.New(deps, agents.WithRuntime(restarted))
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Stop(ctx) })
	if diff := cmp.Diff([]string{"founder"}, restarted.Ran()); diff != "" {
		t.Errorf("after a restart (-want +got):\n%s", diff)
	}
	if len(restarted.Wakes()) != 0 {
		t.Errorf("wakes = %v, want none", restarted.Wakes())
	}
}

// addSmallSeed installs the repository's Small Seed templates in the fake
// catalog, as the plugins module serves them.
func addSmallSeed(t *testing.T, fakes *coretest.Fakes) {
	t.Helper()
	for _, name := range []string{"founder", "router", "waker"} {
		raw, err := os.ReadFile(filepath.Join("../../plugins/small-seed/agents", name+".json")) // #nosec G304 -- the repository's own plugin
		if err != nil {
			t.Fatal(err)
		}
		var head struct {
			Name   string         `json:"name"`
			Kind   core.AgentKind `json:"kind"`
			IsRoot bool           `json:"isRoot"`
			Runner struct {
				Name string `json:"name"`
			} `json:"runner"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			t.Fatal(err)
		}
		fakes.Templates.Add(core.AgentTemplate{
			ID: "small-seed/" + head.Name, Name: head.Name, Kind: head.Kind, IsRoot: head.IsRoot, Runner: head.Runner.Name, Raw: raw,
		})
	}
}

func firstHuman(t *testing.T, fakes *coretest.Fakes) {
	t.Helper()
	_, err := fakes.Events.Publish(context.Background(), core.Event{
		Type: "agentsone.setup.first-human.created", Source: "/daemon", Actor: "system:auth", ChartScope: "machine",
		Data: json.RawMessage(`{"human":"ada"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func waitSeed(t *testing.T, m *agents.Module) {
	t.Helper()
	select {
	case <-m.SeedDone():
	case <-time.After(5 * time.Second):
		t.Fatal("the seed did not finish")
	}
}

func TestModule_SeedsTheSmallSeedOnceTheFirstHumanExists(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	m := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.Society().List(ctx, app.ListFilter{}); len(list) != 0 {
		t.Fatalf("agents before the first human = %+v", list)
	}

	firstHuman(t, fakes)
	waitSeed(t, m)

	list, err := m.Society().List(ctx, app.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range list {
		names = append(names, a.Name+"/"+a.Manager)
	}
	if diff := cmp.Diff([]string{"founder/", "router/founder", "waker/founder"}, names); diff != "" {
		t.Errorf("agents (-want +got):\n%s", diff)
	}
	if c, _ := m.Society().Charter(ctx); c.Version != 1 || !strings.Contains(c.Text, "humans") {
		t.Errorf("charter = %+v", c)
	}
	ports := core.NewPorts()
	m.Provide(ports)
	if installed, err := ports.Seed.SeedInstalled(ctx); err != nil || !installed {
		t.Errorf("Seed port = %v, %v, want installed", installed, err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	audited := len(fakes.Audit.Entries())

	again := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitSeed(t, again) // the stored event replays; the seed finds nothing to do
	if err := again.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if got := len(fakes.Audit.Entries()); got != audited {
		t.Errorf("restart audited %d more entries, want none", got-audited)
	}
}

// seedLog records the messages the module logs.
type seedLog struct {
	mu       sync.Mutex
	messages []string
}

func (l *seedLog) Enabled(context.Context, slog.Level) bool { return true }
func (l *seedLog) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *seedLog) WithGroup(string) slog.Handler            { return l }
func (l *seedLog) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, r.Level.String()+" "+r.Message)
	return nil
}

func (l *seedLog) count(message string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.messages {
		if m == message {
			n++
		}
	}
	return n
}

// problems returns the warnings and errors logged.
func (l *seedLog) problems() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, m := range l.messages {
		if strings.HasPrefix(m, "ERROR ") || strings.HasPrefix(m, "WARN ") {
			out = append(out, m)
		}
	}
	return out
}

func startModule(t *testing.T, deps core.Deps) *agents.Module {
	t.Helper()
	m := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitSeed(t, m)
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func agentNames(t *testing.T, m *agents.Module) []string {
	t.Helper()
	list, err := m.Society().List(context.Background(), app.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range list {
		names = append(names, a.Name)
	}
	return names
}

// A machine whose first human came before the seed listened, or whose
// first-human event never reached it, still gets its society at the next
// start.
func TestModule_SeedsAtStartWhenAHumanExistsAndTheSeedNeverRan(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	fakes.Humans.Add("ada", 1000)
	logs := &seedLog{}
	deps.Log = slog.New(logs)

	m := startModule(t, deps)

	if diff := cmp.Diff([]string{"founder", "router", "waker"}, agentNames(t, m)); diff != "" {
		t.Errorf("agents (-want +got):\n%s", diff)
	}
	if got := logs.count("INFO agents: seeded the society"); got != 1 {
		t.Errorf("logged the seed %d times, want once", got)
	}
}

// A society seeded at start, without the first-human event, gets its
// always-on runners too.
func TestModule_StartsAlwaysOnRunnersAfterTheSeedAtStart(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	fakes.Humans.Add("ada", 1000)
	rt := agentstest.NewRuntime()
	m := agents.New(deps, agents.WithRuntime(rt))
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitSeed(t, m)
	coretest.Check(t, func() error { return m.Stop(ctx) })

	if diff := cmp.Diff([]string{"founder"}, rt.Ran()); diff != "" {
		t.Errorf("runners started (-want +got):\n%s", diff)
	}
}

// The seed runs once per machine: later starts leave the society as the
// humans and agents made it, and log nothing, even when the small-seed
// templates are gone.
func TestModule_SeedsOncePerMachine(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	fakes.Humans.Add("ada", 1000)
	logs := &seedLog{}
	deps.Log = slog.New(logs)
	startModule(t, deps)
	audited := len(fakes.Audit.Entries())
	withoutTemplates := *deps.Ports
	withoutTemplates.Templates = coretest.NewTemplates()
	deps.Ports = &withoutTemplates

	startModule(t, deps)

	if got := len(fakes.Audit.Entries()); got != audited {
		t.Errorf("restart audited %d more entries, want none", got-audited)
	}
	if got := logs.count("INFO agents: seeded the society"); got != 1 {
		t.Errorf("logged the seed %d times, want once", got)
	}
	if got := logs.problems(); len(got) != 0 {
		t.Errorf("restart logged problems %v", got)
	}
}

// With a human and the first-human event both there, the society is
// seeded once.
func TestModule_SeedsOnceWhenTheHumanAndTheEventAreBothThere(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	fakes.Humans.Add("ada", 1000)
	firstHuman(t, fakes)
	logs := &seedLog{}
	deps.Log = slog.New(logs)

	m := startModule(t, deps)

	if diff := cmp.Diff([]string{"founder", "router", "waker"}, agentNames(t, m)); diff != "" {
		t.Errorf("agents (-want +got):\n%s", diff)
	}
	if got := logs.count("INFO agents: seeded the society"); got != 1 {
		t.Errorf("logged the seed %d times, want once", got)
	}
}

// Without a human the seed waits for the first-human event and creates
// nobody.
func TestModule_WithoutAHumanTheSeedWaits(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	m := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.SeedDone():
		t.Fatal("the seed ended without a human")
	case <-time.After(50 * time.Millisecond):
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitSeed(t, m)
	if got := agentNames(t, m); len(got) != 0 {
		t.Errorf("agents = %v, want none", got)
	}
}

func TestModule_StopEndsAWaitingSeed(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitSeed(t, m)
}

func TestModule_StartsWithoutAnEventStream(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	deps.Ports.Subscribe = core.NewPorts().Subscribe
	m := agents.New(deps, agents.WithRuntime(agentstest.NewRuntime()))
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v, want nil: the machine runs without its seed", err)
	}
	waitSeed(t, m)
}

func TestModule_StartsWithoutARunnerSupervisor(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := agents.New(deps)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() without a supervisor error = %v, want nil", err)
	}
	waitSeed(t, m) // nothing can be hired, so there is no seed to wait for
}
