// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/auth/authtest"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/store"
)

func TestRepository_MeetsTheContract(t *testing.T) {
	t.Parallel()
	authtest.RunRepositoryContract(t, func(t *testing.T) authtest.Repo {
		db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "core.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlite.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		return sqlite.New(db)
	})
}

// Migration 0002 revokes every live token kept under the earlier hex id,
// which no revoke could name, and leaves tok_ tokens and earlier revokes
// alone.
func TestMigrate_RevokesTheTokensOfTheEarlierIDForm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { coretest.Close(t, db) })
	ms, err := store.MigrationsFromFS(os.DirFS("."), "migrations")
	if err != nil || len(ms) < 2 || ms[1].Version != 2 {
		t.Fatalf("migrations = %d, %v; want 0002 after 0001", len(ms), err)
	}
	if err := db.Migrate(ctx, "auth", ms[:1]); err != nil {
		t.Fatal(err)
	}
	epoch := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	earlier := epoch.Add(time.Hour).Format(time.RFC3339Nano)
	for _, row := range []struct{ id, revoked any }{
		{"0123456789abcdef01234567", nil},
		{"89abcdef0123456789abcdef", earlier},
	} {
		if _, err := db.SQL().ExecContext(ctx, `INSERT INTO auth_tokens
			(id, hash, subject_kind, subject_name, scopes, issued_at, expires_at, revoked_at)
			VALUES (?, x'00', 'agent', 'founder', '[]', ?, ?, ?)`,
			row.id, epoch.Format(time.RFC3339Nano), epoch.Add(30*24*time.Hour).Format(time.RFC3339Nano), row.revoked); err != nil {
			t.Fatal(err)
		}
	}
	repo := sqlite.New(db)
	live, _, err := domain.NewToken(rand.Reader, core.Caller{Kind: core.CallerAgent, Name: "founder"}, nil, epoch, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveToken(ctx, live); err != nil {
		t.Fatal(err)
	}

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	if tok, err := repo.Token(ctx, "0123456789abcdef01234567"); err != nil || tok.RevokedAt.IsZero() {
		t.Errorf("live hex-id token after the migration: revoked at %v, %v; want revoked", tok.RevokedAt, err)
	}
	if tok, err := repo.Token(ctx, "89abcdef0123456789abcdef"); err != nil || !tok.RevokedAt.Equal(epoch.Add(time.Hour)) {
		t.Errorf("revoked hex-id token after the migration: revoked at %v, %v; want its first revoke kept", tok.RevokedAt, err)
	}
	if tok, err := repo.Token(ctx, live.ID); err != nil || !tok.RevokedAt.IsZero() {
		t.Errorf("tok_ token after the migration: revoked at %v, %v; want live", tok.RevokedAt, err)
	}
	// Only the token this migration revoked waits for its audit record.
	ids, err := repo.LegacyRevokes(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "0123456789abcdef01234567" {
		t.Fatalf("legacy revokes = %v, %v; want the one live hex-id token", ids, err)
	}
	if err := repo.LegacyRevokeAudited(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if ids, err := repo.LegacyRevokes(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("legacy revokes after the audit = %v, %v; want none", ids, err)
	}
}
