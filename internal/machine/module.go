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
	humanaccounts "github.com/SDK-E/agentsone/internal/humanuser/adapters/accounts"
	humanapp "github.com/SDK-E/agentsone/internal/humanuser/app"
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

// idleSweep is how often terminals without a client are checked.
const idleSweep = time.Minute

// Module wires machine control into the daemon.
type Module struct {
	core.Base
	deps      core.Deps
	settings  settings
	machine   *app.Machine
	terminals *app.Terminals
	startup   *app.StartupGate
	humans    *humanapp.Users

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ core.Module = (*Module)(nil)

// System is the machine's way onto the container: apt, files, /proc,
// accounts, the recipe store and power. Tests pass machinetest.NoopSystem.
type System struct {
	Packages  app.PackageManager
	Files     app.FileSystem
	Processes app.ProcessTable
	Users     app.UserDB
	Pty       app.PtyStarter
	Metrics   app.MetricsReader
	Recipes   app.RecipeStore
	Baseline  app.BaselineSource
	Power     app.Power
	Timer     app.Timer
	// HumanAccounts makes humans' Linux users (useradd).
	HumanAccounts humanapp.Accounts
}

// RealSystem is the container the daemon runs in.
func RealSystem(cfg *core.Config, clock core.Clock) System {
	passwd := system.NewPasswd("/etc/passwd")
	return System{
		Packages:  system.NewApt(system.ExecRunner{}),
		Files:     system.NewFiles(),
		Processes: system.NewProcs("/proc", passwd, system.Kill),
		Users:     passwd,
		Pty:       system.PtyStarter{},
		Metrics: system.NewMetrics(system.MetricsPaths{
			Proc: "/proc", CPUStat: "/sys/fs/cgroup/cpu.stat",
			Mounts: []string{"/", cfg.AgentsHome, cfg.StateDir},
		}, clock.Now),
		Recipes:       system.NewRecipeStore(filepath.Join(cfg.StateDir, "recipe")),
		Baseline:      system.NewBaseline(filepath.Join(cfg.LibDir, "baseline-packages.txt")),
		Power:         system.Power{},
		Timer:         system.Power{},
		HumanAccounts: &humanaccounts.Accounts{},
	}
}

// New builds the machine module on the real container.
func New(deps core.Deps) *Module { return NewWith(deps, RealSystem(deps.Config, deps.Clock)) }

// NewWith builds the machine module on sys.
func NewWith(deps core.Deps, sys System) *Module {
	m := app.New(app.Deps{
		Packages: sys.Packages, Files: sys.Files, Processes: sys.Processes, Users: sys.Users,
		Metrics: sys.Metrics, Recipes: sys.Recipes, Baseline: sys.Baseline, Power: sys.Power, Timer: sys.Timer,
		Audit: auditPort{deps.Ports}, Clock: deps.Clock, IDs: deps.IDs,
	})
	terms := app.NewTerminals(app.TerminalDeps{
		Start: sys.Pty, Users: sys.Users, Cells: lateCells{deps.Ports}, Audit: auditPort{deps.Ports},
		Log: deps.Log, Clock: deps.Clock, IDs: deps.IDs,
	})
	return &Module{
		deps: deps, settings: defaultSettings(), machine: m, terminals: terms,
		startup: app.NewStartupGate(), humans: humanapp.New(lateHumans{deps.Ports}, sys.HumanAccounts),
	}
}

// HumanUsers makes humans' Linux users on demand, for programs that run as
// a human (the editor, the desktop).
func (m *Module) HumanUsers() *humanapp.Users { return m.humans }

// Name is the module and migration namespace.
func (*Module) Name() string { return "machine" }

// Provide makes the startup gate the daemon's Startup port: agent wakes wait
// on it until the recipe reapply ended.
func (m *Module) Provide(p *core.Ports) { p.Startup = m.startup }

// API serves the machine tag, terminals included. The net tag (per-agent
// network rules) and the ide tag answer 501 until their work packages land.
func (m *Module) API() core.API {
	h := httpadapter.New(m.machine, m.deps.Config.AgentHome).WithTerminals(m.terminals)
	return core.API{Groups: []core.APIGroup{
		machineapi.Group(h, h.Terminal),
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
	m.wg.Add(5)
	m.machine.BeginReapply(bg, m.settings.ReapplyTimeout.Duration, func(rep domain.Report) {
		defer m.wg.Done()
		m.logReport(rep)
		m.startup.Open()
	})
	go func() { defer m.wg.Done(); m.reconcileHumans(bg) }()
	go func() { defer m.wg.Done(); m.machine.Work(bg) }()
	go func() { defer m.wg.Done(); m.syncLoop(bg) }()
	go func() { defer m.wg.Done(); m.idleLoop(bg) }()
	return nil
}

// reconcileHumans makes every human's Linux user again: a rescue resets
// /etc, while their homes live on in the home volume. A failure is logged
// and never stops the daemon; the user is made again on demand.
func (m *Module) reconcileHumans(ctx context.Context) {
	made, err := m.humans.Reconcile(ctx)
	if err != nil {
		m.deps.Log.Warn("human users not all made", "made", made, "error", err)
		return
	}
	m.deps.Log.Info("human users made", "count", made)
}

func (m *Module) idleLoop(ctx context.Context) {
	t := time.NewTicker(idleSweep)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.terminals.CloseIdle(ctx)
		}
	}
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

// Stop ends the background loops and hangs up every terminal, which also
// ends their WebSockets: the HTTP server does not close hijacked
// connections on shutdown.
func (m *Module) Stop(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	m.terminals.CloseAll(ctx)
	return nil
}

// lateCells reads the Cells port when used: ports are filled after New.
type lateCells struct{ p *core.Ports }

func (c lateCells) AgentCell(ctx context.Context, name string) (core.ProcessCell, error) {
	return c.p.Cells.AgentCell(ctx, name)
}

func (c lateCells) HumanCell(ctx context.Context, name string) (core.ProcessCell, error) {
	return c.p.Cells.HumanCell(ctx, name)
}

// lateHumans reads the Humans port when used: ports are filled after New.
type lateHumans struct{ p *core.Ports }

func (l lateHumans) HumanAccounts(ctx context.Context) ([]core.HumanAccount, error) {
	return l.p.Humans.HumanAccounts(ctx) //nolint:wrapcheck // pass-through to the humans port
}

func (l lateHumans) HumanAccount(ctx context.Context, name string) (core.HumanAccount, error) {
	return l.p.Humans.HumanAccount(ctx, name) //nolint:wrapcheck // pass-through to the humans port
}

func (l lateHumans) HumanByUID(ctx context.Context, uid int) (core.HumanAccount, error) {
	return l.p.Humans.HumanByUID(ctx, uid) //nolint:wrapcheck // pass-through to the humans port
}

// auditPort reads the Audit port when used: ports are filled after New.
type auditPort struct{ p *core.Ports }

func (a auditPort) Append(ctx context.Context, e core.AuditEntry) error {
	return a.p.Audit.Append(ctx, e)
}
