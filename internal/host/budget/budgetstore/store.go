// SPDX-License-Identifier: MIT

// Package budgetstore keeps budget settings and spending in budget.db, an
// SQLite file opened through the pure-Go modernc driver.
package budgetstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	// Registers the "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	"github.com/SDK-E/agentsone/internal/host/budget"
	"github.com/SDK-E/agentsone/internal/host/budget/budgetapp"
)

const schema = `
CREATE TABLE IF NOT EXISTS settings (
	id                INTEGER PRIMARY KEY CHECK (id = 1),
	total_micros      INTEGER NOT NULL CHECK (total_micros >= 0),
	defence_on        INTEGER NOT NULL CHECK (defence_on IN (0, 1)),
	defence_share_pct INTEGER NOT NULL CHECK (defence_share_pct BETWEEN 0 AND 100)
);
CREATE TABLE IF NOT EXISTS spend (
	id             INTEGER PRIMARY KEY CHECK (id = 1),
	general_micros INTEGER NOT NULL,
	defence_micros INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS agent_spend (
	agent  TEXT PRIMARY KEY,
	micros INTEGER NOT NULL
);
`

// Store implements budgetapp.Store.
type Store struct {
	db *sql.DB
}

var _ budgetapp.Store = (*Store)(nil)

// Open opens or creates the database at path. Several processes may open the
// same file: the journal is in WAL mode and writers wait up to five seconds.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("budgetstore: open %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("budgetstore: create schema in %s: %w", path, err), db.Close())
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// LoadSettings returns the stored settings, or zero settings when none exist.
func (s *Store) LoadSettings(ctx context.Context) (budgetapp.Settings, error) {
	var set budgetapp.Settings
	var total int64
	err := s.db.QueryRowContext(ctx,
		`SELECT total_micros, defence_on, defence_share_pct FROM settings WHERE id = 1`).
		Scan(&total, &set.DefenceOn, &set.DefenceSharePct)
	if errors.Is(err, sql.ErrNoRows) {
		return budgetapp.Settings{}, nil
	}
	if err != nil {
		return budgetapp.Settings{}, fmt.Errorf("budgetstore: load settings: %w", err)
	}
	set.Total = budget.Micros(total)
	return set, nil
}

// SaveSettings replaces the stored settings.
func (s *Store) SaveSettings(ctx context.Context, set budgetapp.Settings) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (id, total_micros, defence_on, defence_share_pct) VALUES (1, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET total_micros = excluded.total_micros,
		   defence_on = excluded.defence_on, defence_share_pct = excluded.defence_share_pct`,
		int64(set.Total), set.DefenceOn, set.DefenceSharePct)
	if err != nil {
		return fmt.Errorf("budgetstore: save settings: %w", err)
	}
	return nil
}

// LoadSpend returns the stored spending.
func (s *Store) LoadSpend(ctx context.Context) (budgetapp.Spend, error) {
	sp := budgetapp.Spend{Agents: map[string]budget.Micros{}}
	var general, defence int64
	err := s.db.QueryRowContext(ctx, `SELECT general_micros, defence_micros FROM spend WHERE id = 1`).
		Scan(&general, &defence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return budgetapp.Spend{}, fmt.Errorf("budgetstore: load spending: %w", err)
	}
	sp.General, sp.Defence = budget.Micros(general), budget.Micros(defence)

	rows, err := s.db.QueryContext(ctx, `SELECT agent, micros FROM agent_spend`)
	if err != nil {
		return budgetapp.Spend{}, fmt.Errorf("budgetstore: load agent spending: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var agent string
		var m int64
		if err := rows.Scan(&agent, &m); err != nil {
			return budgetapp.Spend{}, fmt.Errorf("budgetstore: read agent spending: %w", err)
		}
		sp.Agents[agent] = budget.Micros(m)
	}
	if err := rows.Err(); err != nil {
		return budgetapp.Spend{}, fmt.Errorf("budgetstore: read agent spending: %w", err)
	}
	return sp, nil
}

// SaveSpend replaces the stored spending in one transaction.
func (s *Store) SaveSpend(ctx context.Context, sp budgetapp.Spend) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("budgetstore: save spending: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO spend (id, general_micros, defence_micros) VALUES (1, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET general_micros = excluded.general_micros,
		   defence_micros = excluded.defence_micros`,
		int64(sp.General), int64(sp.Defence)); err != nil {
		return fmt.Errorf("budgetstore: save spending: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM agent_spend`); err != nil {
		return fmt.Errorf("budgetstore: save agent spending: %w", err)
	}
	for agent, m := range sp.Agents {
		if _, err = tx.ExecContext(ctx, `INSERT INTO agent_spend (agent, micros) VALUES (?, ?)`, agent, int64(m)); err != nil {
			return fmt.Errorf("budgetstore: save agent spending: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("budgetstore: commit spending: %w", err)
	}
	return nil
}
