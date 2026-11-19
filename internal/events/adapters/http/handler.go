// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/eventsapi"
	"github.com/SDK-E/agentsone/internal/contract/api/triggersapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

const defaultLimit = 50

// Service is the use cases the handlers call. *app.Service implements it.
type Service interface {
	PublishDraft(ctx context.Context, d domain.Draft) (core.Event, error)
	List(ctx context.Context, q app.Query) (app.Page, error)
	Stream(ctx context.Context, f domain.Filter, after *uint64) (<-chan core.Event, error)
	ListEmitters(ctx context.Context, afterID string, limit int) ([]domain.Emitter, error)
	RegisterEmitter(ctx context.Context, reg domain.Registration) (app.Registered, error)
	GetEmitter(ctx context.Context, id string) (domain.Emitter, error)
	EnableEmitter(ctx context.Context, id string) (domain.Emitter, error)
	DisableEmitter(ctx context.Context, id string) (domain.Emitter, error)
	ReceiveWebhook(ctx context.Context, w app.Webhook) (core.Event, error)
	CreateTrigger(ctx context.Context, spec domain.TriggerSpec) (domain.Trigger, error)
	GetTrigger(ctx context.Context, id string) (domain.Trigger, error)
	ListTriggers(ctx context.Context, owner, afterID string, limit int) ([]domain.Trigger, bool, error)
	UpdateTrigger(ctx context.Context, id string, p domain.TriggerPatch) (domain.Trigger, error)
	DeleteTrigger(ctx context.Context, id string) error
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

// Events serves the events group.
type Events struct {
	eventsapi.Unimplemented
	svc    Service
	clock  core.Clock
	ticker Ticker
}

var _ eventsapi.StrictServerInterface = (*Events)(nil)

// NewEvents returns the events handler.
func NewEvents(svc Service, clock core.Clock, opts ...Option) *Events {
	o := options{ticker: systemTicker}
	for _, opt := range opts {
		opt(&o)
	}
	return &Events{svc: svc, clock: clock, ticker: o.ticker}
}

// mapError turns domain errors that the core does not know into problems.
func mapError(err error) error {
	if errors.Is(err, domain.ErrTooLarge) {
		return core.NewProblem(http.StatusRequestEntityTooLarge, "payload-too-large", err.Error())
	}
	return err
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// ListEmitters returns one page of emitters.
func (h *Events) ListEmitters(ctx context.Context, req eventsapi.ListEmittersRequestObject) (eventsapi.ListEmittersResponseObject, error) {
	limit := valueOr(req.Params.Limit, defaultLimit)
	list, err := h.svc.ListEmitters(ctx, valueOr(req.Params.Cursor, ""), limit+1)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].ID
	}
	return eventsapi.ListEmitters200JSONResponse(toEmitterList(list, next)), nil
}

// RegisterEmitter adds a process or webhook emitter.
func (h *Events) RegisterEmitter(ctx context.Context, req eventsapi.RegisterEmitterRequestObject) (eventsapi.RegisterEmitterResponseObject, error) {
	got, err := h.svc.RegisterEmitter(ctx, toRegistration(*req.Body))
	if err != nil {
		return nil, err
	}
	out := api.EmitterRegistered{Emitter: toEmitter(got.Emitter)}
	if got.Secret != "" {
		out.WebhookSecret = &got.Secret
	}
	return eventsapi.RegisterEmitter201JSONResponse(out), nil
}

// GetEmitter returns one emitter.
func (h *Events) GetEmitter(ctx context.Context, req eventsapi.GetEmitterRequestObject) (eventsapi.GetEmitterResponseObject, error) {
	e, err := h.svc.GetEmitter(ctx, req.EmitterId)
	if err != nil {
		return nil, err
	}
	return eventsapi.GetEmitter200JSONResponse(toEmitter(e)), nil
}

// EnableEmitter turns an emitter on.
func (h *Events) EnableEmitter(ctx context.Context, req eventsapi.EnableEmitterRequestObject) (eventsapi.EnableEmitterResponseObject, error) {
	e, err := h.svc.EnableEmitter(ctx, req.EmitterId)
	if err != nil {
		return nil, err
	}
	return eventsapi.EnableEmitter200JSONResponse(toEmitter(e)), nil
}

// DisableEmitter turns an emitter off.
func (h *Events) DisableEmitter(ctx context.Context, req eventsapi.DisableEmitterRequestObject) (eventsapi.DisableEmitterResponseObject, error) {
	e, err := h.svc.DisableEmitter(ctx, req.EmitterId)
	if err != nil {
		return nil, err
	}
	return eventsapi.DisableEmitter200JSONResponse(toEmitter(e)), nil
}

// ListEvents queries stored events.
func (h *Events) ListEvents(ctx context.Context, req eventsapi.ListEventsRequestObject) (eventsapi.ListEventsResponseObject, error) {
	p := req.Params
	f := domain.Filter{SubjectPrefix: valueOr(p.Subject, "")}
	if p.Type != nil {
		f.Types = []string{*p.Type}
	}
	if p.Source != nil {
		f.Sources = []string{*p.Source}
	}
	if p.Actor != nil {
		f.Actors = []string{*p.Actor}
	}
	f.Since, f.Until = timePtr(p.Since), timePtr(p.Until)
	after, err := afterSequence(p.AfterSequence, p.Cursor)
	if err != nil {
		return nil, err
	}
	page, err := h.svc.List(ctx, app.Query{Filter: f, After: after, Limit: valueOr(p.Limit, defaultLimit)})
	if err != nil {
		return nil, err
	}
	return eventsapi.ListEvents200JSONResponse(toEventList(page.Events, page.Next)), nil
}

