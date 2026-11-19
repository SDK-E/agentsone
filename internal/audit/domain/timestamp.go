// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidTimestamp is returned for a time that is not RFC 3339 in UTC with Z.
var ErrInvalidTimestamp = errors.New("invalid audit timestamp")

// Timestamp is when a record was written. Its spelling is part of the
// record's hash ("…:00.5Z" and "…:00.500Z" hash differently), so a parsed
// timestamp keeps the text it was read from.
type Timestamp struct {
	t    time.Time
	text string
}

// NewTimestamp spells t in UTC with as few fraction digits as needed.
func NewTimestamp(t time.Time) Timestamp {
	utc := t.UTC()
	return Timestamp{t: utc, text: utc.Format(time.RFC3339Nano)}
}

// ParseTimestamp reads an RFC 3339 time in UTC with a Z suffix.
func ParseTimestamp(s string) (Timestamp, error) {
	if !strings.HasSuffix(s, "Z") {
		return Timestamp{}, fmt.Errorf("%w: %q needs a Z suffix", ErrInvalidTimestamp, s)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return Timestamp{}, fmt.Errorf("%w: %w", ErrInvalidTimestamp, err)
	}
	return Timestamp{t: t, text: s}, nil
}

// Time is the instant.
func (ts Timestamp) Time() time.Time { return ts.t }

// String is the exact spelling.
func (ts Timestamp) String() string { return ts.text }
