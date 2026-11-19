// SPDX-License-Identifier: MIT

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/bus/adapters/sqlite"
	"github.com/SDK-E/agentsone/internal/bus/bustest"
	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

func openAt(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func openMigrated(t *testing.T) *store.DB { return openAt(t, filepath.Join(t.TempDir(), "bus.db")) }

func TestChannelsRepository(t *testing.T) {
	bustest.RunChannelContract(t, func(t *testing.T) domain.ChannelRepository { return sqlite.NewChannels(openMigrated(t)) })
}

func TestMessagesRepository(t *testing.T) {
	bustest.RunMessageContract(t, func(t *testing.T) domain.MessageRepository { return sqlite.NewMessages(openMigrated(t)) })
}

func TestMigrateTwiceIsSafe(t *testing.T) {
	db := openMigrated(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestSequencesSurviveReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bus.db")
	ctx := context.Background()
	if _, err := sqlite.NewMessages(openAt(t, path)).Append(ctx, bustest.ChannelMessage(1, "general")); err != nil {
		t.Fatal(err)
	}

	stored, err := sqlite.NewMessages(openAt(t, path)).Append(ctx, bustest.ChannelMessage(2, "general"))

	if err != nil || stored.Sequence != 2 {
		t.Fatalf("Append() after reopen = %d, %v, want 2", stored.Sequence, err)
	}
}

func TestAMessageNeedsExactlyOneTarget(t *testing.T) {
	r := sqlite.NewMessages(openMigrated(t))
	both := bustest.ChannelMessage(1, "general")
	both.To = "builder"
	neither := bustest.ChannelMessage(2, "")

	for name, m := range map[string]domain.Message{"both": both, "neither": neither} {
		if _, err := r.Append(context.Background(), m); err == nil {
			t.Errorf("%s: Append() stored a message with a bad target", name)
		}
	}
}
