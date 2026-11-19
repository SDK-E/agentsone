// SPDX-License-Identifier: MIT

package control

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// Commands returns the control page commands.
func Commands(_ *edgeenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "control", Short: "Serve the control page", RunE: edgeenv.NotBuilt("17")},
	}
}
