// SPDX-License-Identifier: MIT

// Command agentsone-emitter-disk is the disk event source. It is installed as
// /usr/lib/agentsone/emitters/disk and run as emitter@disk.service.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/SDK-E/agentsone/emitters/disk"
	"github.com/SDK-E/agentsone/internal/emitkit/adapters/process"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := process.Run(ctx, "disk", os.Args[1:], os.Stderr, disk.Flags)
	stop()
	os.Exit(code)
}
