// SPDX-License-Identifier: MIT

package net

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// NewNetCommand builds `agentsone net` with its `allowlist list|add|remove` children, working on
// the allow list in dataDir and writing to out.
func NewNetCommand(dataDir string, out io.Writer) *cobra.Command {
	netCmd := &cobra.Command{
		Use:   "net",
		Short: "The machine network: what the machine may reach on the internet",
		Args:  cobra.NoArgs,
	}
	allow := &cobra.Command{
		Use:   "allowlist",
		Short: "Show and change the names the machine may reach",
		Long: "The machine may reach only the names on the allow list. Changes apply to a running " +
			"machine as soon as they are saved.\n\nThis limits reach. It does not stop data leaving " +
			"through a site that is allowed.",
		Args: cobra.NoArgs,
	}
	allow.AddCommand(listCommand(dataDir, out), addCommand(dataDir, out), removeCommand(dataDir, out))
	netCmd.AddCommand(allow)
	return netCmd
}

func listCommand(dataDir string, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the allowed names and why each is there",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, err := NewAllowListEditor(dataDir).List(cmd.Context())
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				_, err := fmt.Fprintln(out, "The allow list is empty: the machine can reach no site on the internet.")
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "NAME\tWHY")
			for _, e := range entries {
				_, _ = fmt.Fprintf(tw, "%s\t%s\n", e.Pattern, e.Why)
			}
			return tw.Flush()
		},
	}
}

func addCommand(dataDir string, out io.Writer) *cobra.Command {
	var why string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Allow a name, exact (github.com) or every name below one (*.github.com)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := NewAllowListEditor(dataDir).Add(cmd.Context(), args[0], why)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "Added %s to the allow list.\n", e.Pattern)
			return err
		},
	}
	cmd.Flags().StringVar(&why, "why", "", "why the machine needs this name (shown on the control page)")
	return cmd
}

func removeCommand(dataDir string, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Stop allowing a name; open connections to it are not cut, new ones are refused",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := NewAllowListEditor(dataDir).Remove(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, err := fmt.Fprintf(out, "Removed %s from the allow list.\n", args[0])
			return err
		},
	}
}
