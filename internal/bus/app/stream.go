// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Stream returns messages the caller may see as they are sent. A channel
// narrows the stream to that channel. With after set, stored messages after
// that sequence come first.
func (s *Service) Stream(ctx context.Context, channel string, after *uint64) (<-chan domain.Message, error) {
	if channel != "" && !domain.ValidChannelName(channel) {
		return nil, fmt.Errorf("%w: channel is not a channel name", core.ErrInvalid)
	}
	seen := s.accessFor(s.viewerOf(ctx, callerOf(ctx)))
	match := func(m domain.Message) bool {
		return (channel == "" || m.Channel == channel) && seen.canSee(ctx, m)
	}

	s.mu.Lock()
	cutoff, err := s.d.Messages.Last(ctx)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	live := s.hub.add()
	s.mu.Unlock()

	out := make(chan domain.Message, 16)
	go s.pump(ctx, live, match, after, cutoff, out)
	return out, nil
}

// pump sends the replay, then the live messages, to out until ctx ends or
// the hub drops the subscriber.
func (s *Service) pump(ctx context.Context, live *subscriber, match func(domain.Message) bool, after *uint64, cutoff uint64, out chan<- domain.Message) {
	defer close(out)
	defer s.hub.remove(live)
	send := func(m domain.Message) bool {
		select {
		case out <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if after != nil && !s.replay(ctx, match, *after, cutoff, send) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-live.ch:
			if !ok || (match(m) && !send(m)) {
				return
			}
		}
	}
}

// replay sends stored messages in (after, cutoff]. It reports false when the
// stream must end.
func (s *Service) replay(ctx context.Context, match func(domain.Message) bool, after, cutoff uint64, send func(domain.Message) bool) bool {
	cursor := after
	for cursor < cutoff {
		rows, err := s.d.Messages.After(ctx, cursor, batch)
		if err != nil {
			s.d.Log.WarnContext(ctx, "bus replay failed", "error", err)
			return false
		}
		if len(rows) == 0 {
			return true
		}
		for _, m := range rows {
			if m.Sequence > cutoff {
				return true
			}
			cursor = m.Sequence
			if match(m) && !send(m) {
				return false
			}
		}
	}
	return true
}
