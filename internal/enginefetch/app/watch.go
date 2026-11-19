// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// Watch keeps the engine installed until ctx ends. It runs Ensure at the
// start, then once more after each value on triggers (the licence-accepted
// events). Triggers that arrive while a run is pending fold into that run,
// so a burst of events causes one download. Every error other than
// ErrLicenceNotAccepted goes to onErr. A closed or nil triggers channel
// leaves only the first run; Watch still returns only when ctx ends.
func (f *Fetcher) Watch(ctx context.Context, triggers <-chan core.Event, onErr func(error)) {
	pending := make(chan struct{}, 1)
	pending <- struct{}{}
	var wg sync.WaitGroup
	wg.Go(func() { fold(ctx, triggers, pending) })
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pending:
			_, err := f.Ensure(ctx)
			if err != nil && ctx.Err() == nil && !errors.Is(err, ErrLicenceNotAccepted) {
				onErr(err)
			}
		}
	}
}

// fold turns every trigger into at most one pending run.
func fold(ctx context.Context, triggers <-chan core.Event, pending chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-triggers:
			if !ok {
				return
			}
			select {
			case pending <- struct{}{}:
			default: // a run is already pending; it covers this trigger
			}
		}
	}
}
