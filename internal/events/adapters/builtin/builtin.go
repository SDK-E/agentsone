// SPDX-License-Identifier: MIT

// Package builtin runs the built-in event sources inside the daemon. Each
// source is a goroutine of the events module; it publishes through the
// in-process event port as its own emitter (/emitters/<name>), so turning an
// emitter off in the API silences it. Lifecycle follows the module: Start
// launches the sources, Stop cancels them and waits.
package builtin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
	evdomain "github.com/SDK-E/agentsone/internal/events/domain"
)

// Publisher is the in-process app.Publisher of one emitter.
type Publisher struct {
	events  core.EventPublisher
	emitter string
}

var _ app.Publisher = (*Publisher)(nil)

// NewPublisher publishes through events as the emitter named emitter. The
// events service treats a call with no caller as the daemon's own.
func NewPublisher(events core.EventPublisher, emitter string) *Publisher {
	return &Publisher{events: events, emitter: emitter}
}

// Publish stores the draft. A disabled emitter is not an error: switching an
// emitter off is how an owner silences it.
func (p *Publisher) Publish(ctx context.Context, d domain.Draft) error {
	_, err := p.events.Publish(ctx, core.Event{
		Source:  "/emitters/" + p.emitter,
		Type:    d.Type,
		Subject: d.Subject,
		Data:    d.Data,
	})
	if errors.Is(err, evdomain.ErrEmitterDisabled) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("publish %s: %w", d.Type, err)
	}
	return nil
}

// Named is a source and the emitter name it publishes as.
type Named struct {
	Name   string
	Source app.Source
}

// Runner runs sources as goroutines.
type Runner struct {
	log     *slog.Logger
	clock   core.Clock
	sleeper app.Sleeper
	events  core.EventPublisher
	sources []Named

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRunner returns a runner for sources. The sources share the clock and
// sleeper; each gets its own publisher and logger.
func NewRunner(log *slog.Logger, clock core.Clock, sleeper app.Sleeper, events core.EventPublisher, sources []Named) *Runner {
	return &Runner{log: log, clock: clock, sleeper: sleeper, events: events, sources: sources}
}

// Start launches every source. It is safe to call again: a running runner
// stays as it is. The sources run until Stop, not until ctx ends, because
// ctx is the start-up context.
func (r *Runner) Start(_ context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	for _, s := range r.sources {
		log := r.log.With("emitter", s.Name)
		deps := app.Deps{
			Publisher: NewPublisher(r.events, s.Name),
			Clock:     r.clock,
			Sleeper:   r.sleeper,
			Log:       log,
		}
		r.wg.Go(func() {
			if err := s.Source.Run(ctx, deps); err != nil {
				log.ErrorContext(ctx, "event source stopped with an error", "error", err)
			}
		})
	}
}

// Stop cancels the sources and waits for them to end.
func (r *Runner) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	r.wg.Wait()
}
