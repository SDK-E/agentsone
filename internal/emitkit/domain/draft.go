// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidDraft says a draft can never be published, whatever the daemon says.
var ErrInvalidDraft = errors.New("invalid event draft")

const (
	typePrefix    = "agentsone."
	maxSubjectLen = 1024
)

// Draft is the part of an event a source supplies. The daemon fills the id,
// time, sequence, actor and chart scope.
type Draft struct {
	Type    string
	Subject string
	Data    json.RawMessage
}

// NewDraft encodes data, which must encode to a JSON object, and checks the
// cheap rules. The daemon checks the rest against the event catalog.
func NewDraft(eventType, subject string, data any) (Draft, error) {
	if !strings.HasPrefix(eventType, typePrefix) {
		return Draft{}, fmt.Errorf("%w: type %q is outside the agentsone namespace", ErrInvalidDraft, eventType)
	}
	if len(subject) > maxSubjectLen {
		return Draft{}, fmt.Errorf("%w: subject is longer than %d bytes", ErrInvalidDraft, maxSubjectLen)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Draft{}, fmt.Errorf("%w: encode data: %w", ErrInvalidDraft, err)
	}
	if len(raw) == 0 || raw[0] != '{' {
		return Draft{}, fmt.Errorf("%w: data must be a JSON object", ErrInvalidDraft)
	}
	return Draft{Type: eventType, Subject: subject, Data: raw}, nil
}
