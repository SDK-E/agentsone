// SPDX-License-Identifier: MIT

package gateway

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/SDK-E/agentsone/internal/host/budget/budgetapp"
	"github.com/SDK-E/agentsone/internal/host/budget/budgetstore"
	"github.com/SDK-E/agentsone/internal/host/budget/ledger"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayconfig"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayhttp"
	"github.com/SDK-E/agentsone/internal/host/gateway/keychain"
	"github.com/SDK-E/agentsone/internal/host/gateway/providers"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

// BudgetFileName is the budget database inside the host data folder.
const BudgetFileName = budgetstore.FileName

// InternalName is the gateway's name inside the VM.
const InternalName = "gateway.agentsone.internal"

// VMAddr is where the gateway listens inside the virtual network.
const VMAddr = "192.168.127.1:4000"

// ListenFunc opens a listener, as the host network stack's Network.Listen does.
type ListenFunc func(network, addr string) (net.Listener, error)

// ListenVM opens the gateway's listener inside the virtual network through
// the host network stack. Pass the result to Server.Run.
func ListenVM(listen ListenFunc) (net.Listener, error) {
	ln, err := listen("tcp", VMAddr)
	if err != nil {
		return nil, fmt.Errorf("gateway: listen on %s: %w", VMAddr, err)
	}
	return ln, nil
}

// shutdownGrace is how long running calls may finish when the gateway stops.
const shutdownGrace = 10 * time.Second

// Options configure Open. Zero fields take the real implementations.
type Options struct {
	// DataDir holds gateway.toml and budget.db.
	DataDir string
	// Keys defaults to the macOS Keychain.
	Keys gatewayapp.KeyStore
	// Clock defaults to the system clock.
	Clock gatewayapp.Clock
	// HTTPClient defaults to one with the configured header timeout.
	HTTPClient *http.Client
	Log        *slog.Logger
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type catalogSource struct {
	p atomic.Pointer[routing.Catalog]
}

func (c *catalogSource) Catalog() routing.Catalog { return *c.p.Load() }

// Server is the running model gateway: config, budget, router and HTTP
// handler wired together.
type Server struct {
	cfgPath  string
	limits   gatewayconfig.Limits
	log      *slog.Logger
	store    *budgetstore.Store
	budget   *budgetapp.Service
	catalog  *catalogSource
	upstream *providers.Client
	handler  http.Handler
	key      []byte
}

// Open loads gateway.toml and budget.db from the data folder and builds the
// gateway. It makes a fresh per-boot token key.
func Open(ctx context.Context, o Options) (*Server, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Clock == nil {
		o.Clock = systemClock{}
	}
	if o.Keys == nil {
		o.Keys = keychain.NewSystem()
	}
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("gateway: data folder: %w", err)
	}
	cfgPath := filepath.Join(o.DataDir, gatewayconfig.FileName)
	cfg, err := gatewayconfig.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	cat, err := cfg.Catalog()
	if err != nil {
		return nil, fmt.Errorf("gateway: %s: %w", cfgPath, err)
	}
	if o.HTTPClient == nil {
		o.HTTPClient = providers.NewHTTPClient(time.Duration(cfg.Limits.HeaderTimeoutSeconds) * time.Second)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("gateway: token key: %w", err)
	}

	store, err := budgetstore.Open(ctx, filepath.Join(o.DataDir, BudgetFileName))
	if err != nil {
		return nil, err
	}
	bsvc, err := budgetapp.Open(ctx, store)
	if err != nil {
		return nil, errors.Join(err, store.Close())
	}

	s := &Server{
		cfgPath: cfgPath, limits: cfg.Limits, log: o.Log, store: store, budget: bsvc,
		catalog: &catalogSource{}, upstream: providers.New(o.HTTPClient, cfg.ProviderConfigs()), key: key,
	}
	s.catalog.p.Store(&cat)
	router := gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog:  s.catalog,
		Keys:     o.Keys,
		Upstream: s.upstream,
		Budget:   bsvc,
		Breaker: routing.NewBreaker(cfg.Limits.BreakerFailures,
			time.Duration(cfg.Limits.BreakerCooldownSeconds)*time.Second),
		Clock: o.Clock,
		Limits: gatewayapp.Limits{
			DefaultMaxOutputTokens: cfg.Limits.DefaultMaxOutputTokens,
			MaxOutputTokensCap:     cfg.Limits.MaxOutputTokensCap,
			MaxAttempts:            cfg.Limits.MaxAttempts,
		},
	})
	s.handler = gatewayhttp.New(gatewayhttp.Options{
		Router:     router,
		Key:        func() []byte { return s.key },
		Clock:      o.Clock,
		RetryAfter: time.Duration(cfg.Limits.RetryAfterSeconds) * time.Second,
		Log:        o.Log,
	})
	return s, nil
}

// TokenKey returns a copy of the per-boot key that signs agent gateway
// tokens. The host hands it to the daemon, which issues the tokens. Root in
// the VM can read it there, which is why the token identity is soft.
func (s *Server) TokenKey() []byte { return append([]byte(nil), s.key...) }

// SetAllocations takes the soft per-agent allocations the daemon pushes.
func (s *Server) SetAllocations(a map[string]ledger.Micros) { s.budget.SetAllocations(a) }

// Handler is the gateway's HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Reload rereads gateway.toml: providers, models and prices. On error the
// running config stays. Limits apply at the next start.
func (s *Server) Reload() error {
	cfg, err := gatewayconfig.Load(s.cfgPath)
	if err != nil {
		return err
	}
	cat, err := cfg.Catalog()
	if err != nil {
		return err
	}
	s.upstream.SetConfigs(cfg.ProviderConfigs())
	s.catalog.p.Store(&cat)
	return nil
}

// FlushInterval is how often Run flushes the budget and reloads the config.
func (s *Server) FlushInterval() time.Duration {
	return time.Duration(max(s.limits.FlushIntervalSeconds, 1)) * time.Second
}

// Run serves HTTP on every listener until ctx ends. On each tick (nil means
// every FlushInterval) it reloads the config and flushes the budget. When it
// stops it lets running calls finish for a short grace, flushes the budget a
// last time and closes budget.db.
func (s *Server) Run(ctx context.Context, ticks <-chan time.Time, listeners ...net.Listener) error {
	if ticks == nil {
		t := time.NewTicker(s.FlushInterval())
		defer t.Stop()
		ticks = t.C
	}
	hs := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func() {
			if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("gateway: serve %s: %w", ln.Addr(), err)
			}
		}()
	}

	budgetCtx, stopBudget := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBudget()
	budgetTicks := make(chan time.Time)
	budgetDone := make(chan error, 1)
	go func() {
		budgetDone <- s.budget.Run(budgetCtx, budgetTicks, func(err error) {
			s.log.Error("gateway: flush budget", "err", err)
		})
	}()

	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-serveErr:
			runErr = err
			break loop
		case t := <-ticks:
			if err := s.Reload(); err != nil {
				s.log.Error("gateway: reload config, keeping the running one", "err", err)
			}
			budgetTicks <- t
		}
	}

	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := hs.Shutdown(grace); err != nil {
		// Calls still running are cut. Their reservations were already
		// counted as spent by the last flush, so no money is forgotten.
		_ = hs.Close()
	}
	stopBudget()
	return errors.Join(runErr, <-budgetDone, s.store.Close())
}

// Close releases budget.db for a server that was opened but never run.
func (s *Server) Close() error {
	return errors.Join(s.budget.Flush(context.Background()), s.store.Close())
}
