// SPDX-License-Identifier: MIT

package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	httpadapter "github.com/SDK-E/agentsone/internal/edge/audit/adapters/http"
	"github.com/SDK-E/agentsone/internal/edge/audit/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/edge/audit/app"
	"github.com/SDK-E/agentsone/internal/edge/component"
)

// DefaultPort is the sink's port on the inner address. The machine reaches it at
// http://gateway.agentsone.internal:7710 (the edge's inner address). It is not published.
const DefaultPort = 7710

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	maxHeaderBytes    = 16 << 10
	shutdownGrace     = 10 * time.Second
)

// Component is the edge audit sink. It starts first, so every later part can record proof
// through env.Proof.
type Component struct {
	// Port on the inner address; 0 means DefaultPort. Tests pass a negative number for any
	// free port.
	Port int
}

var _ component.Component = Component{}

// Name names the component.
func (Component) Name() string { return "audit" }

// Start opens audit.db, shares the proof recorder, adds the admin routes and listens for the
// machine's records on the inner address only.
func (c Component) Start(ctx context.Context, env *component.Env) (component.Stopper, error) {
	log := env.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	store, err := sqlite.Open(ctx, filepath.Join(env.DataDir, sqlite.FileName))
	if err != nil {
		return nil, err
	}
	sink := app.New(store, systemClock{}, log)
	port := c.Port
	switch {
	case port == 0:
		port = DefaultPort
	case port < 0:
		port = 0
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", netip.AddrPortFrom(env.InnerAddr, uint16(port)).String())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("audit: listen on the inner address: %w", err), store.Close())
	}
	env.Proof = proofRecorder{sink}
	httpadapter.RegisterAdmin(env.Admin, sink, log)
	s := &Sink{ln: ln, store: store, srv: &http.Server{
		Handler:           httpadapter.Inner(sink, log),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Error("audit: serve", "err", err)
		}
	}()
	log.Info("audit sink listening", "addr", ln.Addr().String())
	return s, nil
}

// Sink is the running audit sink.
type Sink struct {
	ln    net.Listener
	srv   *http.Server
	store *sqlite.Store
	wg    sync.WaitGroup
}

// Addr is where the sink listens.
func (s *Sink) Addr() net.Addr { return s.ln.Addr() }

// Stop stops listening, lets running requests finish and closes audit.db.
func (s *Sink) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	s.wg.Wait()
	return errors.Join(err, s.store.Close())
}

type proofRecorder struct{ sink *app.Sink }

func (p proofRecorder) RecordProof(ctx context.Context, pr component.Proof) error {
	return p.sink.RecordProof(ctx, app.ProofEntry{
		Action: pr.Action, Target: pr.Target, Outcome: pr.Outcome, Details: pr.Details,
	})
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
