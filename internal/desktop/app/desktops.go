// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// ErrTooMany is a new desktop over the machine's desktop cap.
var ErrTooMany = errors.New("too many desktops are running")

// Options tune the desktops.
type Options struct {
	// MaxDesktops is how many humans may have a desktop running at once.
	MaxDesktops int
	// IdleStop is how long a desktop with no client keeps running.
	IdleStop time.Duration
}

// Desktops runs one desktop per human, as the human's own Linux user. A
// desktop starts on its first connection and keeps running while clients
// come and go, so programs opened in it survive a panel switch. It stops
// after IdleStop with no client, and starts again on the next connection
// after a crash.
type Desktops struct {
	accounts Accounts
	launcher Launcher
	clock    core.Clock
	opts     Options

	mu    sync.Mutex
	byHum map[string]*desktop
}

// desktop is one human's desktop, starting or running.
type desktop struct {
	ready   chan struct{} // closed when the launch ended
	display Display       // set when ready and the launch worked
	err     error         // set when ready and the launch failed
	clients int
	idle    time.Time // when the last client left
}

// New builds the desktops.
func New(accounts Accounts, launcher Launcher, clock core.Clock, opts Options) *Desktops {
	return &Desktops{accounts: accounts, launcher: launcher, clock: clock, opts: opts, byHum: map[string]*desktop{}}
}

// Connect opens a VNC connection to human's desktop, starting it first when
// it is not running. Closing the connection leaves the desktop running.
// Errors: core.ErrNotFound when the human has no Linux user, ErrTooMany at
// the desktop cap, core.ErrUnavailable when the desktop would not start or
// answer.
func (d *Desktops) Connect(ctx context.Context, human string) (net.Conn, error) {
	dt, err := d.desktopOf(ctx, human)
	if err != nil {
		return nil, err
	}
	c, err := dt.display.Dial(ctx)
	if err != nil {
		d.leave(human, dt)
		return nil, fmt.Errorf("desktop of %s does not answer: %w: %w", human, err, core.ErrUnavailable)
	}
	return &client{Conn: c, leave: func() { d.leave(human, dt) }}, nil
}

// desktopOf returns human's running desktop with one more client counted,
// launching it when needed. Concurrent first connections share one launch.
func (d *Desktops) desktopOf(ctx context.Context, human string) (*desktop, error) {
	d.mu.Lock()
	dt, ok := d.byHum[human]
	if !ok {
		if len(d.byHum) >= d.opts.MaxDesktops {
			d.mu.Unlock()
			return nil, fmt.Errorf("%w (%d)", ErrTooMany, d.opts.MaxDesktops)
		}
		dt = &desktop{ready: make(chan struct{})}
		d.byHum[human] = dt
		d.mu.Unlock()
		d.launch(ctx, human, dt)
		d.mu.Lock()
	}
	dt.clients++
	d.mu.Unlock()
	<-dt.ready
	if dt.err != nil {
		d.leave(human, dt)
		return nil, dt.err
	}
	return dt, nil
}

// launch starts human's desktop and fills dt. The desktop outlives the
// request that started it, so the launch is not cancelled with it.
func (d *Desktops) launch(ctx context.Context, human string, dt *desktop) {
	defer close(dt.ready)
	u, err := d.accounts.HumanUser(ctx, human)
	if err != nil {
		dt.err = fmt.Errorf("desktop of %s: %w", human, err)
		d.forget(human, dt)
		return
	}
	disp, err := d.launcher.Launch(context.WithoutCancel(ctx), u)
	if err != nil {
		dt.err = fmt.Errorf("start the desktop of %s: %w: %w", human, err, core.ErrUnavailable)
		d.forget(human, dt)
		return
	}
	dt.display = disp
	go func() {
		<-disp.Done()
		d.forget(human, dt)
	}()
}

// forget drops dt if it is still human's desktop.
func (d *Desktops) forget(human string, dt *desktop) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byHum[human] == dt {
		delete(d.byHum, human)
	}
}

func (d *Desktops) leave(_ string, dt *desktop) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dt.clients--
	if dt.clients == 0 {
		dt.idle = d.clock.Now()
	}
}

// Running lists the humans whose desktop is starting or running, sorted.
func (d *Desktops) Running() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.byHum))
	for h := range d.byHum {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

// StopIdle stops desktops left without a client for IdleStop.
func (d *Desktops) StopIdle() {
	now := d.clock.Now()
	d.stopWhere(func(dt *desktop) bool {
		return dt.clients == 0 && dt.display != nil && now.Sub(dt.idle) >= d.opts.IdleStop
	})
}

// StopAll stops every desktop, for the daemon's shutdown.
func (d *Desktops) StopAll() { d.stopWhere(func(*desktop) bool { return true }) }

func (d *Desktops) stopWhere(match func(*desktop) bool) {
	d.mu.Lock()
	var stop []*desktop
	for h, dt := range d.byHum {
		if match(dt) {
			delete(d.byHum, h)
			stop = append(stop, dt)
		}
	}
	d.mu.Unlock()
	for _, dt := range stop {
		<-dt.ready
		if dt.display != nil {
			dt.display.Stop()
		}
	}
}

// client is one VNC connection; Close counts the client out once.
type client struct {
	net.Conn
	once  sync.Once
	leave func()
}

func (c *client) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.leave)
	return err
}
