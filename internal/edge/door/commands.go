// SPDX-License-Identifier: MIT

package door

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// Commands returns the web door commands.
func Commands(_ *edgeenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "door", Short: "Open or close the HTTPS web door", RunE: edgeenv.NotBuilt("16")},
	}
}
