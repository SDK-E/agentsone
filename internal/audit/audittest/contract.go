// SPDX-License-Identifier: MIT

// Package audittest holds the audit repository contract: one suite every
// domain.Repository implementation must pass, in memory and on SQLite.
package audittest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/audit/domain"
)

// RunRepositoryContract runs the repository contract against fresh
// repositories made by newRepo.
func RunRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Helper()
	t.Run("empty log has no last record", func(t *testing.T) { emptyLog(t, newRepo(t)) })
	t.Run("records round trip", func(t *testing.T) { roundTrip(t, newRepo(t)) })
	t.Run("list pages after a sequence", func(t *testing.T) { listPages(t, newRepo(t)) })
	t.Run("out of order append is rejected", func(t *testing.T) { outOfOrder(t, newRepo(t)) })
}

// Chain returns n records that form a valid chain, with fake hashes.
func Chain(n int) []domain.Record {
	records := make([]domain.Record, 0, n)
	prev := domain.GenesisHash
	uid := 0
	for i := 1; i <= n; i++ {
		r := domain.Record{
			Seq:      int64(i),
			Time:     domain.NewTimestamp(time.Date(2026, 11, 19, 19, 0, i, 123456789, time.UTC)),
			Actor:    domain.Actor{Kind: domain.ActorAgent, Name: "founder", UID: &uid},
			Action:   "agent.hire",
			Target:   domain.Target{Kind: "agent", ID: fmt.Sprintf("builder-%d", i)},
			Outcome:  domain.OutcomeSuccess,
			Details:  json.RawMessage(`{"template":"small-seed/builder"}`),
			Origin:   domain.OriginInside,
			PrevHash: prev,
			Hash:     fmt.Sprintf("%064x", i),
		}
		records = append(records, r)
		prev = r.Hash
	}
	return records
}

func appendAll(t *testing.T, repo domain.Repository, records []domain.Record) {
	t.Helper()
	for _, r := range records {
		if err := repo.Append(context.Background(), r); err != nil {
			t.Fatalf("append seq %d: %v", r.Seq, err)
		}
	}
}

func emptyLog(t *testing.T, repo domain.Repository) {
	if _, ok, err := repo.Last(context.Background()); err != nil || ok {
		t.Fatalf("Last on empty log: ok=%v err=%v", ok, err)
	}
	got, err := repo.List(context.Background(), 0, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("List on empty log: %v, %v", got, err)
	}
}

func roundTrip(t *testing.T, repo domain.Repository) {
	want := Chain(2)
	appendAll(t, repo, want)
	last, ok, err := repo.Last(context.Background())
	if err != nil || !ok {
		t.Fatalf("Last: ok=%v err=%v", ok, err)
	}
	assertSameRecord(t, last, want[1])
	got, err := repo.List(context.Background(), 0, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("List: %d records, %v", len(got), err)
	}
	assertSameRecord(t, got[0], want[0])
}

func listPages(t *testing.T, repo domain.Repository) {
	appendAll(t, repo, Chain(4))
	got, err := repo.List(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("page after 1, limit 2: %+v", seqs(got))
	}
}

func outOfOrder(t *testing.T, repo domain.Repository) {
	chain := Chain(3)
	if err := repo.Append(context.Background(), chain[1]); !errors.Is(err, domain.ErrOutOfOrder) {
		t.Fatalf("second record first: got %v, want ErrOutOfOrder", err)
	}
	appendAll(t, repo, chain[:1])
	if err := repo.Append(context.Background(), chain[0]); !errors.Is(err, domain.ErrOutOfOrder) {
		t.Fatalf("duplicate: got %v, want ErrOutOfOrder", err)
	}
	broken := chain[1]
	broken.PrevHash = domain.GenesisHash
	if err := repo.Append(context.Background(), broken); !errors.Is(err, domain.ErrOutOfOrder) {
		t.Fatalf("broken link: got %v, want ErrOutOfOrder", err)
	}
}

func assertSameRecord(t *testing.T, got, want domain.Record) {
	t.Helper()
	if got.Time.String() != want.Time.String() || !got.Time.Time().Equal(want.Time.Time()) {
		t.Errorf("time %v, want %v", got.Time, want.Time)
	}
	got.Time, want.Time = domain.Timestamp{}, domain.Timestamp{}
	if string(got.Details) != string(want.Details) {
		t.Errorf("details %s, want %s", got.Details, want.Details)
	}
	got.Details, want.Details = nil, nil
	if (got.Actor.UID == nil) != (want.Actor.UID == nil) || (got.Actor.UID != nil && *got.Actor.UID != *want.Actor.UID) {
		t.Errorf("actor uid %v, want %v", got.Actor.UID, want.Actor.UID)
	}
	got.Actor.UID, want.Actor.UID = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("record\n got %+v\nwant %+v", got, want)
	}
}

func seqs(records []domain.Record) []int64 {
	out := make([]int64, 0, len(records))
	for _, r := range records {
		out = append(out, r.Seq)
	}
	return out
}
