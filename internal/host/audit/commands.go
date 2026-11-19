// SPDX-License-Identifier: MIT

package audit

import (
	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// Commands returns the audit commands.
func Commands(_ *hostenv.Env) []*cobra.Command {
	return []*cobra.Command{
		{Use: "audit", Short: "Show and verify the audit log", RunE: hostenv.NotBuilt("16")},
	}
}
