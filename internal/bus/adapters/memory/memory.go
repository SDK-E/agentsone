// SPDX-License-Identifier: MIT

// Package memory holds in-memory bus repositories for tests and fakes. They
// pass the same contract suites as the SQLite ones.
package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Channels is an in-memory domain.ChannelRepository.
type Channels struct {
	mu       sync.Mutex
	channels map[string]domain.Channel
}

var _ domain.ChannelRepository = (*Channels)(nil)

// NewChannels returns an empty channel store.
func NewChannels() *Channels { return &Channels{channels: map[string]domain.Channel{}} }

// Create stores c.
func (r *Channels) Create(_ context.Context, c domain.Channel) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.channels[c.Name]; taken {
		return fmt.Errorf("channel %q: %w", c.Name, domain.ErrNameTaken)
	}
	r.channels[c.Name] = c
	return nil
}

// Get returns the channel called name.
func (r *Channels) Get(_ context.Context, name string) (domain.Channel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.channels[name]
	if !ok {
		return domain.Channel{}, fmt.Errorf("channel %q: %w", name, core.ErrNotFound)
	}
	return c, nil
}

// List returns up to limit channels after afterName, in name order.
func (r *Channels) List(_ context.Context, afterName string, limit int) ([]domain.Channel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Channel
	for _, c := range r.channels {
		if c.Name > afterName {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Channel) int { return cmp.Compare(a.Name, b.Name) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Touch records the time of the newest message.
func (r *Channels) Touch(_ context.Context, name string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.channels[name]
	if !ok {
		return fmt.Errorf("channel %q: %w", name, core.ErrNotFound)
	}
	c.LastMessageAt = &at
	r.channels[name] = c
	return nil
}

// Messages is an in-memory domain.MessageRepository.
type Messages struct {
	mu       sync.Mutex
	messages []domain.Message // index i holds sequence i+1
}

var _ domain.MessageRepository = (*Messages)(nil)

// NewMessages returns an empty message log.
func NewMessages() *Messages { return &Messages{} }

// Append stores m with the next sequence.
func (r *Messages) Append(_ context.Context, m domain.Message) (domain.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, stored := range r.messages {
		if stored.ID == m.ID {
			return domain.Message{}, fmt.Errorf("message %s: %w", m.ID, core.ErrConflict)
		}
	}
	m.Sequence = uint64(len(r.messages)) + 1
	r.messages = append(r.messages, m)
	return m, nil
}

// Get returns the message with id.
func (r *Messages) Get(_ context.Context, id string) (domain.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.messages {
		if m.ID == id {
			return m, nil
		}
	}
	return domain.Message{}, fmt.Errorf("message %s: %w", id, core.ErrNotFound)
}

// Before returns up to limit matching messages below sequence before, newest first.
func (r *Messages) Before(_ context.Context, f domain.MessageFilter, before uint64, limit int) ([]domain.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Message
	for i := len(r.messages) - 1; i >= 0 && len(out) < limit; i-- {
		m := r.messages[i]
		if before != 0 && m.Sequence >= before {
			continue
		}
		if matches(f, m) {
			out = append(out, m)
		}
	}
	return out, nil
}

func matches(f domain.MessageFilter, m domain.Message) bool {
	switch {
	case f.Channel != "" && m.Channel != f.Channel:
		return false
	case f.To != "" && m.To != f.To:
		return false
	case f.From != "" && m.From != f.From:
		return false
	case f.Since != nil && m.CreatedAt.Before(*f.Since):
		return false
	}
	return true
}

// After returns up to limit messages after sequence after, oldest first.
func (r *Messages) After(_ context.Context, after uint64, limit int) ([]domain.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if after >= uint64(len(r.messages)) {
		return nil, nil
	}
	rest := r.messages[after:]
	if len(rest) > limit {
		rest = rest[:limit]
	}
	return slices.Clone(rest), nil
}

// Last returns the largest stored sequence.
func (r *Messages) Last(context.Context) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.messages)), nil
}
