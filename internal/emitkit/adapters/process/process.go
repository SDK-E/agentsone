// SPDX-License-Identifier: MIT

// Package process runs one built-in event source as a program: it reads the
// flags, sets up JSON logging on stderr (the journal of the emitter@<name>
// unit), connects to the daemon and runs the source until it is cancelled.
package process

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/emitkit/adapters/daemon"
	"github.com/SDK-E/agentsone/internal/emitkit/app"
)

const (
	publishAttempts = 4
	publishBackoff  = time.Second
)

// Timer is the real app.Sleeper.
type Timer struct{}

// Sleep waits d or until ctx is cancelled, whichever is first.
func (Timer) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run is the whole of a source's main. name is the emitter name (the part
// after /emitters/ in the event source). configure adds the source's own
// flags and returns the source; it is called before the flags are parsed, so
// it should return a source that reads the flag values when it runs.
// The result is the process exit code: 0 on a normal stop, 2 for bad flags,
// 1 when the source fails.
func Run(ctx context.Context, name string, args []string, stderr io.Writer, configure func(*flag.FlagSet) app.Source) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", daemon.DefaultSocket, "path of the daemon's Unix socket")
	src := configure(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil)).With("emitter", name)
	sleeper := Timer{}
	deps := app.Deps{
		Publisher: app.Retrying(daemon.New(*socket, name), sleeper, log, publishAttempts, publishBackoff),
		Clock:     core.SystemClock{},
		Sleeper:   sleeper,
		Log:       log,
	}
	log.InfoContext(ctx, "source starting", "socket", *socket)
	if err := src.Run(ctx, deps); err != nil {
		log.ErrorContext(ctx, "source stopped", "error", err)
		return 1
	}
	log.InfoContext(ctx, "source stopped")
	return 0
}
