// SPDX-License-Identifier: MIT

package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// The contract spells one instant one way: RFC 3339 in UTC with a Z suffix
// (audit hashes depend on it). Every generated type that holds a date-time must
// therefore encode a time given with an offset as UTC, including nullable ones.
func TestGeneratedDateTimesEncodeAsUTC(t *testing.T) {
	const withOffset = "2026-11-19T21:00:00+02:00"
	const utc = "2026-11-19T19:00:00Z"
	cases := []struct {
		name  string
		field string
		into  any
	}{
		{"api nullable date-time", "last_turn_at", &api.Agent{}},
		{"runner nullable date-time", "compactedAt", &protocol.SessionState{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := json.Marshal(map[string]any{c.field: withOffset})
			if err != nil {
				t.Fatal(err)
			}
			// Required fields are missing, so decode errors are expected
			// from validation; the date-time field is still read.
			_ = json.Unmarshal(in, c.into)
			out, err := json.Marshal(c.into)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if got[c.field] != utc {
				t.Fatalf("%s = %v, want %s", c.field, got[c.field], utc)
			}
		})
	}
}
