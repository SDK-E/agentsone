// SPDX-License-Identifier: MIT

package budget

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	"github.com/SDK-E/agentsone/internal/edge/adminsock"
	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// adminClient is what the commands need from the edge admin API.
type adminClient interface {
	Budget(ctx context.Context) (adminapi.Budget, error)
	SetBudget(ctx context.Context, r adminapi.SetBudgetRequest) (adminapi.Budget, error)
	SetDefence(ctx context.Context, r adminapi.SetDefenceRequest) (adminapi.Budget, error)
}

// Commands returns the budget commands. They talk to the running edge through its admin socket.
func Commands(env *edgeenv.Env) []*cobra.Command {
	return commands(env, func(socket string) adminClient { return adminapi.NewClient(socket) })
}

func commands(_ *edgeenv.Env, connect func(socket string) adminClient) []*cobra.Command {
	root := &cobra.Command{
		Use:   "budget",
		Short: "Show and set the total budget, its period and the defence slice",
		Long: "The total budget is a hard wall held in the edge: no model call starts that would pass it.\n" +
			"It lasts one period (a month, a week or a day, on the UTC calendar); then spending starts\n" +
			"again from zero. The defence slice is a share of the total kept for wakers and security work.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var socket string
	root.PersistentFlags().StringVar(&socket, "admin-socket", adminsock.DefaultPath, "the edge admin socket")
	client := func() adminClient { return connect(socket) }

	root.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show the budget, what is spent and what is left",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				b, err := client().Budget(cmd.Context())
				if err != nil {
					return err
				}
				show(cmd.OutOrStdout(), b)
				return nil
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
				micros := int64(total)
				if _, err := client().SetBudget(cmd.Context(), adminapi.SetBudgetRequest{TotalMicros: &micros}); err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Total budget set to %v.\n", total)
				return err
			},
		},
		&cobra.Command{
			Use:     "period month|week|day",
			Short:   "Set how long the total lasts before spending starts again from zero",
			Example: "  agentsone budget period week",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				p := args[0]
				if _, err := client().SetBudget(cmd.Context(), adminapi.SetBudgetRequest{Period: &p}); err != nil {
					return err
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Budget period set to one %s.\n", p)
				return err
			},
		},
		defenceCommand(client),
	)
	return []*cobra.Command{root}
}

func defenceCommand(client func() adminClient) *cobra.Command {
	return &cobra.Command{
		Use:   "defence on|off|share <pct>",
		Short: "Switch the defence slice on or off, or set its share of the total",
		Example: "  agentsone budget defence on\n" +
			"  agentsone budget defence share 20",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch {
			case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
				on := args[0] == "on"
				if _, err := client().SetDefence(cmd.Context(), adminapi.SetDefenceRequest{Enabled: &on}); err != nil {
					return err
				}
				_, err := fmt.Fprintf(out, "Defence slice switched %s.\n", args[0])
				return err
			case len(args) == 2 && args[0] == "share":
				pct, err := strconv.Atoi(args[1])
				if err != nil {
					return fmt.Errorf("share must be a whole percentage from 0 to 100: %q", args[1])
				}
				if _, err := client().SetDefence(cmd.Context(), adminapi.SetDefenceRequest{SharePct: &pct}); err != nil {
					return err
				}
				_, err = fmt.Fprintf(out, "Defence slice set to %d%% of the total.\n", pct)
				return err
			default:
				return errors.New("use: defence on, defence off, or defence share <pct>")
			}
		},
	}
}

func show(w io.Writer, b adminapi.Budget) {
	usd := func(m int64) ledger.Micros { return ledger.Micros(m) }
	defence := "off"
	if b.Defence.Enabled {
		defence = fmt.Sprintf("on, %d%% (%v kept for wakers and security work)", b.Defence.SharePct, usd(b.Defence.ReservedMicros))
	} else if b.Defence.SharePct > 0 {
		defence = fmt.Sprintf("off (share set to %d%%)", b.Defence.SharePct)
	}
	_, _ = fmt.Fprintf(w, "Total budget:     %v (hard wall)\n", usd(b.TotalMicros))
	_, _ = fmt.Fprintf(w, "Period:           one %s, %s to %s (UTC)\n", b.Period,
		b.PeriodStart.UTC().Format("2 Jan 2006"), b.PeriodEnd.UTC().Format("2 Jan 2006"))
	_, _ = fmt.Fprintf(w, "Spent:            %v\n", usd(b.SpentMicros))
	_, _ = fmt.Fprintf(w, "Left for agents:  %v\n", usd(b.RemainingMicros))
	_, _ = fmt.Fprintf(w, "Left for defence: %v\n", usd(b.Defence.RemainingMicros))
	_, _ = fmt.Fprintf(w, "Defence slice:    %s\n", defence)
	if b.Exhausted {
		_, _ = fmt.Fprintln(w, "Model calls from agents are refused until the budget is raised or the next period starts.")
	}
}
