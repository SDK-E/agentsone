// SPDX-License-Identifier: MIT

package httpadapter

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

const (
	cloudEventsVersion = "1.0"
	jsonContentType    = "application/json"
)

// toEvent maps a stored event to the generated DTO. Field names of the wire
// format live only in the generated types, so a contract rename stays a
// change to this file.
func toEvent(e core.Event) api.Event {
	out := api.Event{
		Specversion:     cloudEventsVersion,
		Datacontenttype: jsonContentType,
		Id:              e.ID,
		Source:          e.Source,
		Type:            e.Type,
		Sequence:        e.Sequence,
		Actor:           e.Actor,
		Chartscope:      e.ChartScope,
		Data:            dataMap(e.Data),
	}
	if e.Time != nil {
		out.Time = stamp(*e.Time)
	}
	out.Subject = optional(e.Subject)
	out.Dataschema = optional(e.DataSchema)
	out.Causationid = optional(e.CausationID)
	out.Correlationid = optional(e.CorrelationID)
	return out
}

// dataMap decodes event data for the DTO. The app layer only stores JSON
// objects, so a decode failure yields an empty object.
func dataMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toEventList(events []core.Event, next string) api.EventList {
	items := make([]api.Event, 0, len(events))
	for _, e := range events {
		items = append(items, toEvent(e))
	}
	return api.EventList{Items: items, NextCursor: optional(next)}
}

// toDraft maps a publish request to a domain draft.
func toDraft(p api.EventPublish) (domain.Draft, error) {
	data, err := json.Marshal(p.Data)
	if err != nil {
		return domain.Draft{}, fmt.Errorf("%w: event data: %w", core.ErrInvalid, err)
	}
	d := domain.Draft{Type: p.Type, Data: data}
	if p.Source != nil {
		d.Source = *p.Source
	}
	if p.Subject != nil {
		d.Subject = *p.Subject
	}
	if p.Causationid != nil {
		d.CausationID = *p.Causationid
	}
	if p.Correlationid != nil {
		d.CorrelationID = *p.Correlationid
	}
	return d, nil
}

func toEmitter(e domain.Emitter) api.Emitter {
	return api.Emitter{
		Id: e.ID, Name: e.Name, Kind: api.EmitterKind(e.Kind), Description: e.Description,
		EventTypes: nonNil(e.EventTypes), Enabled: e.Enabled, Owner: e.Owner,
		Unit: optional(e.Unit), PluginId: optional(e.PluginID), WebhookPath: optional(e.WebhookPath),
		CreatedAt: stamp(e.CreatedAt), LastEventAt: stampPtr(e.LastEventAt),
	}
}

func toEmitterList(emitters []domain.Emitter, next string) api.EmitterList {
	items := make([]api.Emitter, 0, len(emitters))
	for _, e := range emitters {
		items = append(items, toEmitter(e))
	}
	return api.EmitterList{Items: items, NextCursor: optional(next)}
}

func toRegistration(r api.EmitterRegister) domain.Registration {
	reg := domain.Registration{
		Name: r.Name, Kind: domain.EmitterKind(r.Kind), Description: r.Description, EventTypes: r.EventTypes,
	}
	if r.Command != nil {
		reg.Command = *r.Command
	}
	return reg
}

// stamp converts a time to the contract's timestamp, which writes UTC.
func stamp(t time.Time) api.Timestamp { return api.Timestamp(t.UTC()) }

// stampPtr converts an optional time to an optional contract timestamp.
func stampPtr(t *time.Time) *api.Timestamp {
	if t == nil {
		return nil
	}
	s := stamp(*t)
	return &s
}

