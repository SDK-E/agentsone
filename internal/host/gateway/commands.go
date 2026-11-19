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

	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayconfig"
	"github.com/SDK-E/agentsone/internal/host/gateway/keychain"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// keyStore is what the commands need from the Keychain.
type keyStore interface {
	Get(ctx context.Context, p routing.ProviderID) (string, error)
	Has(ctx context.Context, p routing.ProviderID) (bool, error)
	Set(ctx context.Context, p routing.ProviderID, key string) error
}

type deps struct {
	keys     keyStore
	stdin    io.Reader
	terminal func() bool
}

// Commands returns the gateway commands.
func Commands(env *hostenv.Env) []*cobra.Command {
	return commands(env, deps{
		keys:     keychain.NewSystem(),
		stdin:    os.Stdin,
		terminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
	})
}

func commands(env *hostenv.Env, d deps) []*cobra.Command {
	root := &cobra.Command{
		Use:   "gateway",
		Short: "Manage model providers and the model gateway",
		Long: "The gateway serves one model name, autoagents/one, to agents in the VM at " +
			InternalName + ":4000\nand picks the real provider and model. Provider keys stay in the macOS Keychain.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
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
			RunE:  func(cmd *cobra.Command, _ []string) error { return listProviders(cmd, env, d) },
		},
		enableCommand(env, d, true),
		enableCommand(env, d, false),
	)
	key := &cobra.Command{
		Use:   "key",
		Short: "Store provider keys in the macOS Keychain",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	key.AddCommand(&cobra.Command{
		Use:   "set <provider>",
		Short: "Store a provider's API key, read from standard input",
		Long: "Reads the key from standard input and stores it in the macOS Keychain\n" +
			"(service \"agentsone\", account = provider id). The key never enters the VM.",
		Example: "  pbpaste | agentsone gateway key set openai",
		Args:    cobra.ExactArgs(1),
		RunE:    func(cmd *cobra.Command, args []string) error { return setKey(cmd, env, d, args[0]) },
	})
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

func listProviders(cmd *cobra.Command, env *hostenv.Env, d deps) error {
	f, err := gatewayconfig.Load(configPath(env))
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROVIDER\tENABLED\tKEY\tMODELS")
	for _, p := range f.Providers {
		keyState := "not needed"
		if p.KeyRequired {
			has, err := d.keys.Has(ctxOf(cmd), routing.ProviderID(p.ID))
			switch {
			case err != nil:
				keyState = "unknown"
			case has:
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

func enableCommand(env *hostenv.Env, d deps, on bool) *cobra.Command {
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
			if on {
				for _, p := range f.Providers {
					if p.ID != args[0] || !p.KeyRequired {
						continue
					}
					if has, err := d.keys.Has(ctxOf(cmd), routing.ProviderID(p.ID)); err == nil && !has {
						_, _ = fmt.Fprintf(out, "No key is stored yet. Store one with: agentsone gateway key set %s\n", p.ID)
					}
				}
			}
			return nil
		},
	}
}

func setKey(cmd *cobra.Command, env *hostenv.Env, d deps, provider string) error {
	f, err := gatewayconfig.Load(configPath(env))
	if err != nil {
		return err
	}
	known := false
	for _, p := range f.Providers {
		known = known || p.ID == provider
	}
	if !known {
		return fmt.Errorf("%w: %q", gatewayconfig.ErrUnknownProvider, provider)
	}
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
	if err := d.keys.Set(ctxOf(cmd), routing.ProviderID(provider), key); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Key for %s stored in the macOS Keychain.\n", provider)
	return err
}

func serveCommand(env *hostenv.Env) *cobra.Command {
	var listen string
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run the gateway on a local address (for tests and the control page)",
		Long: "Runs the gateway on a loopback address until interrupted. Inside the VM the gateway is\n" +
			"reached at " + InternalName + ":4000, wired by the host when the VM runs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, _, err := net.SplitHostPort(listen)
			if err != nil {
				return fmt.Errorf("gateway: --listen: %w", err)
			}
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				return fmt.Errorf("gateway: --listen must be a loopback address, got %q", listen)
			}
			ctx, stop := signal.NotifyContext(ctxOf(cmd), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// Start up fully even if interrupted at once, so Run can stop cleanly.
			startCtx := context.WithoutCancel(ctx)
			srv, err := Open(startCtx, Options{DataDir: env.DataDir, Log: env.Log})
			if err != nil {
				return err
			}
			ln, err := (&net.ListenConfig{}).Listen(startCtx, "tcp", listen)
			if err != nil {
				return errors.Join(fmt.Errorf("gateway: listen: %w", err), srv.Close())
			}
			env.Log.Info("gateway listening", "addr", ln.Addr().String())
			return srv.Run(ctx, nil, ln)
		},
	}
	c.Flags().StringVar(&listen, "listen", "127.0.0.1:4000", "loopback address to listen on")
	return c
}
