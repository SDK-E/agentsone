// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"time"
)

// ChannelRepository stores channels.
type ChannelRepository interface {
	// Create stores c. A name that is taken is ErrNameTaken.
	Create(ctx context.Context, c Channel) error
	// Get returns the channel or core.ErrNotFound.
	Get(ctx context.Context, name string) (Channel, error)
	// List returns up to limit channels with a name after afterName, in name order.
	List(ctx context.Context, afterName string, limit int) ([]Channel, error)
	// Touch records the time of the newest message in the channel.
	Touch(ctx context.Context, name string, at time.Time) error
}

// MessageFilter selects stored messages. Every field that is set must match.
type MessageFilter struct {
	Channel string
	To      string
	From    string
	Since   *time.Time // keeps messages created at or after Since
}

// MessageRepository stores the message log. Sequences are assigned on append
// and only ever grow.
type MessageRepository interface {
	// Append stores m with the next sequence and returns it as stored. An id
	// that is already stored is a core.ErrConflict.
	Append(ctx context.Context, m Message) (Message, error)
	// Get returns the message or core.ErrNotFound.
	Get(ctx context.Context, id string) (Message, error)
	// Before returns up to limit messages that match f with a sequence smaller
	// than before (0 means no bound), newest first.
	Before(ctx context.Context, f MessageFilter, before uint64, limit int) ([]Message, error)
	// After returns up to limit messages with a sequence larger than after, oldest first.
	After(ctx context.Context, after uint64, limit int) ([]Message, error)
	// Last returns the largest stored sequence, or 0 when the log is empty.
	Last(ctx context.Context) (uint64, error)
}
