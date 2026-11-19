// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/SDK-E/agentsone/internal/audit/adapters/memory"
	"github.com/SDK-E/agentsone/internal/audit/app"
	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

type fixtureRecord struct {
	Seq   int64  `json:"seq"`
	Time  string `json:"time"`
	Actor struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		UID  *int   `json:"uid"`
	} `json:"actor"`
	Action string `json:"action"`
	Target struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"target"`
	Outcome  string          `json:"outcome"`
	Details  json.RawMessage `json:"details"`
	Origin   string          `json:"origin"`
	PrevHash string          `json:"prevHash"`
	Hash     string          `json:"hash"`
}

func (f fixtureRecord) record(t *testing.T) domain.Record {
	t.Helper()
	when, err := domain.ParseTimestamp(f.Time)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Record{
		Seq: f.Seq, Time: when, Action: domain.Action(f.Action), Details: f.Details,
		Actor:    domain.Actor{Kind: domain.ActorKind(f.Actor.Kind), Name: f.Actor.Name, UID: f.Actor.UID},
		Target:   domain.Target{Kind: f.Target.Kind, ID: f.Target.ID},
		Outcome:  domain.Outcome(f.Outcome),
		Origin:   domain.Origin(f.Origin),
		PrevHash: f.PrevHash, Hash: f.Hash,
	}
}

// TestVerifyAgreesWithContractFixtures proves the log hashes records exactly
// as the contract's chain fixtures do.
func TestVerifyAgreesWithContractFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "contract", "fixtures", "schemas", "audit-record", "valid", "chain-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no audit chain fixtures")
	}
	sort.Strings(files)
	repo := memory.New()
	for _, f := range files {
		raw, err := os.ReadFile(f) // #nosec G304 -- fixed test directory
		if err != nil {
			t.Fatal(err)
		}
		var fr fixtureRecord
		if err := json.Unmarshal(raw, &fr); err != nil {
			t.Fatal(err)
		}
		if err := repo.Append(context.Background(), fr.record(t)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if err := app.NewLog(repo, coretest.NewClock(coretest.Epoch)).Verify(context.Background()); err != nil {
		t.Fatalf("contract fixtures do not verify through the log: %v", err)
	}
}

var errBroken = errors.New("disk on fire")

type brokenRepo struct{ domain.Repository }

func (brokenRepo) Last(context.Context) (domain.Record, bool, error) {
	return domain.Record{}, false, errBroken
}

func (brokenRepo) List(context.Context, int64, int) ([]domain.Record, error) { return nil, errBroken }

func TestRepositoryErrorsAreWrapped(t *testing.T) {
	log := app.NewLog(brokenRepo{}, coretest.NewClock(coretest.Epoch))
	ctx := context.Background()
	if err := log.Append(ctx, hire("builder")); !errors.Is(err, errBroken) {
		t.Errorf("append: %v", err)
	}
	if _, err := log.List(ctx, 0, 1); !errors.Is(err, errBroken) {
		t.Errorf("list: %v", err)
	}
	if err := log.Verify(ctx); !errors.Is(err, errBroken) {
		t.Errorf("verify: %v", err)
	}
}

func TestAppendWithoutActorOrCallerIsInvalid(t *testing.T) {
	log, _, _ := newLog()
	e := hire("builder")
	e.Actor = ""
	if err := log.Append(context.Background(), e); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}
