// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/audit/adapters/memory"
	"github.com/SDK-E/agentsone/internal/audit/app"
	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func newLog() (*app.Log, *memory.Repository, *coretest.Clock) {
	repo := memory.New()
	clock := coretest.NewClock(coretest.Epoch)
	return app.NewLog(repo, clock), repo, clock
}

func hire(target string) core.AuditEntry {
	return core.AuditEntry{Actor: "agent:founder", Action: "agent.hire", Target: "agent:" + target,
		Detail: json.RawMessage(`{"template":"small-seed/builder"}`)}
}

func TestAppendBuildsAChainThatVerifies(t *testing.T) {
	log, repo, clock := newLog()
	ctx := context.Background()
	for _, name := range []string{"builder", "waker", "router"} {
		if err := log.Append(ctx, hire(name)); err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Second)
	}
	records, err := repo.List(ctx, 0, 10)
	if err != nil || len(records) != 3 {
		t.Fatalf("records %d, %v", len(records), err)
	}
	first := records[0]
	if first.Seq != 1 || first.PrevHash != domain.GenesisHash || !first.Time.Time().Equal(coretest.Epoch) ||
		first.Outcome != domain.OutcomeSuccess || first.Origin != domain.OriginInside {
		t.Fatalf("first record %+v", first)
	}
	if records[1].PrevHash != first.Hash || len(first.Hash) != 64 {
		t.Fatalf("chain link broken: %+v", records[1])
	}
	if err := log.Verify(ctx); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyFindsATamperedRecord(t *testing.T) {
	repo := memory.New()
	clock := coretest.NewClock(coretest.Epoch)
	log := app.NewLog(repo, clock)
	if err := log.Append(context.Background(), hire("builder")); err != nil {
		t.Fatal(err)
	}
	tampered := memory.New()
	r, _, _ := repo.Last(context.Background())
	r.Target.ID = "someone-else"
	if err := tampered.Append(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := app.NewLog(tampered, clock).Verify(context.Background()); err == nil {
		t.Fatal("tampered record verified")
	}
}

func TestAppendTakesTheActorFromTheCaller(t *testing.T) {
	log, repo, _ := newLog()
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: "alice", UID: 1000})
	entry := hire("builder")
	entry.Actor = ""
	if err := log.Append(ctx, entry); err != nil {
		t.Fatal(err)
	}
	r, _, _ := repo.Last(ctx)
	if r.Actor.Kind != domain.ActorHuman || r.Actor.Name != "alice" || r.Actor.UID == nil || *r.Actor.UID != 1000 {
		t.Fatalf("actor %+v", r.Actor)
	}
}

func TestAppendRejectsBadEntries(t *testing.T) {
	log, _, _ := newLog()
	cases := map[string]func(*core.AuditEntry){
		"bad action":      func(e *core.AuditEntry) { e.Action = "hire" },
		"bad actor":       func(e *core.AuditEntry) { e.Actor = "robot:x" },
		"no actor":        func(e *core.AuditEntry) { e.Actor = "" },
		"bad target":      func(e *core.AuditEntry) { e.Target = "" },
		"details array":   func(e *core.AuditEntry) { e.Detail = json.RawMessage(`[1]`) },
		"details invalid": func(e *core.AuditEntry) { e.Detail = json.RawMessage(`{`) },
	}
	for name, mutate := range cases {
		e := hire("builder")
		mutate(&e)
		if err := log.Append(context.Background(), e); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestAppendRecordsTheOutcomeAndDefaultsToSuccess(t *testing.T) {
	log, repo, _ := newLog()
	for _, tc := range []struct {
		given string
		want  domain.Outcome
	}{
		{"", domain.OutcomeSuccess},
		{core.AuditFailure, domain.OutcomeFailure},
		{core.AuditDenied, domain.OutcomeDenied},
	} {
		e := hire("builder")
		e.Outcome = tc.given
		if err := log.Append(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		r, _, _ := repo.Last(context.Background())
		if r.Outcome != tc.want {
			t.Errorf("outcome %q stored as %q, want %q", tc.given, r.Outcome, tc.want)
		}
	}
	e := hire("builder")
	e.Outcome = "maybe"
	if err := log.Append(context.Background(), e); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown outcome: got %v, want ErrInvalid", err)
	}
}

func TestAppendDefaultsEmptyDetailsToAnObject(t *testing.T) {
	log, repo, _ := newLog()
	e := hire("builder")
	e.Detail = nil
	if err := log.Append(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	r, _, _ := repo.Last(context.Background())
	if string(r.Details) != "{}" {
		t.Fatalf("details %s", r.Details)
	}
}

func TestListPassesThroughWithALimit(t *testing.T) {
	log, _, _ := newLog()
	for range 3 {
		if err := log.Append(context.Background(), hire("builder")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := log.List(context.Background(), 1, 1)
	if err != nil || len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("list: %+v, %v", got, err)
	}
}
