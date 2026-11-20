// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const moduleName = "auth"

// Migrate brings the identity tables in core.db up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load auth migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate auth: %w", err)
	}
	return nil
}

// Repository stores humans, sessions, tokens and setup state in core.db.
type Repository struct{ db *sql.DB }

var (
	_ domain.Sessions = (*Repository)(nil)
	_ domain.Tokens   = (*Repository)(nil)
	_ domain.Humans   = (*Repository)(nil)
	_ domain.Setup    = (*Repository)(nil)
)

// New returns a repository on a migrated database.
func New(db *store.DB) *Repository { return &Repository{db: db.SQL()} }

const timeLayout = time.RFC3339Nano

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func nullTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(t), Valid: true}
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", s, err)
	}
	return t, nil
}

func parseNullTime(s sql.NullString) (time.Time, error) {
	if !s.Valid {
		return time.Time{}, nil
	}
	return parseTime(s.String)
}

func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, core.ErrNotFound)
	}
	return fmt.Errorf("read %s: %w", what, err)
}

// SaveSession stores s.
func (r *Repository) SaveSession(ctx context.Context, s domain.Session) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO auth_sessions (id, human, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		s.ID, s.Human, formatTime(s.CreatedAt), formatTime(s.ExpiresAt))
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// Session returns the session with id.
func (r *Repository) Session(ctx context.Context, id string) (domain.Session, error) {
	var s domain.Session
	var created, expires string
	err := r.db.QueryRowContext(ctx, `SELECT id, human, created_at, expires_at FROM auth_sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.Human, &created, &expires)
	if err != nil {
		return domain.Session{}, notFound(err, "session")
	}
	if s.CreatedAt, err = parseTime(created); err != nil {
		return domain.Session{}, err
	}
	if s.ExpiresAt, err = parseTime(expires); err != nil {
		return domain.Session{}, err
	}
	return s, nil
}

// DeleteSession removes a session; an unknown id is not an error.
func (r *Repository) DeleteSession(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteHumanSessions removes every session of human.
func (r *Repository) DeleteHumanSessions(ctx context.Context, human string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE human = ?`, human); err != nil {
		return fmt.Errorf("delete sessions of %s: %w", human, err)
	}
	return nil
}

