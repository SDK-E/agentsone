// SPDX-License-Identifier: MIT

// Command agentctl is the command line for the daemon API, used by humans
// and agents. Work package 8 fills it, generated from contract/dist/openapi.json.
package main

import (
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRoot().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:          "agentctl",
		Short:        "Talk to the Agents One daemon",
		Version:      version,
		SilenceUsage: true,
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the agentctl version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(version)
		},
	})
	return root
}
