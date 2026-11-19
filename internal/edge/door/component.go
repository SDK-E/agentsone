// SPDX-License-Identifier: MIT

package door

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/door/adapters/listen"
	"github.com/SDK-E/agentsone/internal/edge/door/adapters/proxy"
)

// DefaultUpstream is the machine's daemon on the inner network.
const DefaultUpstream = "http://machine:7700"

const (
	// readHeaderTimeout bounds slow clients sending headers.
	readHeaderTimeout = 10 * time.Second
	// idleTimeout closes idle keep-alive connections.
	idleTimeout = 2 * time.Minute
	// maxHeaderBytes caps request headers.
	maxHeaderBytes = 64 << 10
	// shutdownGrace lets running requests finish when the door closes; tunnels are then cut.
	shutdownGrace = 10 * time.Second
)

// Component is the web door: a reverse proxy from each door address (env.DoorAddrs, on the outer
// side) to the machine's daemon. There is no write timeout: event streams and tunnels stay open.
type Component struct {
	// Upstream is the daemon's base URL; empty means DefaultUpstream.
	Upstream string
}

var _ component.Component = Component{}

// Name names the component.
func (Component) Name() string { return "door" }

// Start opens one listener per address of every door address and serves the proxy on each. With
// no door address the door stays closed. If any address is refused, nothing stays open.
func (c Component) Start(ctx context.Context, env *component.Env) (component.Stopper, error) {
	up, err := upstreamURL(c.Upstream)
	if err != nil {
		return nil, err
	}
	log := env.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var lns []net.Listener
	for _, addr := range env.DoorAddrs {
		opened, err := listen.Listen(ctx, addr, env.InnerAddr, net.DefaultResolver)
		if err != nil {
			for _, ln := range lns {
				_ = ln.Close()
			}
			return nil, err
		}
		lns = append(lns, opened...)
	}
	d := &Door{lns: lns}
	handler := proxy.New(up, log)
	for _, ln := range lns {
		srv := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		}
		d.servers = append(d.servers, srv)
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				log.Error("door: serve", "addr", ln.Addr().String(), "err", err)
			}
		}()
		log.Info("door open", "addr", ln.Addr().String(), "upstream", up.String())
	}
	return d, nil
}

func upstreamURL(raw string) (*url.URL, error) {
	if raw == "" {
		raw = DefaultUpstream
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("door: upstream %q must be an http or https URL with a host", raw)
	}
	return u, nil
}

// Door is the running web door.
type Door struct {
	lns     []net.Listener
	servers []*http.Server
	wg      sync.WaitGroup
}

// Addrs are the addresses the door listens on.
func (d *Door) Addrs() []net.Addr {
	out := make([]net.Addr, 0, len(d.lns))
	for _, ln := range d.lns {
		out = append(out, ln.Addr())
	}
	return out
}

// Stop closes the door: it stops accepting, lets running requests finish for a short grace, then
// cuts what is still open, tunnels included, and waits for the servers to end.
func (d *Door) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	var errs []error
	for _, srv := range d.servers {
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("door: shut down: %w", err))
			_ = srv.Close()
		}
	}
	// Hijacked connections (WebSocket tunnels) are not tracked by the server.
	for _, ln := range d.lns {
		if g, ok := ln.(*listen.Guarded); ok {
			g.CloseConns()
		}
	}
	d.wg.Wait()
	return errors.Join(errs...)
}
