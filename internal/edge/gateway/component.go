// SPDX-License-Identifier: MIT

package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"

	"github.com/SDK-E/agentsone/internal/edge/component"
)

// Component is the model gateway and the budget as a part of the edge service. It listens on the
// edge inner address, port 4000, only, and adds its admin routes to the shared admin mux. Its zero
// value is the production gateway; tests set the fields.
type Component struct {
	// Port defaults to Port (4000). Tests pass a pointer to 0 for a free port.
	Port *uint16
	// HTTPClient reaches the providers; nil is the default client.
	HTTPClient *http.Client
	// Keys defaults to key files in the data folder.
	Keys KeyStore
	// Started, when set, receives the running server; tests use it to sign tokens.
	Started func(s *Server, addr netip.AddrPort)
}

var _ component.Component = Component{}

// Name names the component.
func (Component) Name() string { return "model gateway" }

// Start opens the gateway and serves it. The freeze switch must already be shared: without it
// the gateway would not know when to stop model calls, so it does not start.
func (c Component) Start(ctx context.Context, env *component.Env) (component.Stopper, error) {
	if env.Freezer == nil {
		return nil, errors.New("model gateway: no freeze switch; the internet filter or the freeze part must start first")
	}
	if env.Log == nil {
		env.Log = slog.New(slog.DiscardHandler)
	}
	srv, err := Open(ctx, Options{DataDir: env.DataDir, Frozen: env.Freezer.Frozen, Keys: c.Keys,
		HTTPClient: c.HTTPClient, Log: env.Log})
	if err != nil {
		return nil, err
	}
	port := uint16(Port)
	if c.Port != nil {
		port = *c.Port
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp4", netip.AddrPortFrom(env.InnerAddr, port).String())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("model gateway: listen: %w", err), srv.Close())
	}
	if env.Admin != nil {
		srv.RegisterAdmin(env.Admin)
	}
	addr := ln.Addr().(*net.TCPAddr).AddrPort()
	if c.Started != nil {
		c.Started(srv, addr)
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan error, 1)
	go func() { done <- srv.Run(runCtx, nil, ln) }()
	env.Log.Info("model gateway listening", "addr", addr.String())
	return &running{cancel: cancel, done: done}, nil
}

type running struct {
	cancel context.CancelFunc
	done   chan error
	err    error
	ended  bool
}

// Stop lets running calls finish for a short grace, flushes the budget and closes it. It is safe
// to call more than once.
func (r *running) Stop() error {
	if !r.ended {
		r.cancel()
		r.err = <-r.done
		r.ended = true
	}
	return r.err
}
