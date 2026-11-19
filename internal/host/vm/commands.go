// SPDX-License-Identifier: MIT

package vm

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

const workPackage = "15"

// Commands returns the VM commands.
func Commands(_ *hostenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "up", Short: "Start the VM", RunE: hostenv.NotBuilt(workPackage)},
		{Use: "down", Short: "Shut the VM down", RunE: hostenv.NotBuilt(workPackage)},
		{Use: "status", Short: "Show VM state", RunE: hostenv.NotBuilt(workPackage)},
		{Use: "freeze", Short: "Freeze all agents, one team or one agent, or release", RunE: hostenv.NotBuilt(workPackage)},
		{Use: "rescue", Short: "Restore the last good boot copy", RunE: hostenv.NotBuilt(workPackage)},
	}
}
