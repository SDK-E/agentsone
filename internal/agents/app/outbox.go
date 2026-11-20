// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
)

// flushBatch is how many outbox events one flush reads at a time.
const flushBatch = 100

// SetLog sets where the society reports events it could not send yet.
func (s *Society) SetLog(l *slog.Logger) { s.log = l }

// outbound is ev as an outbox event, with its id fixed now.
func (s *Society) outbound(actorRef string, ev event) (domain.Outbound, error) {
	raw, err := json.Marshal(ev.data)
	if err != nil {
		return domain.Outbound{}, fmt.Errorf("encode %s: %w", ev.data.EventType(), err)
	}
	return domain.Outbound{
		ID: s.ids.New("evt"), Source: ev.source, Actor: actorRef, ChartScope: ev.scope,
		Type: ev.data.EventType(), Data: raw, CreatedAt: s.clock.Now(),
	}, nil
}

// Flush publishes the events stored with society changes and not sent yet,
// oldest first, for example after a restart. Each event keeps the id it was
// stored with, and the events module takes an id it already holds as
// published, so an event is published once. Flush stops at the first
// event it cannot send and returns why; the rest wait for the next flush.
func (s *Society) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flush(ctx)
}

// flushAfterChange sends what a committed change stored. The change stands
// either way, so a failure is logged and the event waits for the next flush.
// Call it with mu held.
func (s *Society) flushAfterChange(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	if err := s.flush(ctx); err != nil {
		s.log.WarnContext(ctx, "agents: an event waits in the outbox", "error", err)
	}
}

// flush is Flush with mu held.
func (s *Society) flush(ctx context.Context) error {
	for {
		pending, err := s.repo.PendingEvents(ctx, flushBatch)
		if err != nil {
			return fmt.Errorf("read the outbox: %w", err)
		}
		for _, ev := range pending {
			if err := s.send(ctx, ev); err != nil {
				return err
			}
		}
		if len(pending) < flushBatch {
			return nil
		}
	}
}

// send publishes ev as the society's emitter and marks it sent.
func (s *Society) send(ctx context.Context, ev domain.Outbound) error {
	data, err := decodeData(ev.Type, ev.Data)
	if err != nil {
		return fmt.Errorf("outbox event %s: %w", ev.ID, err)
	}
	_, err = core.PublishData(core.WithCaller(ctx, emitter), s.ports.Events, core.Publication{
		ID: ev.ID, Source: ev.Source, Actor: ev.Actor, ChartScope: ev.ChartScope, Data: data,
	})
	if err != nil {
		return fmt.Errorf("send outbox event %s: %w", ev.ID, err)
	}
	if err := s.repo.MarkSent(ctx, ev.ID, s.clock.Now()); err != nil {
		return fmt.Errorf("mark outbox event %s sent: %w", ev.ID, err)
	}
	return nil
}

// decodeData reads the stored data of an outbox event back into its
// generated type, so it is published through core.PublishData like any
// other society event.
func decodeData(typ string, raw json.RawMessage) (eventdata.Data, error) {
	switch typ {
	case eventdata.TypeAgentsHired:
		var d eventdata.AgentsHired
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("decode %s: %w", typ, err)
		}
		return d, nil
	default:
		return nil, fmt.Errorf("%w: the outbox does not carry %s events", core.ErrInvalid, typ)
	}
}
