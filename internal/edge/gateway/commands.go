// SPDX-License-Identifier: MIT

package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/admin"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// keyClient is what the commands need from the admin socket.
type keyClient interface {
	SetKey(ctx context.Context, provider, key string) error
	DeleteKey(ctx context.Context, provider string) error
	Keys(ctx context.Context) (map[string]bool, error)
}

type deps struct {
	// admin connects to the admin socket at a path.
	admin    func(socket string) keyClient
	stdin    io.Reader
	terminal func() bool
}

// Commands returns the gateway commands.
func Commands(env *hostenv.Env) []*cobra.Command {
	return commands(env, deps{
		admin:    func(socket string) keyClient { return admin.NewClient(socket) },
		stdin:    os.Stdin,
		terminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
	})
}

func commands(env *hostenv.Env, d deps) []*cobra.Command {
	root := &cobra.Command{
		Use:   "gateway",
		Short: "Manage model providers and the model gateway",
		Long: "The gateway serves one model name, autoagents/one, to agents at " + InternalName + ":4000\n" +
			"and picks the real provider and model. Provider keys stay in the edge container.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var socket string
	root.PersistentFlags().StringVar(&socket, "admin-socket", "", "admin socket path (default from gateway.toml)")
	socketOf := func() (string, error) {
		if socket != "" {
			return socket, nil
		}
		f, err := gatewayconfig.Load(configPath(env))
		if err != nil {
			return "", err
		}
		return f.Server.AdminSocketPath(env.DataDir), nil
	}

	providers := &cobra.Command{
		Use:   "providers",
		Short: "List, enable and disable model providers",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	providers.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List providers, whether they are enabled, and whether their key is set",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return listProviders(cmd, env, keysOf(cmd, d, socketOf))
			},
		},
		enableCommand(env, d, socketOf, true),
		enableCommand(env, d, socketOf, false),
	)
	key := &cobra.Command{
		Use:   "key",
		Short: "Store and remove provider keys through the edge admin socket",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	key.AddCommand(
		&cobra.Command{
			Use:   "set <provider>",
			Short: "Store a provider's API key, read from standard input",
			Long: "Reads the key from standard input and sends it to the running edge over the admin\n" +
				"socket. The edge keeps it as a file only it can read. The key never reaches agents.",
			Example: "  agentsone gateway key set openai < openai.key",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return setKey(cmd, d, socketOf, args[0])
			},
		},
		&cobra.Command{
			Use:   "delete <provider>",
			Short: "Remove a provider's API key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				sock, err := socketOf()
				if err != nil {
					return err
				}
				if err := d.admin(sock).DeleteKey(ctxOf(cmd), args[0]); err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Key for %s removed.\n", args[0])
				return err
			},
		},
	)
	root.AddCommand(providers, key, serveCommand(env))
	return []*cobra.Command{root}
}

func configPath(env *hostenv.Env) string { return filepath.Join(env.DataDir, gatewayconfig.FileName) }

func ctxOf(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// keysOf asks the edge which providers have a key. Nil means the edge
// could not be asked.
func keysOf(cmd *cobra.Command, d deps, socketOf func() (string, error)) map[string]bool {
	sock, err := socketOf()
	if err != nil {
		return nil
	}
	keys, err := d.admin(sock).Keys(ctxOf(cmd))
	if err != nil {
		return nil
	}
	return keys
}

func listProviders(cmd *cobra.Command, env *hostenv.Env, keys map[string]bool) error {
	f, err := gatewayconfig.Load(configPath(env))
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROVIDER\tENABLED\tKEY\tMODELS")
	for _, p := range f.Providers {
		keyState := "not needed"
		if p.KeyRequired {
			switch {
			case keys == nil:
				keyState = "unknown (edge not running)"
			case keys[p.ID]:
				keyState = "set"
			default:
				keyState = "missing"
			}
		}
		models := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, m.ID+" ("+m.Tier+")")
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, yesNo(p.Enabled), keyState, strings.Join(models, ", "))
	}
	return tw.Flush()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func enableCommand(env *hostenv.Env, d deps, socketOf func() (string, error), on bool) *cobra.Command {
	verb, done := "enable", "enabled"
	if !on {
		verb, done = "disable", "disabled"
	}
	return &cobra.Command{
		Use:   verb + " <provider>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a provider for routing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configPath(env)
			f, err := gatewayconfig.Load(path)
			if err != nil {
				return err
			}
			if err := f.SetEnabled(args[0], on); err != nil {
				return err
			}
			if err := os.MkdirAll(env.DataDir, 0o700); err != nil {
				return fmt.Errorf("gateway: data folder: %w", err)
			}
			if err := gatewayconfig.Save(path, f); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%s %s. A running gateway picks this up within a few seconds.\n", args[0], done)
			if !on {
				return nil
			}
			for _, p := range f.Providers {
				if p.ID != args[0] || !p.KeyRequired {
					continue
				}
				if keys := keysOf(cmd, d, socketOf); keys != nil && !keys[p.ID] {
					_, _ = fmt.Fprintf(out, "No key is stored yet. Store one with: agentsone gateway key set %s\n", p.ID)
				}
			}
			return nil
		},
	}
}

func setKey(cmd *cobra.Command, d deps, socketOf func() (string, error), provider string) error {
	var key string
	if d.terminal() {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Paste the %s key and press Enter (it is not shown): ", provider)
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return fmt.Errorf("gateway: read key: %w", err)
		}
		key = string(raw)
	} else {
		line, err := bufio.NewReader(d.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("gateway: read key: %w", err)
		}
		key = line
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("gateway: no key on standard input")
	}
	sock, err := socketOf()
	if err != nil {
		return err
	}
	if err := d.admin(sock).SetKey(ctxOf(cmd), provider, key); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Key for %s stored in the edge.\n", provider)
	return err
}

func serveCommand(env *hostenv.Env) *cobra.Command {
	var listen string
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run the gateway and its admin socket",
		Long: "Runs the gateway until interrupted, on the address from gateway.toml ([server] listen,\n" +
			"default :4000) on the machine-side inner network, and opens the admin socket.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(ctxOf(cmd), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// Start up fully even if interrupted at once, so Run can stop cleanly.
			startCtx := context.WithoutCancel(ctx)
			srv, err := Open(startCtx, Options{DataDir: env.DataDir, Log: env.Log})
			if err != nil {
				return err
			}
			addr := listen
			if addr == "" {
				addr = srv.ListenAddr()
			}
			if _, _, err := net.SplitHostPort(addr); err != nil {
				return errors.Join(fmt.Errorf("gateway: --listen: %w", err), srv.Close())
			}
			gw, err := (&net.ListenConfig{}).Listen(startCtx, "tcp", addr)
			if err != nil {
				return errors.Join(fmt.Errorf("gateway: listen: %w", err), srv.Close())
			}
			adminLn, err := srv.ListenAdmin()
			if err != nil {
				return errors.Join(err, gw.Close(), srv.Close())
			}
			env.Log.Info("gateway listening", "addr", gw.Addr().String(), "admin_socket", srv.AdminSocketPath())
			return srv.Run(ctx, nil, Listeners{Gateway: gw, Admin: adminLn})
		},
	}
	c.Flags().StringVar(&listen, "listen", "", "address to listen on, host:port (default from gateway.toml)")
	return c
}
