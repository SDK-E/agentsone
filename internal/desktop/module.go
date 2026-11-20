// SPDX-License-Identifier: MIT

package desktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	httpadapter "github.com/SDK-E/agentsone/internal/desktop/adapters/http"
	"github.com/SDK-E/agentsone/internal/desktop/adapters/system"
	"github.com/SDK-E/agentsone/internal/desktop/app"
	humanaccounts "github.com/SDK-E/agentsone/internal/humanuser/adapters/accounts"
)

// settings is the [desktop] table of the daemon configuration.
type settings struct {
	// MaxDesktops is how many humans may have a desktop running at once.
	// Each takes about 100 MiB, and a web browser in it about 200 more.
	MaxDesktops int `toml:"max_desktops"`
	// IdleStop is how long a desktop with nobody looking at it keeps
	// running. Programs open in it end when it stops.
	IdleStop core.Duration `toml:"idle_stop"`
}

func defaultSettings() settings {
	return settings{MaxDesktops: 4, IdleStop: core.Duration{Duration: 30 * time.Minute}}
}

// idleSweep is how often desktops without a client are checked.
const idleSweep = time.Minute

// Parts are the module's ways onto the container. Tests pass fakes.
type Parts struct {
	Accounts app.Accounts
	Launcher app.Launcher
}

// Module wires the desktops into the daemon.
type Module struct {
	core.Base
	deps     core.Deps
	parts    Parts
	settings settings

	mu       sync.Mutex
	desktops *app.Desktops
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

var _ core.Module = (*Module)(nil)

// New builds the module on the real container: labwc and wayvnc from the
// image, one runtime folder per desktop under the daemon's runtime folder,
// and each human's own Linux user.
func New(deps core.Deps) *Module {
	return NewWith(deps, Parts{
		Accounts: HumanAccounts(deps.Ports, &humanaccounts.Accounts{}),
		Launcher: system.Launcher{
			Compositor: "/usr/bin/labwc", VNCServer: "/usr/bin/wayvnc",
			RuntimeRoot:  filepath.Join(deps.Config.RuntimeDir, "desktop"),
			StartTimeout: 20 * time.Second, StopGrace: 5 * time.Second,
			Procs: system.Procs{Root: "/proc"},
		},
	})
}

// NewWith builds the module on parts.
func NewWith(deps core.Deps, parts Parts) *Module {
	return &Module{deps: deps, parts: parts, settings: defaultSettings()}
}

// Name is the module name.
func (*Module) Name() string { return "desktop" }

// API serves the VNC bridge. It is not a contract operation: the stream
// is raw VNC, like the IDE server's own protocol under /ide/.
func (m *Module) API() core.API {
	return core.API{Routes: []core.Route{{Pattern: httpadapter.Pattern, Handler: httpadapter.Bridge(m)}}}
}

// Connect opens a VNC connection to human's desktop.
func (m *Module) Connect(ctx context.Context, human string) (net.Conn, error) {
	m.mu.Lock()
	d := m.desktops
	m.mu.Unlock()
	if d == nil {
		return nil, fmt.Errorf("the desktop service is not running: %w", core.ErrUnavailable)
	}
	c, err := d.Connect(ctx, human)
	if errors.Is(err, core.ErrUnavailable) {
		m.deps.Log.Warn("desktop did not start", "human", human, "error", err)
	}
	return c, err
}

// Start reads [desktop] and begins the idle sweep. Desktops start on
// their first connection, so an unused desktop costs nothing.
func (m *Module) Start(context.Context) error {
	if err := m.deps.Config.Section("desktop", &m.settings); err != nil {
		return fmt.Errorf("desktop settings: %w", err)
	}
	if m.settings.MaxDesktops < 1 {
		return errors.New("desktop settings: max_desktops must be 1 or more")
	}
	if m.settings.IdleStop.Duration <= 0 {
		return errors.New("desktop settings: idle_stop must be longer than 0s")
	}
	d := app.New(m.parts.Accounts, m.parts.Launcher, m.deps.Clock, app.Options{
		MaxDesktops: m.settings.MaxDesktops, IdleStop: m.settings.IdleStop.Duration,
	})
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.desktops, m.cancel = d, cancel
	m.mu.Unlock()
	m.wg.Go(func() {
		t := time.NewTicker(idleSweep)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.StopIdle()
			}
		}
	})
	return nil
}

// Stop ends the sweep and every desktop. Open bridges end with them: the
// HTTP server does not close hijacked connections on shutdown.
func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	d, cancel := m.desktops, m.cancel
	m.desktops, m.cancel = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	if d != nil {
		d.StopAll()
	}
	return nil
}
