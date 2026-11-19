// SPDX-License-Identifier: MIT

package net

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// Commands returns the network commands.
func Commands(_ *hostenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "net", Short: "Show and change the internet allow list", RunE: hostenv.NotBuilt("15")},
	}
}
