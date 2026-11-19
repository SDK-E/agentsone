// SPDX-License-Identifier: MIT

// Package edge is the edge service of the Docker compose stack: the one place that talks to the
// internet and holds provider keys. It provides the edge command, which serves the edge.
//
// Owned by work package 15 (edge: network, freeze).
package edge

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// Commands returns the command that serves the edge.
func Commands(_ *edgeenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "edge", Short: "Serve the edge", RunE: edgeenv.NotBuilt("15")},
	}
}
