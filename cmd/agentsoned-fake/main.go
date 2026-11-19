// SPDX-License-Identifier: MIT

// Command agentsoned-fake serves the contract's golden fixtures so the
// interface can be built without a VM. Pick a fixture variant with
// ?fixture=<variant> or the X-Fixture-Variant header.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

const shutdownGrace = 5 * time.Second

type config struct {
	addr           string
	fixtures       string
	origins        []string
	streamInterval time.Duration
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		log.Error("bad flags", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, log); err != nil {
		log.Error("fake daemon stopped", "err", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("agentsoned-fake", flag.ContinueOnError)
	cfg := config{}
	var origins string
	fs.StringVar(&cfg.addr, "addr", "127.0.0.1:7700", "listen address")
	fs.StringVar(&cfg.fixtures, "fixtures", "contract/fixtures/api", "folder of API fixtures")
	fs.StringVar(&origins, "cors-origin", "http://127.0.0.1:5173,http://localhost:5173", "comma-separated origins allowed by CORS")
	fs.DurationVar(&cfg.streamInterval, "stream-interval", defaultStreamInterval, "pause between replayed stream events")
	if err := fs.Parse(args); err != nil {
		return config{}, fmt.Errorf("parse flags: %w", err)
	}
	cfg.origins = strings.Split(origins, ",")
	return cfg, nil
}

func run(ctx context.Context, cfg config, log *slog.Logger) error {
	fixtures, err := contracttest.LoadAPIFixtures(cfg.fixtures)
	if err != nil {
		return fmt.Errorf("load fixtures: %w", err)
	}
	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           NewHandler(Options{Fixtures: fixtures, AllowedOrigins: cfg.origins, StreamInterval: cfg.streamInterval}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Requests inherit ctx so open event streams end on shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	log.Info("fake daemon listening", "addr", cfg.addr, "operations", len(fixtures))
	select {
	case err := <-errs:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shut down: %w", err)
	}
	return nil
}