// SaveToken stores t.
func (r *Repository) SaveToken(ctx context.Context, t domain.Token) error {
	scopes, err := json.Marshal(append([]string{}, t.Scopes...))
	if err != nil {
		return fmt.Errorf("encode scopes: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO auth_tokens
		(id, hash, subject_kind, subject_name, scopes, issued_at, expires_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Hash[:], string(t.Subject.Kind), t.Subject.Name, string(scopes),
		formatTime(t.IssuedAt), formatTime(t.ExpiresAt), nullTime(t.RevokedAt))
	if err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	return nil
}

// Token returns the token with id.
func (r *Repository) Token(ctx context.Context, id string) (domain.Token, error) {
	var t domain.Token
	var hash []byte
	var kind, scopes, issued, expires string
	var revoked sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id, hash, subject_kind, subject_name, scopes, issued_at, expires_at, revoked_at
		FROM auth_tokens WHERE id = ?`, id).Scan(&t.ID, &hash, &kind, &t.Subject.Name, &scopes, &issued, &expires, &revoked)
	if err != nil {
		return domain.Token{}, notFound(err, "token")
	}
	copy(t.Hash[:], hash)
	t.Subject.Kind = core.CallerKind(kind)
	t.Subject.UID = -1
	if err := json.Unmarshal([]byte(scopes), &t.Scopes); err != nil {
		return domain.Token{}, fmt.Errorf("decode scopes: %w", err)
	}
	if t.IssuedAt, err = parseTime(issued); err != nil {
		return domain.Token{}, err
	}
	if t.ExpiresAt, err = parseTime(expires); err != nil {
		return domain.Token{}, err
	}
	if t.RevokedAt, err = parseNullTime(revoked); err != nil {
		return domain.Token{}, err
	}
	return t, nil
}

// RevokeToken marks a live token revoked at at.
func (r *Repository) RevokeToken(ctx context.Context, id string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE auth_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("token %s: %w", id, core.ErrNotFound)
	}
	return nil
}

// AddHuman stores a new human.
func (r *Repository) AddHuman(ctx context.Context, h domain.Human) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO `+humanTable+` (`+humanColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
		h.Name, h.DisplayName, h.UID, h.PasswordHash, formatTime(h.CreatedAt), nullTime(h.LastLoginAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("human %s: %w", h.Name, core.ErrConflict)
		}
		return fmt.Errorf("add human: %w", err)
	}
	return nil
}

const (
	humanTable   = `auth_humans`
	humanColumns = `name, display_name, uid, password_hash, created_at, last_login_at`
)

func scanHuman(row interface{ Scan(...any) error }) (domain.Human, error) {
	var h domain.Human
	var created string
	var last sql.NullString
	if err := row.Scan(&h.Name, &h.DisplayName, &h.UID, &h.PasswordHash, &created, &last); err != nil {
		return domain.Human{}, err
	}
	var err error
	if h.CreatedAt, err = parseTime(created); err != nil {
		return domain.Human{}, err
	}
	if h.LastLoginAt, err = parseNullTime(last); err != nil {
		return domain.Human{}, err
	}
	return h, nil
}

// Human returns the human called name.
func (r *Repository) Human(ctx context.Context, name string) (domain.Human, error) {
	h, err := scanHuman(r.db.QueryRowContext(ctx, `SELECT `+humanColumns+` FROM auth_humans WHERE name = ?`, name))
	if err != nil {
		return domain.Human{}, notFound(err, "human "+name)
	}
	return h, nil
}

// HumanByUID returns the human with uid.
func (r *Repository) HumanByUID(ctx context.Context, uid int) (domain.Human, error) {
	h, err := scanHuman(r.db.QueryRowContext(ctx, `SELECT `+humanColumns+` FROM auth_humans WHERE uid = ?`, uid))
	if err != nil {
		return domain.Human{}, notFound(err, fmt.Sprintf("human uid %d", uid))
	}
	return h, nil
}

// ListHumans returns every human by name.
func (r *Repository) ListHumans(ctx context.Context) ([]domain.Human, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+humanColumns+` FROM auth_humans ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	out := []domain.Human{}
	for rows.Next() {
		h, err := scanHuman(rows)
		if err != nil {
			return nil, fmt.Errorf("scan human: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	return out, nil
}

// UpdateHuman replaces a stored human's display name, password hash and
// last sign-in.
func (r *Repository) UpdateHuman(ctx context.Context, h domain.Human) error {
	res, err := r.db.ExecContext(ctx, `UPDATE auth_humans SET display_name = ?, password_hash = ?, last_login_at = ? WHERE name = ?`,
		h.DisplayName, h.PasswordHash, nullTime(h.LastLoginAt), h.Name)
	if err != nil {
		return fmt.Errorf("update human: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("human %s: %w", h.Name, core.ErrNotFound)
	}
	return nil
}

// DeleteHuman removes a human and, by cascade, their sessions.
func (r *Repository) DeleteHuman(ctx context.Context, name string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM auth_humans WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete human: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("human %s: %w", name, core.ErrNotFound)
	}
	return nil
}

func (r *Repository) loadSetup(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM auth_setup WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read setup %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("decode setup %s: %w", key, err)
	}
	return true, nil
}

func (r *Repository) saveSetup(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode setup %s: %w", key, err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO auth_setup (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(b))
	if err != nil {
		return fmt.Errorf("save setup %s: %w", key, err)
	}
	return nil
}

// Licence returns the recorded acceptance.
func (r *Repository) Licence(ctx context.Context) (domain.Licence, bool, error) {
	var l domain.Licence
	ok, err := r.loadSetup(ctx, "licence", &l)
	return l, ok, err
}

// SaveLicence records an acceptance.
func (r *Repository) SaveLicence(ctx context.Context, l domain.Licence) error {
	return r.saveSetup(ctx, "licence", l)
}

// EngineFetch returns the stored fetch status.
func (r *Repository) EngineFetch(ctx context.Context) (domain.EngineFetch, bool, error) {
	var f domain.EngineFetch
	ok, err := r.loadSetup(ctx, "engine_fetch", &f)
	return f, ok, err
}

// SaveEngineFetch stores the fetch status.
func (r *Repository) SaveEngineFetch(ctx context.Context, f domain.EngineFetch) error {
	return r.saveSetup(ctx, "engine_fetch", f)
}
