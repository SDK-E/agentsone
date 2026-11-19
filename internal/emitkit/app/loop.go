// SPDX-License-Identifier: MIT

// Package app holds what every built-in event source shares: the ports it
// needs, a sampling loop, and a publisher wrapper that retries.
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

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

type retrying struct {
	next     Publisher
	sleeper  Sleeper
	log      *slog.Logger
	attempts int
	base     time.Duration
}

// Retrying wraps p so a transient failure is tried again, waiting base, then
// twice base and so on between attempts. A rejection is never retried.
func Retrying(p Publisher, s Sleeper, log *slog.Logger, attempts int, base time.Duration) Publisher {
	return &retrying{next: p, sleeper: s, log: log, attempts: max(attempts, 1), base: base}
}

func (r *retrying) Publish(ctx context.Context, d domain.Draft) error {
	wait := r.base
	var err error
	for attempt := 1; attempt <= r.attempts; attempt++ {
		if err = r.next.Publish(ctx, d); err == nil || errors.Is(err, ErrRejected) {
			return err
		}
		if attempt == r.attempts {
			break
		}
		r.log.WarnContext(ctx, "publish failed, will retry", "type", d.Type, "attempt", attempt, "error", err)
		if serr := r.sleeper.Sleep(ctx, wait); serr != nil {
			return serr
		}
		wait *= 2
	}
	return err
}
