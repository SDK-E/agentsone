// SPDX-License-Identifier: MIT

package door

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// Commands returns the web door commands.
func Commands(_ *hostenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "door", Short: "Open or close the HTTPS web door", RunE: hostenv.NotBuilt("16")},
	}
}
