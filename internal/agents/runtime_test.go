// SPDX-License-Identifier: MIT

package agents_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents"
	supdomain "github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// launcher records what the supervisor asks of the machine.
type launcher struct {
	mu        sync.Mutex
	installed map[string]supdomain.UnitSpec
	started   []string
}

func (l *launcher) Install(_ context.Context, u supdomain.UnitSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.installed == nil {
		l.installed = map[string]supdomain.UnitSpec{}
	}
	l.installed[u.Agent] = u
	return nil
}
func (l *launcher) Start(_ context.Context, a string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = append(l.started, a)
	return nil
}
func (l *launcher) Stop(context.Context, string) error    { return nil }
func (l *launcher) Restart(context.Context, string) error { return nil }
func (l *launcher) Remove(context.Context, string) error  { return nil }
func (l *launcher) Mode(string) supdomain.LimitMode       { return supdomain.LimitsSoft }

func (l *launcher) units() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for name, u := range l.installed {
		out = append(out, name+"/"+u.Account.User)
	}
	slices.Sort(out)
	return out
}

type accounts struct {
	mu      sync.Mutex
	created []string
}

func (a *accounts) Ensure(_ context.Context, acc supdomain.Account) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.created = append(a.created, acc.User)
	return acc.UID, nil
}
func (a *accounts) Lock(context.Context, supdomain.Account) error { return nil }
func (a *accounts) EnsureRootHome(context.Context, supdomain.Account) error {
	return nil
}

type openaiRunner struct{}

func (openaiRunner) Info() core.RunnerInfo {
	return core.RunnerInfo{Name: "openai", Version: "1.0.0", Source: "builtin"}
}
func (openaiRunner) Exec(_ context.Context, _ core.Agent, socket string) (core.ExecSpec, error) {
	return core.ExecSpec{Path: "/usr/lib/agentsone/runners/openai", Args: []string{"--socket", socket}}, nil
}

// With the runner supervisor wired, the Small Seed installs (users and
// runner units for founder, router and waker), the setup status says so,
// and waking the founder starts its runner.
func TestModule_WithTheSupervisorTheSeedInstallsAndTheFounderStarts(t *testing.T) {
	t.Parallel()
	deps, fakes := coretest.NewDeps(t)
	addSmallSeed(t, fakes)
	fakes.Runners.Register(openaiRunner{})
	l, acc := &launcher{}, &accounts{}
	m := agents.New(deps, agents.WithSupervisor(agents.SupervisorOptions{Launcher: l, Accounts: acc}))
	ctx := context.Background()
	m.Provide(deps.Ports)
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })

	firstHuman(t, fakes)
	waitSeed(t, m)

	if diff := cmp.Diff([]string{"founder/root", "router/router", "waker/waker"}, l.units()); diff != "" {
		t.Errorf("runner units (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"router", "waker"}, acc.created); diff != "" {
		t.Errorf("agent users (-want +got):\n%s", diff)
	}
	if ok, err := deps.Ports.Seed.SeedInstalled(ctx); err != nil || !ok {
		t.Fatalf("SeedInstalled = %v, %v; want true", ok, err)
	}

	if err := deps.Ports.Waker.Wake(ctx, "founder", core.WakeEvent, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("wake founder: %v", err)
	}
	l.mu.Lock()
	started := slices.Clone(l.started)
	l.mu.Unlock()
	if diff := cmp.Diff([]string{"founder"}, started); diff != "" {
		t.Errorf("started runners (-want +got):\n%s", diff)
	}
}
