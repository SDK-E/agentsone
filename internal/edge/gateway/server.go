// SPDX-License-Identifier: MIT

package gateway

import (
	"context"
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
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/tokenkey"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// BudgetFileName is the budget database inside the edge data folder.
const BudgetFileName = budgetstore.FileName

// InternalName is the gateway's name on the machine-side inner network.
const InternalName = "gateway.agentsone.internal"

// Port is the gateway port on the edge inner address.
const Port = 4000

// shutdownGrace is how long running calls may finish when the gateway stops.
const shutdownGrace = 10 * time.Second

// KeyStore holds provider keys: the router reads them, the admin routes write them.
type KeyStore interface {
	gatewayapp.KeyStore
	admin.Keys
}

// Options configure Open. Zero fields take the real implementations, except Frozen.
type Options struct {
	// DataDir holds gateway.toml, budget.db, gateway-key and the keys folder.
	DataDir string
	// Frozen reads the edge freeze switch before every model call. Nil refuses every call.
	Frozen func(ctx context.Context) (bool, error)
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

// Server is the model gateway: config, budget, router, HTTP handler and admin routes wired
// together.
type Server struct {
	cfgPath  string
	limits   gatewayconfig.Limits
	log      *slog.Logger
	store    *budgetstore.Store
	budget   *budgetapp.Service
	catalog  *catalogSource
	upstream *providers.Client
	keys     KeyStore
	handler  http.Handler
	key      []byte
}

// Open loads gateway.toml, budget.db and the gateway key from the data folder and builds the
// gateway. The gateway key is made on first start and kept.
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
	key, err := tokenkey.LoadOrCreate(filepath.Join(o.DataDir, tokenkey.FileName))
	if err != nil {
		return nil, err
	}

	store, err := budgetstore.Open(ctx, filepath.Join(o.DataDir, BudgetFileName))
	if err != nil {
		return nil, err
	}
	bsvc, err := budgetapp.Open(ctx, store, o.Clock)
	if err != nil {
		return nil, errors.Join(err, store.Close())
	}

	s := &Server{
		cfgPath: cfgPath, limits: cfg.Limits, log: o.Log,
		store: store, budget: bsvc, catalog: &catalogSource{}, keys: o.Keys,
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
		Frozen:     o.Frozen,
		Log:        o.Log,
	})
	return s, nil
}

// TokenKey returns a copy of the key that signs agent gateway tokens. The edge hands it to the
// daemon, which issues the tokens. Root in the agent machine can read it there, which is why the
// token identity is soft.
func (s *Server) TokenKey() []byte { return append([]byte(nil), s.key...) }

// SetAllocations takes the soft per-agent allocations the daemon pushes.
func (s *Server) SetAllocations(a map[string]ledger.Micros) { s.budget.SetAllocations(a) }

// Handler is the gateway's HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// RegisterAdmin adds the gateway's admin routes (providers, keys, budget) to mux.
func (s *Server) RegisterAdmin(mux *http.ServeMux) {
	admin.Register(mux, admin.Options{
		Keys:      s.keys,
		Providers: gatewayconfig.NewStore(s.cfgPath),
		Budget:    s.budget,
		Changed: func() {
			if err := s.Reload(); err != nil {
				s.log.Error("gateway: reload config after an admin change", "err", err)
			}
		},
		Log: s.log,
	})
}

// Reload rereads gateway.toml: providers, models and prices. On error the running config stays.
// Limits apply at the next start.
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

// Run serves the gateway on ln until ctx ends. On each tick (nil means every FlushInterval) it
// reloads the config and flushes the budget. When it stops it lets running calls finish for a
// short grace, flushes the budget a last time and closes budget.db.
func (s *Server) Run(ctx context.Context, ticks <-chan time.Time, ln net.Listener) error {
	if ticks == nil {
		t := time.NewTicker(s.FlushInterval())
		defer t.Stop()
		ticks = t.C
	}
	hs := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second,
		ErrorLog: slog.NewLogLogger(s.log.Handler(), slog.LevelWarn)}
	serveErr := make(chan error, 1)
	go func() {
		if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("gateway: serve %s: %w", ln.Addr(), err)
		}
	}()

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
		// Calls still running are cut. Their reservations were already counted as spent by the
		// last flush, so no money is forgotten.
		_ = hs.Close()
	}
	stopBudget()
	return errors.Join(runErr, <-budgetDone, s.store.Close())
}

// Close releases budget.db for a server that was opened but never run.
func (s *Server) Close() error {
	return errors.Join(s.budget.Flush(context.Background()), s.store.Close())
}
