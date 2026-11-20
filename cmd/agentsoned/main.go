// SPDX-License-Identifier: MIT

// Command agentsoned is the Agents One daemon. It loads the config, opens
// the stores, runs every module and serves the API on the Unix socket and
// on loopback TCP. Builders fill their own internal package, not this one.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/core"
)

func main() {
	configPath := flag.String("config", core.ConfigPath, "path to config.toml")
	flag.Parse()
	if err := run(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "agentsoned:", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return err
	}
	log, err := newLogger(cfg.Log)
	if err != nil {
		return err
	}
	// Decide once, before any agent runs, whether custom agents get hard
	// limits (a cgroup each) or soft ones, and say so on one log line.
	limits := process.ProbeSystem()
	limits.Log(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stores, closeStores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := closeStores(); err != nil {
			log.Error("close databases at shutdown", "error", err)
		}
	}()
	clock := core.SystemClock{}
	return serve(ctx, core.Deps{
		Config: &cfg,
		Log:    log,
		Clock:  clock,
		IDs:    core.SystemIDs(),
		Stores: stores,
		Ports:  core.NewPorts(),
	}, limits.Root)
}

func newLogger(c core.LogConfig) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", c.Level, err)
	}
	opts := &slog.HandlerOptions{Level: level}
	switch c.Format {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil
	default:
		return nil, fmt.Errorf("log format %q: want text or json", c.Format)
	}
}
