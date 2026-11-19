// SPDX-License-Identifier: MIT

package net

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// Commands returns the network commands: `net allowlist list|add|remove`, working on the allow
// list in the edge data folder.
func Commands(env *edgeenv.Env) []*cobra.Command {
	return []*cobra.Command{NewNetCommand(env.DataDir, env.Out)}
}
