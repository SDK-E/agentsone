// SPDX-License-Identifier: MIT

// Command agentsone is the host tool on the Mac. It holds the human-only
// powers. Each host package adds its own commands; do not add logic here.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/budget"
	"github.com/SDK-E/agentsone/internal/edge/gateway"
	"github.com/SDK-E/agentsone/internal/host/audit"
	"github.com/SDK-E/agentsone/internal/host/control"
	"github.com/SDK-E/agentsone/internal/host/door"
	"github.com/SDK-E/agentsone/internal/host/hostenv"
	"github.com/SDK-E/agentsone/internal/host/net"
	"github.com/SDK-E/agentsone/internal/host/vm"
)

var version = "dev"

func main() {
	env, err := hostenv.New(os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentsone:", err)
		os.Exit(1)
	}
	if err := newRoot(env).Execute(); err != nil {
		os.Exit(1)
	}
}

func newRoot(env *hostenv.Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "agentsone",
		Short:         "Run and govern the Agents One VM from the Mac",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.SetOut(env.Out)
	root.SetErr(env.Err)
	for _, commands := range []func(*hostenv.Env) []*cobra.Command{
		vm.Commands, net.Commands, gateway.Commands, budget.Commands,
		audit.Commands, door.Commands, control.Commands,
	} {
		root.AddCommand(commands(env)...)
	}
	return root
}
