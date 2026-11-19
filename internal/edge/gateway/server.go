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

	budgetstore "github.com/SDK-E/agentsone/internal/edge/budget/adapters/sqlite"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/admin"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	gatewayhttp "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/http"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/keyfile"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/providers"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// BudgetFileName is the budget database inside the edge data folder.
const BudgetFileName = budgetstore.FileName

// InternalName is the gateway's name on the machine-side inner network.
const InternalName = "gateway.agentsone.internal"

// shutdownGrace is how long running calls may finish when the gateway stops.
const shutdownGrace = 10 * time.Second

// KeyStore holds provider keys: the router reads them, the admin socket
// writes them.
type KeyStore interface {
	gatewayapp.KeyStore
	admin.Keys
}

// Options configure Open. Zero fields take the real implementations.
type Options struct {
	// DataDir holds gateway.toml, budget.db and the keys folder.
	DataDir string
	// Keys defaults to key files in DataDir/keys.
	Keys KeyStore
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

func (c *catalogSource) providerIDs() []string {
	cat := c.Catalog()
	ids := make([]string, 0, len(cat.Providers))
	for _, p := range cat.Providers {
		ids = append(ids, string(p.ID))
	}
	return ids
}

// Server is the running model gateway: config, budget, router, HTTP handler
// and admin socket handler wired together.
type Server struct {
	dataDir  string
	cfgPath  string
	server   gatewayconfig.Server
	limits   gatewayconfig.Limits
	log      *slog.Logger
	store    *budgetstore.Store
	budget   *budgetapp.Service
	catalog  *catalogSource
	upstream *providers.Client
	handler  http.Handler
	admin    http.Handler
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
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("gateway: data folder: %w", err)
	}
	if o.Keys == nil {
		ks, err := keyfile.Open(filepath.Join(o.DataDir, keyfile.FolderName))
		if err != nil {
			return nil, err
		}
		o.Keys = ks
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
		dataDir: o.DataDir, cfgPath: cfgPath, server: cfg.Server, limits: cfg.Limits, log: o.Log,
		store: store, budget: bsvc, catalog: &catalogSource{},
		upstream: providers.New(o.HTTPClient, cfg.ProviderConfigs()), key: key,
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
	s.admin = admin.New(admin.Options{Keys: o.Keys, Providers: s.catalog.providerIDs})
	return s, nil
}

// TokenKey returns a copy of the per-boot key that signs agent gateway
// tokens. The edge hands it to the daemon, which issues the tokens. Root in
// the agent machine can read it there, which is why the token identity is
// soft.
func (s *Server) TokenKey() []byte { return append([]byte(nil), s.key...) }

// SetAllocations takes the soft per-agent allocations the daemon pushes.
func (s *Server) SetAllocations(a map[string]ledger.Micros) { s.budget.SetAllocations(a) }

// Handler is the gateway's HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// AdminHandler is the admin socket's HTTP handler.
func (s *Server) AdminHandler() http.Handler { return s.admin }

// ListenAddr is the configured gateway address.
func (s *Server) ListenAddr() string { return s.server.Listen }

// AdminSocketPath is the configured admin socket path.
func (s *Server) AdminSocketPath() string { return s.server.AdminSocketPath(s.dataDir) }

// ListenAdmin opens the admin socket at its configured path, mode 0660, in
// the configured group shared by the edge and control containers.
func (s *Server) ListenAdmin() (net.Listener, error) {
	return admin.Listen(s.AdminSocketPath(), s.server.AdminGID)
}

// Reload rereads gateway.toml: providers, models and prices. On error the
// running config stays. Limits and the server section apply at the next
// start.
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

// Listeners are what Run serves. Admin may be nil.
type Listeners struct {
	Gateway net.Listener
	Admin   net.Listener
}

func serve(hs *http.Server, ln net.Listener, errs chan<- error) {
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		errs <- fmt.Errorf("gateway: serve %s: %w", ln.Addr(), err)
	}
}

// Run serves the gateway and the admin socket until ctx ends. On each tick
// (nil means every FlushInterval) it reloads the config and flushes the
// budget. When it stops it lets running calls finish for a short grace,
// flushes the budget a last time and closes budget.db.
func (s *Server) Run(ctx context.Context, ticks <-chan time.Time, ls Listeners) error {
	if ticks == nil {
		t := time.NewTicker(s.FlushInterval())
		defer t.Stop()
		ticks = t.C
	}
	servers := []*http.Server{}
	serveErr := make(chan error, 2)
	if ls.Gateway != nil {
		hs := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, hs)
		go serve(hs, ls.Gateway, serveErr)
	}
	if ls.Admin != nil {
		hs := &http.Server{Handler: s.admin, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, hs)
		go serve(hs, ls.Admin, serveErr)
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
	for _, hs := range servers {
		if err := hs.Shutdown(grace); err != nil {
			// Calls still running are cut. Their reservations were already
			// counted as spent by the last flush, so no money is forgotten.
			_ = hs.Close()
		}
	}
	stopBudget()
	return errors.Join(runErr, <-budgetDone, s.store.Close())
}

// Close releases budget.db for a server that was opened but never run.
func (s *Server) Close() error {
	return errors.Join(s.budget.Flush(context.Background()), s.store.Close())
}
