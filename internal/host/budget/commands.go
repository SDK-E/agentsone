// SPDX-License-Identifier: MIT

package budget

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/host/budget/budgetapp"
	"github.com/SDK-E/agentsone/internal/host/budget/budgetstore"
	"github.com/SDK-E/agentsone/internal/host/budget/ledger"
	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// Commands returns the budget commands.
func Commands(env *hostenv.Env) []*cobra.Command {
	root := &cobra.Command{
		Use:   "budget",
		Short: "Show and set the total budget and the defence slice",
		Long: "The total budget is a hard wall held on the Mac: no model call starts that would pass it.\n" +
			"The defence slice is a share of the total kept for wakers and security work.\n" +
			"A running gateway picks up changes within one flush interval (5 seconds by default).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show the budget, what is spent and what is left",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withService(cmd.Context(), env, func(s *budgetapp.Service) error {
					show(cmd.OutOrStdout(), s.State())
					return nil
				})
			},
		},
		&cobra.Command{
			Use:     "set <usd>",
			Short:   "Set the total budget in US dollars, for example 25 or 25.50",
			Example: "  agentsone budget set 25",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				total, err := ledger.ParseUSD(args[0])
				if err != nil {
					return err
				}
				return withService(cmd.Context(), env, func(s *budgetapp.Service) error {
					if err := s.SetTotal(cmd.Context(), total); err != nil {
						return err
					}
					_, err := fmt.Fprintf(cmd.OutOrStdout(), "Total budget set to %v.\n", total)
					return err
				})
			},
		},
		defenceCommand(env),
	)
	return []*cobra.Command{root}
}

func defenceCommand(env *hostenv.Env) *cobra.Command {
	d := &cobra.Command{
		Use:   "defence on|off|share <pct>",
		Short: "Switch the defence slice on or off, or set its share of the total",
		Example: "  agentsone budget defence on\n" +
			"  agentsone budget defence share 20",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			switch {
			case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
				on := args[0] == "on"
				return withService(ctx, env, func(s *budgetapp.Service) error {
					if err := s.SetDefence(ctx, on); err != nil {
						return err
					}
					_, err := fmt.Fprintf(out, "Defence slice switched %s.\n", args[0])
					return err
				})
			case len(args) == 2 && args[0] == "share":
				pct, err := strconv.Atoi(args[1])
				if err != nil {
					return fmt.Errorf("share must be a whole percentage from 0 to 100: %q", args[1])
				}
				return withService(ctx, env, func(s *budgetapp.Service) error {
					if err := s.SetDefenceShare(ctx, pct); err != nil {
						return err
					}
					_, err := fmt.Fprintf(out, "Defence slice set to %d%% of the total.\n", pct)
					return err
				})
			default:
				return errors.New("use: defence on, defence off, or defence share <pct>")
			}
		},
	}
	return d
}

func withService(ctx context.Context, env *hostenv.Env, fn func(*budgetapp.Service) error) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(env.DataDir, 0o700); err != nil {
		return fmt.Errorf("budget: data folder: %w", err)
	}
	store, err := budgetstore.Open(ctx, filepath.Join(env.DataDir, budgetstore.FileName))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	svc, err := budgetapp.Open(ctx, store)
	if err != nil {
		return err
	}
	return fn(svc)
}

func show(w io.Writer, st ledger.State) {
	defence := "off"
	if st.DefenceOn {
		defence = fmt.Sprintf("on, %d%% (%v kept for wakers and security work)", st.DefenceSharePct, st.DefenceReserve)
	} else if st.DefenceSharePct > 0 {
		defence = fmt.Sprintf("off (share set to %d%%)", st.DefenceSharePct)
	}
	_, _ = fmt.Fprintf(w, "Total budget:     %v (hard wall)\n", st.Total)
	_, _ = fmt.Fprintf(w, "Spent:            %v\n", st.Spent)
	_, _ = fmt.Fprintf(w, "Left for agents:  %v\n", st.Remaining)
	_, _ = fmt.Fprintf(w, "Left for defence: %v\n", st.DefenceRemaining)
	_, _ = fmt.Fprintf(w, "Defence slice:    %s\n", defence)
	if st.Exhausted {
		_, _ = fmt.Fprintln(w, "Model calls from agents are refused until the budget is raised.")
	}
}
