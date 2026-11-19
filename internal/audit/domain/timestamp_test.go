// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/audit/domain"
)

func TestParseTimestampKeepsItsSpelling(t *testing.T) {
	for _, s := range []string{"2026-11-19T19:04:00.500Z", "2026-11-19T19:00:00Z"} {
		ts, err := domain.ParseTimestamp(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if ts.String() != s {
			t.Errorf("%q became %q", s, ts)
		}
	}
	ts, _ := domain.ParseTimestamp("2026-11-19T19:04:00.500Z")
	if want := time.Date(2026, 11, 19, 19, 4, 0, 500_000_000, time.UTC); !ts.Time().Equal(want) {
		t.Errorf("time %v, want %v", ts.Time(), want)
	}
}

func TestParseTimestampRejectsOffsetsAndJunk(t *testing.T) {
	for _, s := range []string{"", "2026-11-19T19:04:00+01:00", "2026-11-19 19:04:00Z", "yesterday"} {
		if _, err := domain.ParseTimestamp(s); !errors.Is(err, domain.ErrInvalidTimestamp) {
			t.Errorf("%q: got %v, want ErrInvalidTimestamp", s, err)
		}
	}
}

func TestNewTimestampWritesUTC(t *testing.T) {
	paris := time.FixedZone("CET", 3600)
	ts := domain.NewTimestamp(time.Date(2026, 11, 19, 20, 4, 0, 500_000_000, paris))
	if ts.String() != "2026-11-19T19:04:00.5Z" {
		t.Fatalf("spelling %q", ts)
	}
}
