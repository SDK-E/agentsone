// SPDX-License-Identifier: MIT

// Package component is the seam of the edge service. Each part of the edge (the internet filter,
// the model gateway, the door, the audit log) is a Component. `agentsone edge` starts them in
// order with one shared Env, waits for a stop signal, and stops them in reverse order.
//
// To add a part, export a Component from its own package and add it to the list in
// internal/edge/commands.go. A part never imports another part: what parts share goes through
// Env, set by the part that provides it.
//
// Owned by work package 15 (edge: network, freeze).
package component

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/tokenkey"
)

// Freezer is the freeze-all switch: Freeze cuts all egress of the edge until Release. The
// internet filter provides it; the control side (the door) uses it.
type Freezer interface {
	Freeze(ctx context.Context) error
	Release(ctx context.Context) error
	Frozen(ctx context.Context) (bool, error)
}

// Env is what every component receives. Components start in list order, so a component may use
// what an earlier one shared.
type Env struct {
	// InnerAddr is the edge's IPv4 address on the inner network, the one the machine reaches.
	InnerAddr netip.Addr
	// DataDir is the edge state folder.
	DataDir string
	// ConfigDir is the edge configuration folder.
	ConfigDir string
	Log       *slog.Logger
	// DoorAddrs are the machine's door addresses (host:port, host may be a name), for the door.
	DoorAddrs []string
	// Admin is the shared mux of the admin socket. Components add their routes in Start, under
	// /v1/<component>/. It serves once every component has started.
	Admin *http.ServeMux

	// Proof records what the edge itself saw in the edge audit log (origin host, proof). Set by
	// the audit component, which starts first. It may be nil.
	Proof ProofRecorder
	// Freezer is set by the internet filter when it starts.
	Freezer Freezer
	// TokenKey is the gateway key, which checks the tokens the daemon signs. Run loads it (or
	// makes it on first start) from the data folder before any part starts. Parts must not change
	// it.
	TokenKey []byte
}

// ProofRecorder writes edge proof records.
type ProofRecorder interface {
	RecordProof(ctx context.Context, p Proof) error
}

// Proof is one thing the edge saw.
type Proof struct {
	Action  string         // area.verb: net.deny, freeze.on, model.call
	Target  string         // kind:id: network:example.org, machine:all
	Outcome string         // success | failure | denied
	Details map[string]any // no secrets, no floats
}

// Component is one part of the edge.
type Component interface {
	// Name names the component in logs and errors.
	Name() string
	// Start starts the component and returns once it serves. It may set the shared fields of env.
	Start(ctx context.Context, env *Env) (Stopper, error)
}

// Stopper stops a started component.
type Stopper interface {
	Stop() error
}

// Run starts every component in order, then serves the admin mux on admin, until ctx ends. It
// then closes the admin socket and stops the components in reverse order. If a component fails to
// start, the ones already started are stopped and Run returns. Run closes admin.
func Run(ctx context.Context, env *Env, components []Component, admin net.Listener) (err error) {
	defer func() {
		if cerr := admin.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			err = errors.Join(err, fmt.Errorf("close admin socket: %w", cerr))
		}
	}()
	if !env.InnerAddr.IsValid() {
		return errors.New("start edge: the inner address is not set")
	}
	if env.Log == nil {
		env.Log = slog.New(slog.DiscardHandler)
	}
	if env.DataDir == "" {
		return errors.New("start edge: the data folder is not set")
	}
	key, err := tokenkey.LoadOrCreate(tokenkey.Path(env.DataDir))
	if err != nil {
		return fmt.Errorf("start edge: %w", err)
	}
	env.TokenKey = key
	env.Admin = http.NewServeMux()
	var started []Stopper
	var names []string
	stopAll := func() error {
		var errs []error
		for i := len(started) - 1; i >= 0; i-- {
			if err := started[i].Stop(); err != nil {
				errs = append(errs, fmt.Errorf("stop %s: %w", names[i], err))
			}
		}
		return errors.Join(errs...)
	}
	for _, c := range components {
		s, err := c.Start(ctx, env)
		if err != nil {
			return errors.Join(fmt.Errorf("start %s: %w", c.Name(), err), stopAll())
		}
		started, names = append(started, s), append(names, c.Name())
		env.Log.Info("edge component started", "component", c.Name())
	}
	srv := &http.Server{Handler: env.Admin, ReadHeaderTimeout: adminHeaderTimeout, ErrorLog: slog.NewLogLogger(env.Log.Handler(), slog.LevelWarn)}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(admin) }()
	env.Log.Info("edge serving", "admin_socket", admin.Addr().String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-served:
		serveErr = fmt.Errorf("admin socket: %w", serveErr)
	}
	env.Log.Info("edge stopping")
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), adminShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		serveErr = errors.Join(serveErr, fmt.Errorf("close admin socket: %w", err))
	}
	return errors.Join(serveErr, stopAll())
}

const (
	adminHeaderTimeout   = 10 * time.Second
	adminShutdownTimeout = 10 * time.Second
)
