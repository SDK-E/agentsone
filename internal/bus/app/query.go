// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Query selects stored messages.
type Query struct {
	Filter domain.MessageFilter
	// Before is the sequence to start below. Zero starts at the newest.
	Before uint64
	Limit  int
}

// MessagePage is one page of messages, newest first.
type MessagePage struct {
	Messages []domain.Message
	// Next is the sequence to pass as Before for the next page, or 0 at the end.
	Next uint64
}

// ListMessages returns stored messages the caller may see, newest first.
func (s *Service) ListMessages(ctx context.Context, q Query) (MessagePage, error) {
	if err := validateFilter(q.Filter); err != nil {
		return MessagePage{}, err
	}
	limit := clampLimit(q.Limit)
	seen := s.accessFor(s.viewerOf(ctx, callerOf(ctx)))
	var page MessagePage
	cursor := q.Before
	for {
		rows, err := s.d.Messages.Before(ctx, q.Filter, cursor, batch)
		if err != nil {
			return MessagePage{}, err
		}
		for _, m := range rows {
			cursor = m.Sequence
			if !seen.canSee(ctx, m) {
				continue
			}
			if len(page.Messages) == limit {
				page.Next = page.Messages[limit-1].Sequence
				return page, nil
			}
			page.Messages = append(page.Messages, m)
		}
		if len(rows) < batch {
			return page, nil
		}
	}
}

func validateFilter(f domain.MessageFilter) error {
	switch {
	case f.Channel != "" && !domain.ValidChannelName(f.Channel):
		return fmt.Errorf("%w: channel is not a channel name", core.ErrInvalid)
	case f.To != "" && !domain.ValidAgentName(f.To):
		return fmt.Errorf("%w: to is not an agent name", core.ErrInvalid)
	case f.From != "" && !domain.ValidActor(f.From):
		return fmt.Errorf("%w: from is not an actor", core.ErrInvalid)
	}
	return nil
}
