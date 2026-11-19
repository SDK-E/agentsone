// SPDX-License-Identifier: MIT

// Package clock is the built-in clock event source. It publishes
// agentsone.clock.tick at the start of every minute. Missed minutes (a
// suspended machine, a stopped unit) are not made up: a tick says "this
// minute began", and a late one would lie about it.
//
// agentsone.clock.schedule.fired is not published here; the daemon's
// scheduler owns it.
package clock

import (
	"context"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

const minuteLayout = "2006-01-02T15:04:00Z"

// Source is the clock source.
type Source struct{}

var _ app.Source = Source{}

// New returns the clock source.
func New() Source { return Source{} }

// Run waits for each minute boundary and publishes its tick until ctx is
// cancelled.
func (Source) Run(ctx context.Context, d app.Deps) error {
	for {
		now := d.Clock.Now().UTC()
		next := now.Truncate(time.Minute).Add(time.Minute)
		if err := d.Sleeper.Sleep(ctx, next.Sub(now)); err != nil {
			return nil //nolint:nilerr // a cancelled sleep is the normal way a source stops
		}
		draft, err := domain.NewDraft("agentsone.clock.tick", "", map[string]string{"minute": next.Format(minuteLayout)})
		if err != nil {
			return err
		}
		if err := d.Publisher.Publish(ctx, draft); err != nil && ctx.Err() == nil {
			d.Log.WarnContext(ctx, "tick not published", "minute", next.Format(minuteLayout), "error", err)
		}
	}
}
