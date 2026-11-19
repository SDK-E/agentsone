// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const moduleName = "audit"

// Migrate brings the audit tables up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load audit migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate audit: %w", err)
	}
	return nil
}

// Repository stores audit records in audit.db.
type Repository struct{ db *store.DB }

var _ domain.Repository = (*Repository)(nil)

// New returns a repository on a migrated database.
func New(db *store.DB) *Repository { return &Repository{db: db} }

const columns = `seq, time, actor_kind, actor_name, actor_uid, action, target_kind, target_id,
	outcome, details, origin, prev_hash, hash`

// Append adds r inside one transaction when it follows the last record.
func (s *Repository) Append(ctx context.Context, r domain.Record) error {
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		last, hasLast, err := lastRecord(ctx, tx)
		if err != nil {
			return err
		}
		if !r.Follows(last, hasLast) {
			return fmt.Errorf("seq %d: %w", r.Seq, domain.ErrOutOfOrder)
		}
		return insert(ctx, tx, r)
	})
	if err != nil {
		return fmt.Errorf("append audit record: %w", err)
	}
	return nil
}

// Last returns the newest record.
func (s *Repository) Last(ctx context.Context) (domain.Record, bool, error) {
	r, ok, err := lastRecord(ctx, s.db.SQL())
	if err != nil {
		return domain.Record{}, false, fmt.Errorf("last audit record: %w", err)
	}
	return r, ok, nil
}

// List returns up to limit records after afterSeq, oldest first.
func (s *Repository) List(ctx context.Context, afterSeq int64, limit int) ([]domain.Record, error) {
	rows, err := s.db.SQL().QueryContext(ctx,
		`SELECT `+columns+` FROM audit_records WHERE seq > ? ORDER BY seq LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit records: %w", err)
	}
	return out, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func lastRecord(ctx context.Context, q queryer) (domain.Record, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT `+columns+` FROM audit_records ORDER BY seq DESC LIMIT 1`)
	r, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Record{}, false, nil
	}
	if err != nil {
		return domain.Record{}, false, err
	}
	return r, true, nil
}

func insert(ctx context.Context, tx *sql.Tx, r domain.Record) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_records (`+columns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Seq, r.Time.String(), string(r.Actor.Kind), r.Actor.Name, nullableUID(r.Actor.UID),
		r.Action.String(), r.Target.Kind, r.Target.ID, string(r.Outcome), string(r.Details), string(r.Origin),
		r.PrevHash, r.Hash)
	if err != nil {
		return fmt.Errorf("insert seq %d: %w", r.Seq, err)
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanRecord(s scanner) (domain.Record, error) {
	var (
		r                                domain.Record
		when, actorKind, action, outcome string
		details, origin                  string
		uid                              sql.NullInt64
	)
	err := s.Scan(&r.Seq, &when, &actorKind, &r.Actor.Name, &uid, &action, &r.Target.Kind, &r.Target.ID,
		&outcome, &details, &origin, &r.PrevHash, &r.Hash)
	if err != nil {
		return domain.Record{}, fmt.Errorf("scan audit record: %w", err)
	}
	if r.Time, err = domain.ParseTimestamp(when); err != nil {
		return domain.Record{}, fmt.Errorf("audit record %d time: %w", r.Seq, err)
	}
	r.Actor.Kind, r.Action, r.Outcome, r.Origin = domain.ActorKind(actorKind), domain.Action(action),
		domain.Outcome(outcome), domain.Origin(origin)
	r.Details = []byte(details)
	r.Actor.UID = uidFrom(uid)
	return r, nil
}

func nullableUID(uid *int) sql.NullInt64 {
	if uid == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*uid), Valid: true}
}

func uidFrom(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}
