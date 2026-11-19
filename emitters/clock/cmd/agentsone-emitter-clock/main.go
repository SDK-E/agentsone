// SPDX-License-Identifier: MIT

// Command agentsone-emitter-clock is the clock event source. It is installed
// as /usr/lib/agentsone/emitters/clock and run as emitter@clock.service.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/SDK-E/agentsone/emitters/clock"
	"github.com/SDK-E/agentsone/internal/emitkit/adapters/process"
	"github.com/SDK-E/agentsone/internal/emitkit/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := process.Run(ctx, "clock", os.Args[1:], os.Stderr, func(*flag.FlagSet) app.Source { return clock.New() })
	stop()
	os.Exit(code)
}
