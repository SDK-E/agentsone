// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/audit/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/edge/audit/app"
	"github.com/SDK-E/agentsone/internal/edge/audit/domain"
)

func open(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func rec(seq int64) app.Stored {
	n := strconv.FormatInt(seq, 10)
	return app.Stored{Seq: seq, Hash: "h" + n, Raw: json.RawMessage(`{"seq":` + n + `}`)}
}

func TestStore_EmptyChainHasTheGenesisHead(t *testing.T) {
	t.Parallel()
	s := open(t, filepath.Join(t.TempDir(), "audit.db"))
	h, err := s.Head(t.Context(), app.Inside)
	if err != nil || h != domain.Empty() {
		t.Fatalf("Head = %+v, %v", h, err)
	}
}

func TestStore_KeepsEachChainApartAndInOrder(t *testing.T) {
	t.Parallel()
	s := open(t, filepath.Join(t.TempDir(), "audit.db"))
	ctx := t.Context()
	for _, r := range []app.Stored{rec(1), rec(2), rec(3)} {
		if err := s.Append(ctx, app.Inside, r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := s.Append(ctx, app.Proof, rec(1)); err != nil {
		t.Fatalf("Append proof: %v", err)
	}
	h, _ := s.Head(ctx, app.Inside)
	if h.Seq != 3 || h.Hash != "h3" {
		t.Fatalf("Head = %+v", h)
	}
	got, err := s.List(ctx, app.Inside, 1, 10)
	if err != nil || len(got) != 2 || got[0].Seq != 2 || string(got[1].Raw) != `{"seq":3}` {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if hash, _ := s.HashAt(ctx, app.Inside, 2); hash != "h2" {
		t.Fatalf("HashAt(2) = %q", hash)
	}
	if hash, _ := s.HashAt(ctx, app.Inside, 9); hash != "" {
		t.Fatalf("HashAt(9) = %q, want empty", hash)
	}
	if p, _ := s.Head(ctx, app.Proof); p.Seq != 1 {
		t.Fatalf("proof head = %+v", p)
	}
}

func TestStore_NeverOverwritesARecord(t *testing.T) {
	t.Parallel()
	s := open(t, filepath.Join(t.TempDir(), "audit.db"))
	if err := s.Append(t.Context(), app.Inside, rec(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(t.Context(), app.Inside, app.Stored{Seq: 1, Hash: "x", Raw: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("a record was overwritten")
	}
}

func TestStore_RefusesUpdatesAndDeletes(t *testing.T) {
	t.Parallel()
	s := open(t, filepath.Join(t.TempDir(), "audit.db"))
	if err := s.Append(t.Context(), app.Inside, rec(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAlert(t.Context(), domain.Alert{Kind: "gap", Seq: 3, Time: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE records SET hash = 'x'`, `DELETE FROM records`,
		`UPDATE alerts SET detail = 'x'`, `DELETE FROM alerts`,
	} {
		if err := s.Exec(t.Context(), q); err == nil {
			t.Errorf("%s succeeded on an append-only table", q)
		}
	}
}

func TestStore_KeepsAnAlertOnceAndNewestFirst(t *testing.T) {
	t.Parallel()
	s := open(t, filepath.Join(t.TempDir(), "audit.db"))
	ctx := t.Context()
	at := time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)
	for _, a := range []domain.Alert{
		{Kind: "gap", Seq: 3, Detail: "first", Time: at},
		{Kind: "gap", Seq: 3, Detail: "again", Time: at.Add(time.Minute)},
		{Kind: "fork", Seq: 1, Detail: "fork", Time: at.Add(2 * time.Minute)},
	} {
		if err := s.AddAlert(ctx, a); err != nil {
			t.Fatalf("AddAlert: %v", err)
		}
	}
	got, err := s.Alerts(ctx, 10)
	if err != nil || len(got) != 2 || got[0].Kind != "fork" || got[1].Detail != "first" || !got[1].Time.Equal(at) {
		t.Fatalf("Alerts = %+v, %v", got, err)
	}
}

func TestStore_SurvivesAReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.db")
	first, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Append(t.Context(), app.Inside, rec(1)); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	h, err := open(t, path).Head(t.Context(), app.Inside)
	if err != nil || h.Seq != 1 {
		t.Fatalf("Head after reopen = %+v, %v", h, err)
	}
}

func TestStore_ReportsAClosedDatabase(t *testing.T) {
	t.Parallel()
	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	ctx := t.Context()
	if _, err := s.Head(ctx, app.Inside); err == nil {
		t.Error("Head on a closed store")
	}
	if _, err := s.HashAt(ctx, app.Inside, 1); err == nil {
		t.Error("HashAt on a closed store")
	}
	if _, err := s.List(ctx, app.Inside, 0, 1); err == nil {
		t.Error("List on a closed store")
	}
	if _, err := s.Alerts(ctx, 1); err == nil {
		t.Error("Alerts on a closed store")
	}
	if err := s.AddAlert(ctx, domain.Alert{Kind: "gap"}); err == nil {
		t.Error("AddAlert on a closed store")
	}
}

func TestOpen_FailsOnAFolderPath(t *testing.T) {
	t.Parallel()
	if _, err := sqlite.Open(t.Context(), t.TempDir()); err == nil {
		t.Fatal("Open on a folder succeeded")
	}
}
