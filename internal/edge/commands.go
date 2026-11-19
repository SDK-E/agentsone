// SPDX-License-Identifier: MIT

// Package edge is the edge service of the Docker compose stack: the one place that talks to the
// internet and holds provider keys. It provides the edge command, which serves the edge.
//
// Owned by work package 15 (edge: network, freeze).
package edge

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/adminsock"
	"github.com/SDK-E/agentsone/internal/edge/audit"
	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/door"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
	"github.com/SDK-E/agentsone/internal/edge/freeze"
	"github.com/SDK-E/agentsone/internal/edge/gateway"
	edgenet "github.com/SDK-E/agentsone/internal/edge/net"
)

// Components are the parts of the edge, in start order. Add a part here; see package component.
// The audit sink comes first: it shares the proof recorder with every part after it. The internet
// filter shares the egress freeze; the freeze-all switch then takes its place for the later parts.
// The model gateway refuses to start without a freeze switch, so it starts after freeze and gets
// the freeze-all switch. The door, the humans' way in, opens last.
func Components() []component.Component {
	return []component.Component{
		audit.Component{},
		edgenet.Component{},
		freeze.Component{},
		gateway.Component{},
		door.Component{},
	}
}

// Commands returns the command that serves the edge.
func Commands(env *edgeenv.Env) []*cobra.Command {
	return []*cobra.Command{NewServeCommand(env, Components())}
}

// NewServeCommand builds `agentsone edge`, which serves components until SIGINT or SIGTERM.
func NewServeCommand(env *edgeenv.Env, components []component.Component) *cobra.Command {
	var innerAddr, dataDir, adminSocket string
	var doorAddrs []string
	cmd := &cobra.Command{
		Use:   "edge",
		Short: "Serve the edge",
		Long: "Serve the edge: the internet filter for the machine and the other edge parts. Every " +
			"part the machine reaches listens on the inner address only. The control container " +
			"reaches the edge through the admin socket.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if innerAddr == "" {
				return errors.New("--inner-addr is required: the edge's IPv4 address on the inner network")
			}
			addr, err := netip.ParseAddr(innerAddr)
			if err != nil {
				return fmt.Errorf("--inner-addr %q is not an IP address", innerAddr)
			}
			admin, err := adminsock.Listen(adminSocket)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return component.Run(ctx, &component.Env{
				InnerAddr: addr, DataDir: dataDir, ConfigDir: env.ConfigDir, Log: env.Log, DoorAddrs: doorAddrs,
			}, components, admin)
		},
	}
	f := cmd.Flags()
	f.StringVar(&innerAddr, "inner-addr", "", "the edge's IPv4 address on the inner network (required)")
	f.StringVar(&dataDir, "data-dir", env.DataDir, "the edge state folder")
	f.StringVar(&adminSocket, "admin-socket", adminsock.DefaultPath, "the admin socket the control container uses")
	f.StringArrayVar(&doorAddrs, "door-addr", nil, "a door address of the machine, host:port (repeat for more)")
	return cmd
}
