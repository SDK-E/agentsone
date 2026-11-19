// SPDX-License-Identifier: MIT

package gateway

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// Commands returns the gateway commands.
func Commands(_ *edgeenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "gateway", Short: "Manage model providers and the model gateway", RunE: edgeenv.NotBuilt("16")},
	}
}
