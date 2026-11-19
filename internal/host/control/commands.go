// SPDX-License-Identifier: MIT

package control

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// Commands returns the control page commands.
func Commands(_ *hostenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "control", Short: "Open the local control page", RunE: hostenv.NotBuilt("17")},
	}
}