// afterSequence picks the later of the after_sequence parameter and the
// sequence a cursor carries.
func afterSequence(param *int64, cursor *string) (uint64, error) {
	var after uint64
	if param != nil {
		if *param < 0 {
			return 0, fmt.Errorf("%w: after_sequence must not be negative", core.ErrInvalid)
		}
		after = uint64(*param)
	}
	if cursor != nil {
		c, err := domain.ParseSequence(*cursor)
		if err != nil {
			return 0, fmt.Errorf("%w: cursor is not valid", core.ErrInvalid)
		}
		after = max(after, c)
	}
	return after, nil
}

// PublishEvent stores an event published by the caller.
func (h *Events) PublishEvent(ctx context.Context, req eventsapi.PublishEventRequestObject) (eventsapi.PublishEventResponseObject, error) {
	draft, err := toDraft(*req.Body)
	if err != nil {
		return nil, err
	}
	e, err := h.svc.PublishDraft(ctx, draft)
	if err != nil {
		return nil, mapError(err)
	}
	return eventsapi.PublishEvent201JSONResponse(toEvent(e)), nil
}

// StreamEvents streams events as they happen, replaying from Last-Event-ID.
func (h *Events) StreamEvents(ctx context.Context, req eventsapi.StreamEventsRequestObject) (eventsapi.StreamEventsResponseObject, error) {
	var f domain.Filter
	if req.Params.Type != nil {
		f.Types = *req.Params.Type
	}
	var after *uint64
	if id := req.Params.LastEventID; id != nil && *id != "" {
		seq, err := domain.ParseSequence(*id)
		if err != nil {
			return nil, fmt.Errorf("%w: Last-Event-ID is not an event sequence", core.ErrInvalid)
		}
		after = &seq
	}
	streamCtx, cancel := context.WithCancel(ctx)
	events, err := h.svc.Stream(streamCtx, f, after)
	if err != nil {
		cancel()
		return nil, err
	}
	ticks, stop := h.ticker(heartbeat)
	return eventsapi.StreamEvents200TexteventStreamResponse{
		Body: newFrameReader(streamCtx, cancel, events, ticks, stop, h.clock),
	}, nil
}

// Triggers serves the triggers group.
type Triggers struct {
	triggersapi.Unimplemented
	svc Service
}

var _ triggersapi.StrictServerInterface = (*Triggers)(nil)

// NewTriggers returns the triggers handler.
func NewTriggers(svc Service) *Triggers { return &Triggers{svc: svc} }

// ListTriggers returns one page of the triggers the caller may see.
func (h *Triggers) ListTriggers(ctx context.Context, req triggersapi.ListTriggersRequestObject) (triggersapi.ListTriggersResponseObject, error) {
	limit := valueOr(req.Params.Limit, defaultLimit)
	list, more, err := h.svc.ListTriggers(ctx, valueOr(req.Params.Owner, ""), valueOr(req.Params.Cursor, ""), limit)
	if err != nil {
		return nil, err
	}
	next := ""
	if more && len(list) > 0 {
		next = list[len(list)-1].ID
	}
	out, err := toTriggerList(list, next)
	if err != nil {
		return nil, err
	}
	return triggersapi.ListTriggers200JSONResponse(out), nil
}

// CreateTrigger stores a trigger.
func (h *Triggers) CreateTrigger(ctx context.Context, req triggersapi.CreateTriggerRequestObject) (triggersapi.CreateTriggerResponseObject, error) {
	spec, err := toSpec(*req.Body)
	if err != nil {
		return nil, err
	}
	t, err := h.svc.CreateTrigger(ctx, spec)
	if err != nil {
		return nil, err
	}
	out, err := toTrigger(t)
	if err != nil {
		return nil, err
	}
	return triggersapi.CreateTrigger201JSONResponse(out), nil
}

// GetTrigger returns one trigger.
func (h *Triggers) GetTrigger(ctx context.Context, req triggersapi.GetTriggerRequestObject) (triggersapi.GetTriggerResponseObject, error) {
	t, err := h.svc.GetTrigger(ctx, req.TriggerId)
	if err != nil {
		return nil, err
	}
	out, err := toTrigger(t)
	if err != nil {
		return nil, err
	}
	return triggersapi.GetTrigger200JSONResponse(out), nil
}

// UpdateTrigger changes the fields the request sets.
func (h *Triggers) UpdateTrigger(ctx context.Context, req triggersapi.UpdateTriggerRequestObject) (triggersapi.UpdateTriggerResponseObject, error) {
	patch, err := toPatch(*req.Body)
	if err != nil {
		return nil, err
	}
	t, err := h.svc.UpdateTrigger(ctx, req.TriggerId, patch)
	if err != nil {
		return nil, err
	}
	out, err := toTrigger(t)
	if err != nil {
		return nil, err
	}
	return triggersapi.UpdateTrigger200JSONResponse(out), nil
}

// DeleteTrigger removes a trigger.
func (h *Triggers) DeleteTrigger(ctx context.Context, req triggersapi.DeleteTriggerRequestObject) (triggersapi.DeleteTriggerResponseObject, error) {
	if err := h.svc.DeleteTrigger(ctx, req.TriggerId); err != nil {
		return nil, err
	}
	return triggersapi.DeleteTrigger204Response{}, nil
}
