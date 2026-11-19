// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/app"
	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/contract/api/busapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// Service is the use cases the handlers call. *app.Service implements it.
type Service interface {
	CreateChannel(ctx context.Context, spec domain.ChannelSpec) (domain.Channel, error)
	GetChannel(ctx context.Context, name string) (domain.Channel, error)
	ListChannels(ctx context.Context, afterName string, limit int) (app.ChannelPage, error)
	Post(ctx context.Context, d domain.Draft) (domain.Message, error)
	ListMessages(ctx context.Context, q app.Query) (app.MessagePage, error)
	Stream(ctx context.Context, channel string, after *uint64) (<-chan domain.Message, error)
}

var _ Service = (*app.Service)(nil)

// Ticker makes a heartbeat source: a channel that ticks every d and a
// function that stops it.
type Ticker func(d time.Duration) (<-chan time.Time, func())

// Option changes a handler.
type Option func(*options)

type options struct {
	ticker Ticker
}

// WithTicker replaces the heartbeat ticker, for tests.
func WithTicker(t Ticker) Option { return func(o *options) { o.ticker = t } }

func systemTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// Bus serves the bus group.
type Bus struct {
	busapi.Unimplemented
	svc    Service
	clock  core.Clock
	ticker Ticker
}

var _ busapi.StrictServerInterface = (*Bus)(nil)

// NewBus returns the bus handler.
func NewBus(svc Service, clock core.Clock, opts ...Option) *Bus {
	o := options{ticker: systemTicker}
	for _, opt := range opts {
		opt(&o)
	}
	return &Bus{svc: svc, clock: clock, ticker: o.ticker}
}

// Group returns the bus API group over svc.
func Group(svc Service, clock core.Clock, opts ...Option) core.APIGroup {
	return busapi.Group(NewBus(svc, clock, opts...))
}

// ListChannels returns one page of the channels the caller may see.
func (h *Bus) ListChannels(ctx context.Context, req busapi.ListChannelsRequestObject) (busapi.ListChannelsResponseObject, error) {
	page, err := h.svc.ListChannels(ctx, valueOr(req.Params.Cursor, ""), valueOr(req.Params.Limit, 0))
	if err != nil {
		return nil, err
	}
	return busapi.ListChannels200JSONResponse(toChannelList(page.Channels, page.Next)), nil
}

// CreateChannel makes a channel.
func (h *Bus) CreateChannel(ctx context.Context, req busapi.CreateChannelRequestObject) (busapi.CreateChannelResponseObject, error) {
	c, err := h.svc.CreateChannel(ctx, toSpec(*req.Body))
	if err != nil {
		return nil, err
	}
	return busapi.CreateChannel201JSONResponse(toChannel(c)), nil
}

// GetChannel returns one channel.
func (h *Bus) GetChannel(ctx context.Context, req busapi.GetChannelRequestObject) (busapi.GetChannelResponseObject, error) {
	c, err := h.svc.GetChannel(ctx, req.ChannelName)
	if err != nil {
		return nil, err
	}
	return busapi.GetChannel200JSONResponse(toChannel(c)), nil
}

// ListBusMessages returns one page of messages, newest first.
func (h *Bus) ListBusMessages(ctx context.Context, req busapi.ListBusMessagesRequestObject) (busapi.ListBusMessagesResponseObject, error) {
	p := req.Params
	q := app.Query{
		Filter: domain.MessageFilter{
			Channel: valueOr(p.Channel, ""), To: valueOr(p.To, ""), From: valueOr(p.From, ""), Since: timePtr(p.Since),
		},
		Limit: valueOr(p.Limit, 0),
	}
	if p.Cursor != nil {
		before, err := parseSequence(*p.Cursor, "cursor")
		if err != nil {
			return nil, err
		}
		q.Before = before
	}
	page, err := h.svc.ListMessages(ctx, q)
	if err != nil {
		return nil, err
	}
	return busapi.ListBusMessages200JSONResponse(toMessageList(page.Messages, page.Next)), nil
}

// SendBusMessage sends a message to a channel or an agent.
func (h *Bus) SendBusMessage(ctx context.Context, req busapi.SendBusMessageRequestObject) (busapi.SendBusMessageResponseObject, error) {
	m, err := h.svc.Post(ctx, toDraft(*req.Body))
	if err != nil {
		return nil, err
	}
	return busapi.SendBusMessage201JSONResponse(toMessage(m)), nil
}

// StreamBus streams messages as they are sent, replaying from Last-Event-ID.
func (h *Bus) StreamBus(ctx context.Context, req busapi.StreamBusRequestObject) (busapi.StreamBusResponseObject, error) {
	var after *uint64
	if id := req.Params.LastEventID; id != nil && *id != "" {
		seq, err := parseSequence(*id, "Last-Event-ID")
		if err != nil {
			return nil, err
		}
		after = &seq
	}
	streamCtx, cancel := context.WithCancel(ctx)
	messages, err := h.svc.Stream(streamCtx, valueOr(req.Params.Channel, ""), after)
	if err != nil {
		cancel()
		return nil, err
	}
	ticks, stop := h.ticker(heartbeat)
	return busapi.StreamBus200TexteventStreamResponse{
		Body: newFrameReader(streamCtx, cancel, messages, ticks, stop, h.clock),
	}, nil
}
