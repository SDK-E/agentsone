// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return db
}

func TestOpenPragmas(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	var mode string
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
	var busy int
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
}

func TestOpenMemoryAndEmpty(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, ""); err == nil {
		t.Error("Open(\"\") succeeded, want error")
	}
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.SQL().ExecContext(ctx, "CREATE TABLE a (x)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, "INSERT INTO a VALUES (1)"); err != nil {
		t.Fatal(err)
	}
}

func TestFTS5(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, "CREATE VIRTUAL TABLE t USING fts5(x)"); err != nil {
		t.Fatalf("fts5: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx, "INSERT INTO t (x) VALUES ('hello agents one')"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.SQL().QueryRowContext(ctx, "SELECT count(*) FROM t WHERE t MATCH 'agents'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("match count = %d, want 1", n)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, `CREATE TABLE p (id INTEGER PRIMARY KEY);
		CREATE TABLE c (pid INTEGER NOT NULL REFERENCES p(id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, "INSERT INTO c (pid) VALUES (42)"); err == nil {
		t.Error("insert with dangling foreign key succeeded")
	}
}

func TestMigrate(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	fsys := fstest.MapFS{
		"m/0001_create.sql": {Data: []byte("CREATE TABLE agents (name TEXT PRIMARY KEY);")},
		"m/0002_more.sql":   {Data: []byte("ALTER TABLE agents ADD COLUMN line TEXT;")},
	}
	ms, err := MigrationsFromFS(fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Version != 1 || ms[1].Name != "more" {
		t.Fatalf("migrations = %+v", ms)
	}
	for range 2 {
		if err := db.Migrate(ctx, "agents", ms); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
	}
	if v, err := db.Version(ctx, "agents"); err != nil || v != 2 {
		t.Errorf("Version = %d, %v; want 2", v, err)
	}
	// Another module has its own version line.
	if v, err := db.Version(ctx, "memory"); err != nil || v != 0 {
		t.Errorf("Version(memory) = %d, %v; want 0", v, err)
	}
	other := []Migration{{Version: 1, Name: "create", SQL: "CREATE TABLE notes (id INTEGER);"}}
	if err := db.Migrate(ctx, "memory", other); err != nil {
		t.Fatalf("Migrate memory: %v", err)
	}
	if v, _ := db.Version(ctx, "memory"); v != 1 {
		t.Errorf("Version(memory) = %d, want 1", v)
	}

	// A renamed, already applied version is an error.
	renamed := []Migration{{Version: 1, Name: "renamed", SQL: "SELECT 1"}}
	if err := db.Migrate(ctx, "agents", renamed); err == nil {
		t.Error("renamed migration accepted")
	}
	// Versions must strictly increase.
	bad := []Migration{{Version: 2, Name: "a", SQL: "SELECT 1"}, {Version: 2, Name: "b", SQL: "SELECT 1"}}
	if err := db.Migrate(ctx, "x", bad); err == nil {
		t.Error("duplicate versions accepted")
	}
	// A failing migration leaves nothing recorded.
	failing := []Migration{{Version: 1, Name: "broken", SQL: "CREATE TABLE ok (x); NOT SQL"}}
	if err := db.Migrate(ctx, "y", failing); err == nil {
		t.Error("broken migration accepted")
	}
	if v, _ := db.Version(ctx, "y"); v != 0 {
		t.Errorf("Version(y) = %d after failure, want 0", v)
	}
}

func TestMigrationsFromFSBadName(t *testing.T) {
	fsys := fstest.MapFS{"m/create.sql": {Data: []byte("SELECT 1")}}
	if _, err := MigrationsFromFS(fsys, "m"); err == nil {
		t.Error("bad file name accepted")
	}
}

func TestTx(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, "CREATE TABLE a (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := db.SQL().QueryRowContext(ctx, "SELECT count(*) FROM a").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	insert := func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO a VALUES (1)")
		return err
	}

	if err := db.Tx(ctx, insert); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("count after commit = %d, want 1", n)
	}

	boom := errors.New("boom")
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if err := insert(tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx error = %v, want boom", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("count after rollback = %d, want 1", n)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was not re-raised")
			}
		}()
		_ = db.Tx(ctx, func(tx *sql.Tx) error {
			if err := insert(tx); err != nil {
				return err
			}
			panic("inside tx")
		})
	}()
	if n := count(); n != 1 {
		t.Fatalf("count after panic = %d, want 1", n)
	}
}
