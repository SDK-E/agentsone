// SPDX-License-Identifier: MIT

package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	"github.com/SDK-E/agentsone/internal/edge/adminsock"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// adminClient is what the commands need from the edge admin API.
type adminClient interface {
	Providers(ctx context.Context) ([]adminapi.Provider, error)
	AddProvider(ctx context.Context, id string) (adminapi.Provider, error)
	RemoveProvider(ctx context.Context, id string) error
	SetKey(ctx context.Context, provider, key string) (adminapi.KeyState, error)
	ClearKey(ctx context.Context, provider string) error
}

type deps struct {
	// admin connects to the admin socket at a path.
	admin    func(socket string) adminClient
	stdin    io.Reader
	terminal func() bool
}

// Commands returns the gateway commands. They talk to the running edge through its admin socket.
func Commands(env *edgeenv.Env) []*cobra.Command {
	return commands(env, deps{
		admin:    func(socket string) adminClient { return adminapi.NewClient(socket) },
		stdin:    os.Stdin,
		terminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
	})
}

func commands(_ *edgeenv.Env, d deps) []*cobra.Command {
	root := &cobra.Command{
		Use:   "gateway",
		Short: "Manage model providers and their keys",
		Long: "The gateway serves one model name, autoagents/one, to agents at " + InternalName + ":4000\n" +
			"and picks the real provider and model. Provider keys stay in the edge container.\n" +
			"These commands talk to the running edge through its admin socket.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var socket string
	root.PersistentFlags().StringVar(&socket, "admin-socket", adminsock.DefaultPath, "the edge admin socket")
	client := func() adminClient { return d.admin(socket) }

	providers := &cobra.Command{
		Use:   "providers",
		Short: "List, add and remove model providers",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	providers.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List providers, whether they are added, and whether their key is set",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				list, err := client().Providers(cmd.Context())
				if err != nil {
					return err
				}
				return listProviders(cmd.OutOrStdout(), list)
			},
		},
		&cobra.Command{
			Use:   "add <provider>",
			Short: "Add a provider: the gateway may route calls to it",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := client().AddProvider(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				msg := fmt.Sprintf("%s added. The gateway routes calls to it now.\n", p.ID)
				if p.KeyRequired && !p.Key.Set {
					msg += fmt.Sprintf("No key is stored yet. Store one with: agentsone gateway key set %s\n", p.ID)
				}
				_, err = io.WriteString(cmd.OutOrStdout(), msg)
				return err
			},
		},
		&cobra.Command{
			Use:   "remove <provider>",
			Short: "Remove a provider from routing; its key stays stored",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := client().RemoveProvider(cmd.Context(), args[0]); err != nil {
					return err
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s removed. The gateway no longer routes calls to it.\n", args[0])
				return err
			},
		},
	)
	key := &cobra.Command{
		Use:   "key",
		Short: "Store and clear provider keys",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	key.AddCommand(
		&cobra.Command{
			Use:   "set <provider>",
			Short: "Store a provider's API key, read from standard input",
			Long: "Reads the key from standard input and sends it to the running edge over the admin\n" +
				"socket. The edge keeps it as a file only it can read. The key is never shown again\n" +
				"and never reaches agents; only a short fingerprint tells keys apart.",
			Example: "  agentsone gateway key set openai < openai.key",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				k, err := readKey(cmd, d, args[0])
				if err != nil {
					return err
				}
				ks, err := client().SetKey(cmd.Context(), args[0], k)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Key for %s stored in the edge (fingerprint %s).\n", args[0], ks.Fingerprint)
				return err
			},
		},
		&cobra.Command{
			Use:   "clear <provider>",
			Short: "Remove a provider's API key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := client().ClearKey(cmd.Context(), args[0]); err != nil {
					return err
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Key for %s removed.\n", args[0])
				return err
			},
		},
	)
	root.AddCommand(providers, key)
	return []*cobra.Command{root}
}

func listProviders(w io.Writer, list []adminapi.Provider) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROVIDER\tADDED\tKEY\tMODELS") //nolint:errcheck // tabwriter buffers; Flush below reports write failures
	for _, p := range list {
		keyState := "not needed"
		switch {
		case p.Key.Set:
			keyState = "set (" + p.Key.Fingerprint + ")"
		case p.KeyRequired:
			keyState = "missing"
		}
		models := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, m.ID+" ("+m.Tier+")")
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, yesNo(p.Enabled), keyState, strings.Join(models, ", ")) //nolint:errcheck // tabwriter buffers; Flush below reports write failures
	}
	return tw.Flush()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func readKey(cmd *cobra.Command, d deps, provider string) (string, error) {
	var key string
	if d.terminal() {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Paste the %s key and press Enter (it is not shown): ", provider) //nolint:errcheck // a prompt on the terminal; ReadPassword below reports a broken terminal
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		_, _ = fmt.Fprintln(cmd.ErrOrStderr()) //nolint:errcheck // ends the prompt line; the key is already read
		if err != nil {
			return "", fmt.Errorf("gateway: read key: %w", err)
		}
		key = string(raw)
	} else {
		line, err := bufio.NewReader(d.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("gateway: read key: %w", err)
		}
		key = line
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("gateway: no key on standard input")
	}
	return key, nil
}
