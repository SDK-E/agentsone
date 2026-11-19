// SPDX-License-Identifier: MIT

package budget

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// Commands returns the budget commands.
func Commands(_ *edgeenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "budget", Short: "Show and set the total budget and the defence slice", RunE: edgeenv.NotBuilt("16")},
	}
}