// timePtr converts an optional contract timestamp to an optional time.
func timePtr(s *api.Timestamp) *time.Time {
	if s == nil {
		return nil
	}
	t := time.Time(*s)
	return &t
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toFilter(f api.EventFilter) domain.Filter {
	out := domain.Filter{}
	if f.Types != nil {
		out.Types = *f.Types
	}
	if f.Sources != nil {
		out.Sources = *f.Sources
	}
	if f.SubjectPrefix != nil {
		out.SubjectPrefix = *f.SubjectPrefix
	}
	if f.Actors != nil {
		out.Actors = *f.Actors
	}
	return out
}

func fromFilter(f domain.Filter) api.EventFilter {
	out := api.EventFilter{}
	if len(f.Types) > 0 {
		out.Types = &f.Types
	}
	if len(f.Sources) > 0 {
		out.Sources = &f.Sources
	}
	if f.SubjectPrefix != "" {
		out.SubjectPrefix = &f.SubjectPrefix
	}
	if len(f.Actors) > 0 {
		out.Actors = &f.Actors
	}
	return out
}

// toAction maps the wake or create-task union to a domain action.
func toAction(a api.TriggerAction) (domain.Action, error) {
	kind, err := a.Discriminator()
	if err != nil {
		return domain.Action{}, fmt.Errorf("%w: trigger action: %w", core.ErrInvalid, err)
	}
	switch kind {
	case string(domain.ActionWake):
		w, err := a.AsWakeAction()
		if err != nil {
			return domain.Action{}, fmt.Errorf("%w: wake action: %w", core.ErrInvalid, err)
		}
		return domain.Action{Kind: domain.ActionWake, Agent: w.Agent, Message: w.Message}, nil
	case string(domain.ActionCreateTask):
		c, err := a.AsCreateTaskAction()
		if err != nil {
			return domain.Action{}, fmt.Errorf("%w: create-task action: %w", core.ErrInvalid, err)
		}
		return domain.Action{Kind: domain.ActionCreateTask, Agent: c.Assignee, Title: c.Title, Priority: string(c.Priority)}, nil
	}
	return domain.Action{}, fmt.Errorf("%w: unknown trigger action kind %q", core.ErrInvalid, kind)
}

func fromAction(a domain.Action) (api.TriggerAction, error) {
	var out api.TriggerAction
	var err error
	switch a.Kind {
	case domain.ActionCreateTask:
		err = out.FromCreateTaskAction(api.CreateTaskAction{
			Kind: api.CreateTaskActionKind(a.Kind), Assignee: a.Agent, Title: a.Title, Priority: api.TaskPriority(a.Priority),
		})
	default:
		err = out.FromWakeAction(api.WakeAction{Kind: api.WakeActionKind(a.Kind), Agent: a.Agent, Message: a.Message})
	}
	if err != nil {
		return api.TriggerAction{}, fmt.Errorf("encode trigger action: %w", err)
	}
	return out, nil
}

func toTrigger(t domain.Trigger) (api.Trigger, error) {
	action, err := fromAction(t.Action)
	if err != nil {
		return api.Trigger{}, err
	}
	return api.Trigger{
		Id: t.ID, Name: t.Name, Owner: t.Owner, Filter: fromFilter(t.Filter), Action: action,
		WakeRate: api.WakeRate{Max: t.Rate.Max, PerSeconds: t.Rate.PerSeconds}, Enabled: t.Enabled,
		FiredCount: t.FiredCount, DroppedCount: t.DroppedCount,
		CreatedAt: stamp(t.CreatedAt), LastFiredAt: stampPtr(t.LastFiredAt),
	}, nil
}

func toTriggerList(triggers []domain.Trigger, next string) (api.TriggerList, error) {
	items := make([]api.Trigger, 0, len(triggers))
	for _, t := range triggers {
		item, err := toTrigger(t)
		if err != nil {
			return api.TriggerList{}, err
		}
		items = append(items, item)
	}
	return api.TriggerList{Items: items, NextCursor: optional(next)}, nil
}

// toSpec maps a create request. A trigger is on unless the request says not.
func toSpec(c api.TriggerCreate) (domain.TriggerSpec, error) {
	action, err := toAction(c.Action)
	if err != nil {
		return domain.TriggerSpec{}, err
	}
	enabled := true
	if c.Enabled != nil {
		enabled = *c.Enabled
	}
	return domain.TriggerSpec{
		Name: c.Name, Filter: toFilter(c.Filter), Action: action,
		Rate:    domain.TriggerRate{Max: c.WakeRate.Max, PerSeconds: c.WakeRate.PerSeconds},
		Enabled: enabled,
	}, nil
}

// toPatch maps an update request; unset fields stay nil.
func toPatch(u api.TriggerUpdate) (domain.TriggerPatch, error) {
	p := domain.TriggerPatch{Name: u.Name, Enabled: u.Enabled}
	if u.Filter != nil {
		f := toFilter(*u.Filter)
		p.Filter = &f
	}
	if u.Action != nil {
		a, err := toAction(*u.Action)
		if err != nil {
			return domain.TriggerPatch{}, err
		}
		p.Action = &a
	}
	if u.WakeRate != nil {
		p.Rate = &domain.TriggerRate{Max: u.WakeRate.Max, PerSeconds: u.WakeRate.PerSeconds}
	}
	return p, nil
}
