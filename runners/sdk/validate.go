// SPDX-License-Identifier: MIT

package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// errParams is wrapped by every parameter fault; the SDK answers -32602.
var errParams = errors.New("invalid params")

var (
	turnIDPattern = regexp.MustCompile(`^trn_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

	cancelReasons   = []string{"human", "manager", "freeze", "timeout", "superseded"}
	sleepReasons    = []string{"idle", "budget", "freeze", "shutdown"}
	shutdownReasons = []string{"stop", "restart", "update", "freeze", "power", "retire"}
)

func paramsErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errParams, fmt.Sprintf(format, args...))
}

// objectKeys checks that raw is a JSON object whose keys are all known and
// whose required keys are all present (the protocol's objects are closed).
func objectKeys(raw json.RawMessage, what string, required, optional []string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return paramsErr("%s must be an object", what)
	}
	for k := range obj {
		if !slices.Contains(required, k) && !slices.Contains(optional, k) {
			return paramsErr("%s has unknown field %q", what, k)
		}
	}
	for _, k := range required {
		if _, ok := obj[k]; !ok {
			return paramsErr("%s needs field %q", what, k)
		}
	}
	return nil
}

func decodeInto(raw json.RawMessage, what string, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(v); err != nil {
		return paramsErr("%s: %v", what, err)
	}
	return nil
}

func oneOf(what, v string, allowed []string) error {
	if !slices.Contains(allowed, v) {
		return paramsErr("%s %q is not one of %v", what, v, allowed)
	}
	return nil
}

func checkTurnID(id string) error {
	if !turnIDPattern.MatchString(id) {
		return paramsErr("turnId %q is not a turn id", id)
	}
	return nil
}

// parseWake validates wake params.
func parseWake(raw json.RawMessage) (Wake, error) {
	if err := objectKeys(raw, "wake params", []string{"reason", "turnId", "payload"}, nil); err != nil {
		return Wake{}, err
	}
	var p struct {
		Reason  WakeReason      `json:"reason"`
		TurnID  string          `json:"turnId"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := decodeInto(raw, "wake params", &p); err != nil {
		return Wake{}, err
	}
	if err := checkTurnID(p.TurnID); err != nil {
		return Wake{}, err
	}
	w := Wake{Reason: p.Reason, TurnID: p.TurnID}
	var err error
	switch p.Reason {
	case WakeChat:
		w.Chat, err = parsePayload[ChatMessage](p.Payload, "chat payload",
			[]string{"id", "chatId", "from", "text", "sentAt"}, []string{"routedBy"})
	case WakeMessage:
		w.Message, err = parsePayload[BusMessage](p.Payload, "message payload",
			[]string{"id", "from", "to", "subject", "body", "sentAt"}, []string{"replyTo", "correlationId"})
	case WakeTask:
		w.Task, err = parsePayload[Task](p.Payload, "task payload",
			[]string{"id", "title", "body", "from", "createdAt"}, []string{"due", "correlationId"})
	case WakeTimer:
		w.Timer, err = parsePayload[Timer](p.Payload, "timer payload",
			[]string{"scheduleId", "name", "due", "firedAt"}, []string{"note"})
	case WakeEvent:
		w.Event, err = parsePayload[Event](p.Payload, "event payload",
			[]string{"specversion", "id", "source", "type", "time", "data", "sequence", "actor", "chartscope", "datacontenttype"},
			[]string{"subject", "dataschema", "causationid", "correlationid"})
	default:
		err = paramsErr("wake reason %q is unknown", p.Reason)
	}
	return w, err
}

func parsePayload[T any](raw json.RawMessage, what string, required, optional []string) (*T, error) {
	if err := objectKeys(raw, what, required, optional); err != nil {
		return nil, err
	}
	var v T
	if err := decodeInto(raw, what, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

type deliverParams struct {
	turnID   string
	delivery Delivery
}

func parseDeliver(raw json.RawMessage) (deliverParams, error) {
	if err := objectKeys(raw, "deliver params", []string{"kind", "turnId", "message"}, nil); err != nil {
		return deliverParams{}, err
	}
	var p struct {
		Kind    DeliveryKind    `json:"kind"`
		TurnID  string          `json:"turnId"`
		Message json.RawMessage `json:"message"`
	}
	if err := decodeInto(raw, "deliver params", &p); err != nil {
		return deliverParams{}, err
	}
	if err := checkTurnID(p.TurnID); err != nil {
		return deliverParams{}, err
	}
	out := deliverParams{turnID: p.TurnID, delivery: Delivery{Kind: p.Kind}}
	var err error
	switch p.Kind {
	case DeliverChat:
		out.delivery.Chat, err = parsePayload[ChatMessage](p.Message, "chat message",
			[]string{"id", "chatId", "from", "text", "sentAt"}, []string{"routedBy"})
	case DeliverBus:
		out.delivery.Bus, err = parsePayload[BusMessage](p.Message, "bus message",
			[]string{"id", "from", "to", "subject", "body", "sentAt"}, []string{"replyTo", "correlationId"})
	default:
		err = paramsErr("deliver kind %q is unknown", p.Kind)
	}
	return out, err
}

type cancelParams struct {
	turnID string
	reason string
}

func parseCancel(raw json.RawMessage) (cancelParams, error) {
	if err := objectKeys(raw, "cancel params", []string{"turnId", "reason"}, nil); err != nil {
		return cancelParams{}, err
	}
	var p struct {
		TurnID string `json:"turnId"`
		Reason string `json:"reason"`
	}
	if err := decodeInto(raw, "cancel params", &p); err != nil {
		return cancelParams{}, err
	}
	if err := checkTurnID(p.TurnID); err != nil {
		return cancelParams{}, err
	}
	if err := oneOf("cancel reason", p.Reason, cancelReasons); err != nil {
		return cancelParams{}, err
	}
	return cancelParams{turnID: p.TurnID, reason: p.Reason}, nil
}

func parseSleep(raw json.RawMessage) (SleepRequest, error) {
	if err := objectKeys(raw, "sleep params", []string{"reason", "maxSummaryChars"}, nil); err != nil {
		return SleepRequest{}, err
	}
	var req SleepRequest
	var p struct {
		Reason          string `json:"reason"`
		MaxSummaryChars int    `json:"maxSummaryChars"`
	}
	if err := decodeInto(raw, "sleep params", &p); err != nil {
		return req, err
	}
	if err := oneOf("sleep reason", p.Reason, sleepReasons); err != nil {
		return req, err
	}
	if p.MaxSummaryChars < 256 || p.MaxSummaryChars > 65536 {
		return req, paramsErr("maxSummaryChars %d is outside 256 to 65536", p.MaxSummaryChars)
	}
	return SleepRequest{Reason: p.Reason, MaxSummaryChars: p.MaxSummaryChars}, nil
}

type shutdownParams struct {
	reason  string
	graceMs int
}

func parseShutdown(raw json.RawMessage) (shutdownParams, error) {
	if err := objectKeys(raw, "shutdown params", []string{"reason", "graceMs"}, nil); err != nil {
		return shutdownParams{}, err
	}
	var p struct {
		Reason  string `json:"reason"`
		GraceMs int    `json:"graceMs"`
	}
	if err := decodeInto(raw, "shutdown params", &p); err != nil {
		return shutdownParams{}, err
	}
	if err := oneOf("shutdown reason", p.Reason, shutdownReasons); err != nil {
		return shutdownParams{}, err
	}
	if p.GraceMs < 0 {
		return shutdownParams{}, paramsErr("graceMs %d is negative", p.GraceMs)
	}
	return shutdownParams{reason: p.Reason, graceMs: p.GraceMs}, nil
}
