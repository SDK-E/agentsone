// SPDX-License-Identifier: MIT

package common_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/common"
)

func TestTimestampRoundTripsAsRFC3339(t *testing.T) {
	in := common.Timestamp(time.Date(2026, 11, 19, 19, 0, 0, 0, time.UTC))
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2026-11-19T19:00:00Z"` {
		t.Fatalf("encoded %s", b)
	}
	var out common.Timestamp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !time.Time(out).Equal(time.Time(in)) {
		t.Fatalf("decoded %v", time.Time(out))
	}
	if err := json.Unmarshal([]byte(`"yesterday"`), &out); err == nil {
		t.Fatal("bad time accepted")
	}
}
