// SPDX-License-Identifier: MIT

// Command agentctl is the command line for the daemon API, used by humans
// and agents. It has one command per API operation, grouped by tag, built
// from the embedded contract, and `agentctl mcp` serves the same operations
// as MCP tools on stdio.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	stop()
	os.Exit(code)
}

// run executes agentctl and returns the exit code.
func run(ctx context.Context, args []string, stdio cli.IO) int {
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		// A contract that cannot be mapped is a build defect, caught by the parity test.
		_, _ = fmt.Fprintf(stdio.Err, "error: the embedded API description cannot be used: %v\n", err) //nolint:errcheck // stderr is the last place to report; the exit code still says failure
		return cli.ExitFailure
	}
	return cli.Run(ctx, cat, version, args, stdio, cli.WithTagDescriptions(api.Spec))
}
