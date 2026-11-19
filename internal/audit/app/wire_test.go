// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/audit/adapters/memory"
	"github.com/SDK-E/agentsone/internal/audit/app"
	"github.com/SDK-E/agentsone/internal/auditchain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// What the edge receives must hash to the record's own hash, or the edge flags it as broken.
func TestEncodeRecord_WritesWhatTheChainHashCovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := app.NewLog(memory.New(), coretest.NewClock(time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)))
	if err := l.Append(ctx, core.AuditEntry{Actor: "agent:founder", Action: "agent.hire", Target: "agent:scout"}); err != nil {
		t.Fatal(err)
	}
	recs, err := l.List(ctx, 0, 1)
	if err != nil || len(recs) != 1 {
		t.Fatalf("List = %v, %v", recs, err)
	}
	raw, err := app.EncodeRecord(recs[0])
	if err != nil {
		t.Fatalf("EncodeRecord: %v", err)
	}
	if got, err := auditchain.Hash(raw); err != nil || got != recs[0].Hash {
		t.Fatalf("hash of the shipped form = %q, %v; want the record's hash %q", got, err, recs[0].Hash)
	}
}
