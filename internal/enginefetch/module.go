// SPDX-License-Identifier: MIT

// Package enginefetch wires the engine fetch into the daemon. The module
// installs the pinned Claude engine on the engines volume once a human
// accepted its licence: it tries at start and again after every licence
// accepted event. The event is stored, so a restart replays it.
package enginefetch

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/enginefetch/adapters/coresetup"
	"github.com/SDK-E/agentsone/internal/enginefetch/adapters/disk"
	"github.com/SDK-E/agentsone/internal/enginefetch/adapters/httpsource"
	"github.com/SDK-E/agentsone/internal/enginefetch/app"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

// LicenceAcceptedEvent is the event internal/auth publishes when a human
// accepts the engine licence.
const LicenceAcceptedEvent = "agentsone.setup.licence.accepted"

// backoffStep is the wait before a new download attempt, times the attempt
// number.
const backoffStep = 5 * time.Second

// Module runs the engine fetcher in the background.
type Module struct {
	core.Base
	log     *slog.Logger
	ports   *core.Ports
	fetcher *app.Fetcher
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

var _ core.Module = (*Module)(nil)

// wiring is what New fills from the machine and tests replace.
type wiring struct {
	pin          domain.Pin
	goos, goarch string
	source       app.Source
	disk         app.Disk
}

// New builds the fetcher on the pinned engine, this machine's platform, the
// default HTTP client (which leaves through the edge proxy) and the engines
// volume. It panics only when the pin built into the binary is broken.
func New(deps core.Deps) *Module {
	pin, err := domain.Pinned()
	if err != nil {
		panic(fmt.Sprintf("enginefetch: %v", err))
	}
	return newModule(deps, wiring{
		pin: pin, goos: runtime.GOOS, goarch: runtime.GOARCH,
		source: httpsource.New(nil), disk: disk.New(domain.DefaultRoot),
	})
}

func newModule(deps core.Deps, w wiring) *Module {
	setup := coresetup.New(deps.Ports, deps.Log)
	f, err := app.New(app.Deps{
		Pin: w.pin, GOOS: w.goos, GOARCH: w.goarch,
		Licence: setup, Status: setup, Source: w.source, Disk: w.disk, Clock: deps.Clock,
		Wait: backoff,
	})
	if err != nil {
		panic(fmt.Sprintf("enginefetch: %v", err))
	}
	return &Module{log: deps.Log, ports: deps.Ports, fetcher: f}
}

// Name is the module name.
func (*Module) Name() string { return "enginefetch" }

// Start subscribes to licence accepted events, replaying stored ones, and
// runs the fetcher until Stop or the daemon context ends. Without the
// events port the engine is still fetched once at start.
func (m *Module) Start(ctx context.Context) error {
	ctx, m.stop = context.WithCancel(ctx)
	events, err := m.ports.Subscribe.Subscribe(ctx, core.EventFilter{Types: []string{LicenceAcceptedEvent}})
	if err != nil {
		m.log.Warn("engine fetch: licence events are not available, so the engine is fetched only at start", "err", err)
		events = nil
	}
	m.wg.Go(func() {
		m.fetcher.Watch(ctx, events, func(err error) { m.log.Error("engine fetch", "err", err) })
	})
	return nil
}

// Stop ends the fetcher. A download in progress stops and continues from
// where it stopped at the next start.
func (m *Module) Stop(context.Context) error {
	if m.stop != nil {
		m.stop()
		m.wg.Wait()
	}
	return nil
}

// backoff waits backoffStep times the attempt number.
func backoff(ctx context.Context, attempt int) error {
	return wait(ctx, backoffStep*time.Duration(attempt))
}

func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the context error passes through
	case <-t.C:
		return nil
	}
}
