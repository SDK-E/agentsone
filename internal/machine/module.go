// SPDX-License-Identifier: MIT

package machine

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/ideapi"
	"github.com/SDK-E/agentsone/internal/contract/api/machineapi"
	"github.com/SDK-E/agentsone/internal/contract/api/netapi"
	"github.com/SDK-E/agentsone/internal/core"
	httpadapter "github.com/SDK-E/agentsone/internal/machine/adapters/http"
	"github.com/SDK-E/agentsone/internal/machine/adapters/system"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// settings is the [machine] table of the daemon configuration.
type settings struct {
	// ReapplyTimeout bounds the recipe reapply at start, so a slow mirror
	// never holds the daemon.
	ReapplyTimeout core.Duration `toml:"reapply_timeout"`
	// SyncInterval is how often packages installed from a terminal are
	// picked up into the recipe.
	SyncInterval core.Duration `toml:"sync_interval"`
}

func defaultSettings() settings {
	return settings{
		ReapplyTimeout: core.Duration{Duration: 5 * time.Minute},
		SyncInterval:   core.Duration{Duration: 5 * time.Minute},
	}
}

// Module wires machine control into the daemon.
type Module struct {
	core.Base
	deps     core.Deps
	settings settings
	machine  *app.Machine
	startup  *app.StartupGate

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ core.Module = (*Module)(nil)

// New builds the machine module on the real container.
func New(deps core.Deps) *Module {
	cfg := deps.Config
	passwd := system.NewPasswd("/etc/passwd")
	m := app.New(app.Deps{
		Packages:  system.NewApt(system.ExecRunner{}),
		Files:     system.NewFiles(),
		Processes: system.NewProcs("/proc", passwd, system.Kill),
		Users:     passwd,
		Metrics: system.NewMetrics(system.MetricsPaths{
			Proc: "/proc", CPUStat: "/sys/fs/cgroup/cpu.stat",
			Mounts: []string{"/", cfg.AgentsHome, cfg.StateDir},
		}, deps.Clock.Now),
		Recipes:  system.NewRecipeStore(filepath.Join(cfg.StateDir, "recipe")),
		Baseline: system.NewBaseline(filepath.Join(cfg.LibDir, "baseline-packages.txt")),
		Power:    system.Power{},
		Timer:    system.Power{},
		Audit:    auditPort{deps.Ports},
		Clock:    deps.Clock,
		IDs:      deps.IDs,
	})
	return &Module{deps: deps, settings: defaultSettings(), machine: m, startup: app.NewStartupGate()}
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "machine" }

// Provide makes the startup gate the daemon's Startup port: agent wakes wait
// on it until the recipe reapply ended.
func (m *Module) Provide(p *core.Ports) { p.Startup = m.startup }

// API serves the machine tag. Terminals, the net tag (per-agent network
// rules) and the ide tag answer 501 until their work packages land.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{
		machineapi.Group(httpadapter.New(m.machine, m.deps.Config.AgentHome)),
		netapi.Group(netapi.Unimplemented{}),
		ideapi.Group(ideapi.Unimplemented{}),
	}}
}

// Start returns at once: the recipe reapply runs in the background, so the
// API opens early and the machine-recipe service shows "activating". Agent
// wakes wait on the startup gate, which opens when the reapply ends. A
// failure is logged, audited and shown on the machine-recipe service; it
// opens the gate too and never stops the daemon.
func (m *Module) Start(context.Context) error {
	if err := m.deps.Config.Section("machine", &m.settings); err != nil {
		return fmt.Errorf("machine settings: %w", err)
	}
	bg, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(3)
	m.machine.BeginReapply(bg, m.settings.ReapplyTimeout.Duration, func(rep domain.Report) {
		defer m.wg.Done()
		m.logReport(rep)
		m.startup.Open()
	})
	go func() { defer m.wg.Done(); m.machine.Work(bg) }()
	go func() { defer m.wg.Done(); m.syncLoop(bg) }()
	return nil
}

func (m *Module) logReport(rep domain.Report) {
	if rep.State == domain.RecipeFailed {
		m.deps.Log.Warn("machine recipe did not fully apply", "summary", rep.Service().Description)
		return
	}
	m.deps.Log.Info("machine recipe applied", "packages", rep.Packages, "files", rep.Files)
}

func (m *Module) syncLoop(ctx context.Context) {
	t := time.NewTicker(m.settings.SyncInterval.Duration)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.machine.SyncRecipe(ctx); err != nil {
				m.deps.Log.Warn("machine recipe sync", "error", err)
			}
		}
	}
}

// Stop ends the package worker and the recipe sync.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	return nil
}

// auditPort reads the Audit port when used: ports are filled after New.
type auditPort struct{ p *core.Ports }

func (a auditPort) Append(ctx context.Context, e core.AuditEntry) error {
	return a.p.Audit.Append(ctx, e)
}
