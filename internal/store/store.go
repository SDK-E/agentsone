// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	// Register the pure-Go "sqlite" driver.
	_ "modernc.org/sqlite"
)

// DriverName is the database/sql driver name registered by modernc.org/sqlite.
const DriverName = "sqlite"

// DB is an open SQLite database.
type DB struct {
	sql  *sql.DB
	path string
}

type options struct {
	busyTimeoutMS int
	maxOpenConns  int
}

// Option changes how Open configures the database.
type Option func(*options)

// WithBusyTimeout sets the SQLite busy timeout in milliseconds (default 5000).
func WithBusyTimeout(ms int) Option {
	return func(o *options) { o.busyTimeoutMS = ms }
}

// WithMaxOpenConns sets the connection pool size (default 8 for files, always 1 for memory).
func WithMaxOpenConns(n int) Option {
	return func(o *options) { o.maxOpenConns = n }
}

// Open opens (creating when needed) the SQLite database at path. Use ":memory:" for a private
// in-memory database, which is limited to one connection so every query sees the same data.
func Open(ctx context.Context, path string, opts ...Option) (*DB, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	o := options{busyTimeoutMS: 5000, maxOpenConns: 8}
	for _, opt := range opts {
		opt(&o)
	}

	memory := path == ":memory:"
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", o.busyTimeoutMS))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	if !memory {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	q.Set("_txlock", "immediate")
	dsn := "file:" + path + "?" + q.Encode()

	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if memory {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(o.maxOpenConns)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}
	return &DB{sql: db, path: path}, nil
}

// SQL returns the underlying *sql.DB.
func (db *DB) SQL() *sql.DB { return db.sql }

// Path returns the path the database was opened with.
func (db *DB) Path() string { return db.path }

// Close closes the database.
func (db *DB) Close() error { return db.sql.Close() }

// Tx runs fn inside a transaction. It commits when fn returns nil and rolls back when fn returns
// an error or panics; a panic is re-raised after the rollback.
func (db *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("store: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
