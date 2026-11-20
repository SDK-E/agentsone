// SPDX-License-Identifier: MIT

// Package app holds what every built-in event source shares: the ports it
// needs, a sampling loop and a real timer. Sources run inside the daemon, as
// goroutines of the events module.
package app

import (
	"context"
	"log/slog"
	"time"
)

// Timer is the real Sleeper.
type Timer struct{}

var _ Sleeper = Timer{}

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

// Every runs step now and then once per interval until ctx is cancelled,
// which is a normal end and returns nil. A failing step is logged and the
// loop goes on: a source must outlive a bad sample.
func Every(ctx context.Context, s Sleeper, interval time.Duration, log *slog.Logger, step func(context.Context) error) error {
	for ctx.Err() == nil {
		if err := step(ctx); err != nil && ctx.Err() == nil {
			log.WarnContext(ctx, "sample failed", "error", err)
		}
		if err := s.Sleep(ctx, interval); err != nil {
			return nil //nolint:nilerr // a cancelled sleep is the normal way a source stops
		}
	}
	return nil
}
