// SPDX-License-Identifier: MIT

package control

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/edge/adminsock"
	"github.com/SDK-E/agentsone/internal/edge/control/adapters/argon2id"
	"github.com/SDK-E/agentsone/internal/edge/control/adapters/edgeadmin"
	"github.com/SDK-E/agentsone/internal/edge/control/adapters/web"
	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// DefaultListen is where the control page listens inside its container. The compose stack
// publishes it on the loopback address of the host.
const DefaultListen = ":8081"

// Commands returns the control page commands: `control` serves the page, `control reset` clears
// the passphrase.
func Commands(env *edgeenv.Env) []*cobra.Command {
	cmd := newServeCommand(env)
	cmd.AddCommand(newResetCommand(env))
	return []*cobra.Command{cmd}
}

func newServeCommand(env *edgeenv.Env) *cobra.Command {
	var listen, adminSocket string
	var trusted []string
	cmd := &cobra.Command{
		Use:   "control",
		Short: "Serve the control page",
		Long: "Serve the control page, where a human uses the human-only powers: the internet allow " +
			"list, model providers and keys, the budget and the defence share, and freeze all. The " +
			"page reaches the edge only through the admin socket. While no passphrase is set it " +
			"prints a one-time setup code to its log.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			proxies, err := parsePrefixes(trusted)
			if err != nil {
				return err
			}
			ln, err := (&net.ListenConfig{}).Listen(cmd.Context(), "tcp", listen)
			if err != nil {
				return fmt.Errorf("control page: %w", err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return Serve(ctx, ln, ServeOptions{
				Edge: edgeadmin.New(adminSocket, edgeadmin.DefaultTimeout), Hasher: argon2id.New(argon2id.DefaultParams),
				Log: env.Log, TrustedProxies: proxies,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&listen, "listen", DefaultListen, "the address the control page listens on")
	f.StringVar(&adminSocket, "admin-socket", adminsock.DefaultPath, "the edge admin socket")
	f.StringArrayVar(&trusted, "trusted-proxy", nil,
		"an address or network (CIDR) of a proxy trusted to say the visitor used TLS with X-Forwarded-Proto (repeat for more; default none)")
	return cmd
}

func parsePrefixes(in []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("--trusted-proxy %q is not an address or a network", s)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Edge is everything the control page needs from the edge: the admin socket client is one.
type Edge interface {
	app.Credentials
	app.Network
	app.Models
	app.Money
	app.Freeze
}

// ServeOptions configure Serve.
type ServeOptions struct {
	Edge           Edge
	Hasher         app.Hasher
	Log            *slog.Logger
	TrustedProxies []netip.Prefix
	// Clock defaults to the system clock.
	Clock app.Clock
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Serve serves the control page on ln until ctx ends. Serve closes ln.
func Serve(ctx context.Context, ln net.Listener, o ServeOptions) error {
	if o.Clock == nil {
		o.Clock = systemClock{}
	}
	auth := app.NewAuth(app.AuthOptions{Credentials: o.Edge, Hasher: o.Hasher, Clock: o.Clock, Entropy: rand.Reader, Log: o.Log})
	powers := app.NewPowers(app.PowersOptions{Network: o.Edge, Models: o.Edge, Money: o.Edge, Freeze: o.Edge})
	if err := auth.Prepare(ctx); err != nil {
		// The edge may still be starting. The first visit asks again and prints the code then.
		o.Log.Warn("control page: the edge did not answer yet; the setup code comes with the first visit", "err", err)
	}
	srv := &http.Server{
		Handler:           web.New(web.Options{Auth: auth, Powers: powers, Log: o.Log, TrustedProxies: o.TrustedProxies}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(o.Log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	o.Log.Info("control page serving", "addr", ln.Addr().String())
	var err error
	select {
	case <-ctx.Done():
	case err = <-served:
		return fmt.Errorf("control page: %w", err)
	}
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if serr := srv.Shutdown(shutCtx); serr != nil {
		err = fmt.Errorf("control page: stop: %w", serr)
	}
	if serveErr := <-served; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		err = errors.Join(err, serveErr)
	}
	return err
}

func newResetCommand(env *edgeenv.Env) *cobra.Command {
	var adminSocket string
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Clear the control page passphrase",
		Long: "Clear the control page passphrase, for when it is forgotten. Run it where the stack " +
			"runs: docker compose exec edge agentsone control reset. Everyone is signed out, and the " +
			"control page asks for a new setup code, printed in its log (docker compose logs control).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := edgeadmin.New(adminSocket, edgeadmin.DefaultTimeout).Reset(cmd.Context()); err != nil {
				return fmt.Errorf("reset the control passphrase: %w", err)
			}
			_, err := fmt.Fprintln(env.Out, "The control passphrase is cleared. Open the control page: it asks for "+
				"a new setup code, which is in the log of the control service (docker compose logs control).")
			return err
		},
	}
	cmd.Flags().StringVar(&adminSocket, "admin-socket", adminsock.DefaultPath, "the edge admin socket")
	return cmd
}
