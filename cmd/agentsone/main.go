// SPDX-License-Identifier: MIT

// Command agentsone is the edge binary of the Docker compose stack. It holds the human-only
// powers. Each edge package adds its own commands; do not add logic here.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge"
	"github.com/SDK-E/agentsone/internal/edge/audit"
	"github.com/SDK-E/agentsone/internal/edge/budget"
	"github.com/SDK-E/agentsone/internal/edge/control"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
	"github.com/SDK-E/agentsone/internal/edge/gateway"
	"github.com/SDK-E/agentsone/internal/edge/net"
)

var version = "dev"

func main() {
	env := edgeenv.New(os.Stdout, os.Stderr)
	if err := newRoot(env).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "agentsone:", err)
		os.Exit(1)
	}
}

func newRoot(env *edgeenv.Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "agentsone",
		Short:         "Serve the Agents One edge and control page",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(env.Out)
	root.SetErr(env.Err)
	for _, commands := range []func(*edgeenv.Env) []*cobra.Command{
		edge.Commands, control.Commands, net.Commands, gateway.Commands, budget.Commands,
		audit.Commands,
	} {
		root.AddCommand(commands(env)...)
	}
	return root
}
