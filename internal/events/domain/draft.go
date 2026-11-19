// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// MaxDataBytes is the largest event data, encoded.
const MaxDataBytes = 64 << 10

const (
	maxTypeLen    = 128
	maxSubjectLen = 1024
)

var (
	typePattern   = regexp.MustCompile(`^agentsone\.[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
	anyIDPattern  = regexp.MustCompile(`^[a-z]{2,4}_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	namePattern   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	pluginName    = regexp.MustCompile(`^plg_[0-9A-HJKMNP-TV-Z]{26}$`)
	sourcePattern = regexp.MustCompile(`^/(daemon|host|emitters/[a-z][a-z0-9-]{0,63}|agents/[a-z][a-z0-9-]{0,30})(/[A-Za-z0-9._~-]+)*$`)
)

// Draft is what a publisher supplies. Everything else is filled by the bus.
type Draft struct {
	Type          string
	Source        string // empty: the caller's own source
	Subject       string
	Data          json.RawMessage
	CausationID   string
	CorrelationID string
}

// Validate checks the rules that need no other data.
func (d Draft) Validate() error {
	if len(d.Type) > maxTypeLen || !typePattern.MatchString(d.Type) {
		return ErrInvalidType
	}
	if err := validateData(d.Data); err != nil {
		return err
	}
	if len(d.Subject) > maxSubjectLen {
		return ErrInvalidSubject
	}
	for _, id := range []string{d.CausationID, d.CorrelationID} {
		if id != "" && !anyIDPattern.MatchString(id) {
			return ErrInvalidLink
		}
	}
	if d.Source != "" {
		return ValidateSource(d.Source)
	}
	return nil
}

func validateData(data json.RawMessage) error {
	if len(data) == 0 {
		return ErrInvalidData
	}
	if len(data) > MaxDataBytes {
		return ErrTooLarge
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return ErrInvalidData
	}
	return nil
}

// ValidateSource checks that source is one of the shapes the event schema allows.
func ValidateSource(source string) error {
	if len(source) > 255 || !sourcePattern.MatchString(source) {
		return ErrInvalidSource
	}
	return nil
}

// EmitterOfSource returns the emitter name of a "/emitters/<name>" source.
func EmitterOfSource(source string) (string, bool) {
	rest, ok := strings.CutPrefix(source, "/emitters/")
	if !ok || rest == "" {
		return "", false
	}
	name, _, _ := strings.Cut(rest, "/")
	return name, name != ""
}

// ValidName reports whether s can be the name part of an actor or chart scope.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// ActorOf writes the caller as the event actor, "<kind>:<name>". The name
// always comes from the caller, never from a request body.
func ActorOf(c core.Caller) (string, error) {
	valid := namePattern.MatchString(c.Name)
	if c.Kind == core.CallerPlugin {
		valid = pluginName.MatchString(c.Name)
	}
	if !valid {
		return "", ErrInvalidActor
	}
	return string(c.Kind) + ":" + c.Name, nil
}

// EventActor is the actor written on an event. The event schema has no plugin
// kind, so plugin callers appear as "system:plugin".
func EventActor(c core.Caller) (string, error) {
	actor, err := ActorOf(c)
	if err != nil {
		return "", err
	}
	if c.Kind == core.CallerPlugin {
		return "system:plugin", nil
	}
	return actor, nil
}

// DefaultSource is where an event from c comes from when it names no source.
func DefaultSource(c core.Caller) string {
	switch c.Kind {
	case core.CallerAgent:
		return "/agents/" + c.Name
	case core.CallerHost:
		return "/host"
	default:
		return "/daemon"
	}
}

// Event builds the stored envelope. The sequence is added when it is stored.
func (d Draft) Event(id string, now time.Time, actor, chartScope string) core.Event {
	correlation := d.CorrelationID
	switch {
	case correlation != "":
	case d.CausationID != "":
		correlation = d.CausationID
	default:
		correlation = id
	}
	return core.Event{
		SpecVersion:     "1.0",
		ID:              id,
		Source:          d.Source,
		Type:            d.Type,
		Subject:         d.Subject,
		Time:            &now,
		DataContentType: "application/json",
		Data:            d.Data,
		Actor:           actor,
		ChartScope:      chartScope,
		CausationID:     d.CausationID,
		CorrelationID:   correlation,
	}
}
