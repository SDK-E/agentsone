// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// ErrEventNotFound is returned for an outbox event id the society never
// stored.
var ErrEventNotFound = fmt.Errorf("outbox event: %w", core.ErrNotFound)

// Outbound is an event the society stored together with the change it
// announces, waiting to be published. Agents and events live in different
// stores, so the event is written in the society's own transaction and
// published after it commits. ID is fixed when it is written: a resend after
// a crash names the same event, and the events store refuses a second copy.
type Outbound struct {
	ID         string
	Source     string
	Actor      string
	ChartScope string
	Type       string
	Data       json.RawMessage
	CreatedAt  time.Time
}

// OutboxRepository stores society changes with the events that announce
// them.
type OutboxRepository interface {
	// HireAgent stores a and ev in one transaction: both or neither. It
	// returns ErrNameTaken when the name exists.
	HireAgent(ctx context.Context, a Agent, ev Outbound) error
	// PendingEvents returns up to limit events not marked sent, oldest first.
	PendingEvents(ctx context.Context, limit int) ([]Outbound, error)
	// MarkSent records that the event was published. Marking it again
	// changes nothing; an unknown id returns ErrEventNotFound.
	MarkSent(ctx context.Context, id string, at time.Time) error
}
