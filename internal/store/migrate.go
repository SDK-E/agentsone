// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Migration is one schema step for one module.
type Migration struct {
	// Version is a positive number; versions must be strictly increasing.
	Version int
	// Name is a short label, recorded and checked on later runs.
	Name string
	// SQL is the statements to run, in one transaction.
	SQL string
}

var migrationFile = regexp.MustCompile(`^(\d{4,})_([a-z0-9_]+)\.sql$`)

// MigrationsFromFS reads migrations from files named like 0001_create_agents.sql in dir,
// usually an embed.FS. Files that do not match the pattern are an error.
func MigrationsFromFS(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("store: read migrations %s: %w", dir, err)
	}
	var ms []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFile.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("store: migration file %q does not match NNNN_name.sql", e.Name())
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("store: migration file %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", e.Name(), err)
		}
		ms = append(ms, Migration{Version: v, Name: m[2], SQL: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms, nil
}

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	module     TEXT    NOT NULL,
	version    INTEGER NOT NULL,
	name       TEXT    NOT NULL,
	applied_at TEXT    NOT NULL,
	PRIMARY KEY (module, version)
)`

// Migrate applies the pending migrations of module, each in its own transaction. It is
// idempotent. It fails when versions are not strictly increasing, or when a version already
// applied was recorded under a different name.
func (db *DB) Migrate(ctx context.Context, module string, ms []Migration) error {
	if module == "" {
		return errors.New("store: migrate: empty module name")
	}
	prev := 0
	for _, m := range ms {
		if m.Version <= prev {
			return fmt.Errorf("store: migrate %s: version %d is not greater than %d", module, m.Version, prev)
		}
		prev = m.Version
	}
	if _, err := db.sql.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	applied := map[int]string{}
	rows, err := db.sql.QueryContext(ctx, `SELECT version, name FROM schema_migrations WHERE module = ?`, module)
	if err != nil {
		return fmt.Errorf("store: read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		var n string
		if err := rows.Scan(&v, &n); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: scan schema_migrations: %w", err)
		}
		applied[v] = n
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("store: close rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: read schema_migrations: %w", err)
	}

	for _, m := range ms {
		if name, ok := applied[m.Version]; ok {
			if name != m.Name {
				return fmt.Errorf("store: migrate %s: version %d was applied as %q, now named %q", module, m.Version, name, m.Name)
			}
			continue
		}
		err := db.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (module, version, name, applied_at) VALUES (?, ?, ?, ?)`,
				module, m.Version, m.Name, time.Now().UTC().Format(time.RFC3339Nano))
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migrate %s %04d_%s: %w", module, m.Version, m.Name, err)
		}
	}
	return nil
}

// Version returns the highest applied migration version of module, or 0 when none.
func (db *DB) Version(ctx context.Context, module string) (int, error) {
	if _, err := db.sql.ExecContext(ctx, createMigrationsTable); err != nil {
		return 0, fmt.Errorf("store: create schema_migrations: %w", err)
	}
	var v sql.NullInt64
	err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations WHERE module = ?`, module).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("store: read version: %w", err)
	}
	return int(v.Int64), nil
}
