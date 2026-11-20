// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SDK-E/agentsone/internal/contract/eventdata"
)

// Publication is an event a daemon module announces. Its data is a generated
// contract type, so the payload is the catalog's and the type cannot differ
// from it: the type comes from Data.EventType().
type Publication struct {
	// ID is the event id when the module fixed it before publishing, as an
	// outbox does so that a resend names the same event. Empty lets the bus
	// assign one. The events module takes an id it already stored as an
	// event already published: it stores nothing and returns no error.
	ID string
	// Source is the event source, for example "/daemon".
	Source  string
	Subject string
	// Actor is "<kind>:<name>", for example "agent:founder".
	Actor string
	// ChartScope is the part of the chart the event concerns, for example "team:founder".
	ChartScope string
	Data       eventdata.Data
}

// PublishData stores p on the bus and returns the stored event. Daemon modules
// use it instead of building a core.Event by hand; a test fails when one does.
func PublishData(ctx context.Context, bus EventPublisher, p Publication) (Event, error) {
	if p.Data == nil {
		return Event{}, fmt.Errorf("publish: %w: no event data", ErrInvalid)
	}
	raw, err := json.Marshal(p.Data)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s: %w", p.Data.EventType(), err)
	}
	e, err := bus.Publish(ctx, Event{
		ID: p.ID, SpecVersion: "1.0", Source: p.Source, Type: p.Data.EventType(), Subject: p.Subject,
		DataContentType: "application/json", Data: raw, Actor: p.Actor, ChartScope: p.ChartScope,
	})
	if err != nil {
		return Event{}, fmt.Errorf("publish %s: %w", p.Data.EventType(), err)
	}
	return e, nil
}
