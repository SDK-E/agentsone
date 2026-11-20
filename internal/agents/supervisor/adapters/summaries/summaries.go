// SPDX-License-Identifier: MIT

// Package summaries keeps each agent's compacted runner session in core.db
// (the supervisor's SummaryStore), so the next welcome hands it back even
// after the daemon restarts.
package summaries

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

// namespace is the migration namespace in core.db.
const namespace = "agents-summaries"

// Migrate creates the summaries table.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load summaries migrations: %w", err)
	}
	if err := db.Migrate(ctx, namespace, ms); err != nil {
		return fmt.Errorf("migrate summaries: %w", err)
	}
	return nil
}

// Store is the summaries table.
type Store struct{ db *store.DB }

var _ app.SummaryStore = (*Store)(nil)

// New returns the store on db. Run Migrate first.
func New(db *store.DB) *Store { return &Store{db: db} }

// Load returns agent's summary, or the zero Summary when there is none.
func (s *Store) Load(ctx context.Context, agent string) (app.Summary, error) {
	var out app.Summary
	var at string
	err := s.db.SQL().QueryRowContext(ctx,
		`SELECT text, compacted_at, paused_turn_id FROM runner_summaries WHERE agent = ?`, agent,
	).Scan(&out.Text, &at, &out.PausedTurnID)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Summary{}, nil
	}
	if err != nil {
		return app.Summary{}, fmt.Errorf("load summary of %s: %w", agent, err)
	}
	if out.CompactedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
		return app.Summary{}, fmt.Errorf("load summary of %s: compacted_at: %w", agent, err)
	}
	return out, nil
}

// Save replaces agent's summary.
func (s *Store) Save(ctx context.Context, agent string, v app.Summary) error {
	_, err := s.db.SQL().ExecContext(ctx,
		`INSERT INTO runner_summaries (agent, text, compacted_at, paused_turn_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (agent) DO UPDATE SET text = excluded.text, compacted_at = excluded.compacted_at,
		paused_turn_id = excluded.paused_turn_id`,
		agent, v.Text, v.CompactedAt.UTC().Format(time.RFC3339Nano), v.PausedTurnID)
	if err != nil {
		return fmt.Errorf("save summary of %s: %w", agent, err)
	}
	return nil
}
