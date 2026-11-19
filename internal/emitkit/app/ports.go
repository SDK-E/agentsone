// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

// ErrRejected says the daemon refused an event and asking again cannot help:
// the event is wrong, or this source may not publish it.
var ErrRejected = errors.New("the daemon rejected the event")

// Publisher sends one event to the bus.
type Publisher interface {
	Publish(ctx context.Context, d domain.Draft) error
}

// Sleeper waits. The real one uses a timer; tests move a fake clock instead.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// Deps is everything a source runs with.
type Deps struct {
	Publisher Publisher
	Clock     core.Clock
	Sleeper   Sleeper
	Log       *slog.Logger
}

// Source is one built-in event source. Run returns nil when ctx is cancelled.
type Source interface {
	Run(ctx context.Context, d Deps) error
}
