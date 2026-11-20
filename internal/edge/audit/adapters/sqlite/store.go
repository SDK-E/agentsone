// SPDX-License-Identifier: MIT

// Package sqlite keeps the edge's audit copy in audit.db in the edge data folder. Both tables are
// append only: triggers refuse every UPDATE and DELETE, so a bug cannot rewrite the copy.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // the pure-Go SQLite driver

	"github.com/SDK-E/agentsone/internal/edge/audit/app"
	"github.com/SDK-E/agentsone/internal/edge/audit/domain"
)

// FileName is the database inside the edge data folder.
const FileName = "audit.db"

const schema = `
CREATE TABLE IF NOT EXISTS records (
	chain TEXT    NOT NULL CHECK (chain IN ('inside', 'proof')),
	seq   INTEGER NOT NULL CHECK (seq >= 1),
	hash  TEXT    NOT NULL,
	raw   TEXT    NOT NULL,
	PRIMARY KEY (chain, seq)
) STRICT;
CREATE TABLE IF NOT EXISTS alerts (
	id     INTEGER PRIMARY KEY,
	kind   TEXT    NOT NULL,
	seq    INTEGER NOT NULL,
	detail TEXT    NOT NULL,
	time   TEXT    NOT NULL,
	UNIQUE (kind, seq)
) STRICT;
CREATE TRIGGER IF NOT EXISTS records_no_update BEFORE UPDATE ON records
	BEGIN SELECT RAISE(ABORT, 'audit records are append only'); END;
CREATE TRIGGER IF NOT EXISTS records_no_delete BEFORE DELETE ON records
	BEGIN SELECT RAISE(ABORT, 'audit records are append only'); END;
CREATE TRIGGER IF NOT EXISTS alerts_no_update BEFORE UPDATE ON alerts
	BEGIN SELECT RAISE(ABORT, 'audit alerts are append only'); END;
CREATE TRIGGER IF NOT EXISTS alerts_no_delete BEFORE DELETE ON alerts
	BEGIN SELECT RAISE(ABORT, 'audit alerts are append only'); END;
`

// Store is audit.db.
type Store struct{ db *sql.DB }

var _ app.Store = (*Store)(nil)

// Open opens or creates the database at path.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("audit: create schema in %s: %w", path, err), db.Close())
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Exec runs one statement. Tests use it to prove the tables refuse changes.
func (s *Store) Exec(ctx context.Context, query string) error {
	_, err := s.db.ExecContext(ctx, query)
	return err
}

// Head returns the last record of a chain.
func (s *Store) Head(ctx context.Context, c app.Chain) (domain.Head, error) {
	h := domain.Empty()
	err := s.db.QueryRowContext(ctx,
		`SELECT seq, hash FROM records WHERE chain = ? ORDER BY seq DESC LIMIT 1`, string(c)).Scan(&h.Seq, &h.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Empty(), nil
	}
	if err != nil {
		return domain.Head{}, fmt.Errorf("audit: head of %s: %w", c, err)
	}
	return h, nil
}

// HashAt returns the hash held at seq, empty when none.
func (s *Store) HashAt(ctx context.Context, c app.Chain, seq int64) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT hash FROM records WHERE chain = ? AND seq = ?`, string(c), seq).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("audit: record %d of %s: %w", seq, c, err)
	}
	return hash, nil
}

// Append keeps one record. A record already held at that place is never replaced.
func (s *Store) Append(ctx context.Context, c app.Chain, r app.Stored) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO records (chain, seq, hash, raw) VALUES (?, ?, ?, ?)`,
		string(c), r.Seq, r.Hash, string(r.Raw)); err != nil {
		return fmt.Errorf("audit: keep record %d of %s: %w", r.Seq, c, err)
	}
	return nil
}

// List returns up to limit records with Seq > after, oldest first.
func (s *Store) List(ctx context.Context, c app.Chain, after int64, limit int) ([]app.Stored, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, hash, raw FROM records WHERE chain = ? AND seq > ? ORDER BY seq LIMIT ?`,
		string(c), after, limit)
	if err != nil {
		return nil, fmt.Errorf("audit: list %s: %w", c, err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	var out []app.Stored
	for rows.Next() {
		var r app.Stored
		var raw string
		if err := rows.Scan(&r.Seq, &r.Hash, &raw); err != nil {
			return nil, fmt.Errorf("audit: list %s: %w", c, err)
		}
		r.Raw = []byte(raw)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: list %s: %w", c, err)
	}
	return out, nil
}

// AddAlert keeps an alert once per kind and sequence.
func (s *Store) AddAlert(ctx context.Context, a domain.Alert) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO alerts (kind, seq, detail, time) VALUES (?, ?, ?, ?) ON CONFLICT (kind, seq) DO NOTHING`,
		a.Kind, a.Seq, a.Detail, a.Time.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("audit: keep alert: %w", err)
	}
	return nil
}

// Alerts returns up to limit alerts, newest first.
func (s *Store) Alerts(ctx context.Context, limit int) ([]domain.Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, seq, detail, time FROM alerts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("audit: alerts: %w", err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	var out []domain.Alert
	for rows.Next() {
		var a domain.Alert
		var at string
		if err := rows.Scan(&a.Kind, &a.Seq, &a.Detail, &at); err != nil {
			return nil, fmt.Errorf("audit: alerts: %w", err)
		}
		if a.Time, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("audit: alert time %q: %w", at, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: alerts: %w", err)
	}
	return out, nil
}
