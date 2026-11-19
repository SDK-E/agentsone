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
	"unicode"

	"github.com/SDK-E/agentsone/internal/memory/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const (
	moduleName = "memory"
	// maxSnippet is the contract's limit for a snippet, in characters.
	maxSnippet = 500
	// snippetTokens is how many words around the hit the snippet shows.
	snippetTokens = 24
)

// Migrate brings the memory tables up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load memory migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate memory: %w", err)
	}
	return nil
}

// Repository stores memory records in memory.db.
type Repository struct{ db *store.DB }

var _ domain.Repository = (*Repository)(nil)

// New returns a repository on a migrated database.
func New(db *store.DB) *Repository { return &Repository{db: db} }

const columns = `r.id, r.scope, r.owner, r.team, r.key, r.title, r.body, r.tags, r.links,
	r.source_kind, r.source_ref, r.created_at, r.updated_at, r.revision`

// Get returns the record, or domain.ErrNotFound.
func (s *Repository) Get(ctx context.Context, id string) (domain.Record, error) {
	row := s.db.SQL().QueryRowContext(ctx, `SELECT `+columns+` FROM memory_records r WHERE r.id = ?`, id)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Record{}, fmt.Errorf("memory %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Record{}, fmt.Errorf("get memory %s: %w", id, err)
	}
	return rec, nil
}

// FindByKey returns the record the owner wrote with key in scope.
func (s *Repository) FindByKey(ctx context.Context, owner string, scope domain.Scope, key string) (domain.Record, bool, error) {
	if key == "" {
		return domain.Record{}, false, nil
	}
	row := s.db.SQL().QueryRowContext(ctx,
		`SELECT `+columns+` FROM memory_records r WHERE r.owner = ? AND r.scope = ? AND r.key = ?`,
		owner, string(scope), key)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Record{}, false, nil
	}
	if err != nil {
		return domain.Record{}, false, fmt.Errorf("find memory by key: %w", err)
	}
	return rec, true, nil
}

// Save inserts r or replaces the record with its id, and its search entry.
func (s *Repository) Save(ctx context.Context, r domain.Record) error {
	tags, links, err := encodeLists(r)
	if err != nil {
		return err
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO memory_records
			(id, scope, owner, team, key, title, body, tags, links, source_kind, source_ref, created_at, updated_at, revision)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			  scope=excluded.scope, owner=excluded.owner, team=excluded.team, key=excluded.key,
			  title=excluded.title, body=excluded.body, tags=excluded.tags, links=excluded.links,
			  source_kind=excluded.source_kind, source_ref=excluded.source_ref,
			  created_at=excluded.created_at, updated_at=excluded.updated_at, revision=excluded.revision`,
			r.ID, string(r.Scope), r.Owner, r.Team, r.Key, r.Title, r.Body, tags, links,
			string(r.Source.Kind), r.Source.Ref, formatTime(r.CreatedAt), formatTime(r.UpdatedAt), r.Revision)
		if err != nil {
			return err
		}
		return reindex(ctx, tx, r)
	})
	if err != nil {
		return fmt.Errorf("save memory %s: %w", r.ID, err)
	}
	return nil
}

func reindex(ctx context.Context, tx *sql.Tx, r domain.Record) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE record_id = ?`, r.ID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO memory_fts (title, body, tags, record_id) VALUES (?,?,?,?)`,
		r.Title, r.Body, strings.Join(r.Tags, " "), r.ID)
	return err
}

// Delete removes the record and its search entry, or returns domain.ErrNotFound.
func (s *Repository) Delete(ctx context.Context, id string) error {
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM memory_records WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE record_id = ?`, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete memory %s: %w", id, err)
	}
	return nil
}

// List returns visible records, newest first.
func (s *Repository) List(ctx context.Context, q domain.ListQuery) (domain.Page, error) {
	where, args := visibilityClause(q.Visibility)
	if q.Scope != "" {
		where, args = where+` AND r.scope = ?`, append(args, string(q.Scope))
	}
	if q.Owner != "" {
		where, args = where+` AND r.owner = ?`, append(args, q.Owner)
	}
	if q.Tag != "" {
		where, args = where+` AND EXISTS (SELECT 1 FROM json_each(r.tags) WHERE value = ?)`, append(args, q.Tag)
	}
	if q.After != "" {
		where, args = where+` AND r.id < ?`, append(args, q.After)
	}
	rows, err := s.db.SQL().QueryContext(ctx,
		`SELECT `+columns+` FROM memory_records r WHERE `+where+` ORDER BY r.id DESC LIMIT ?`,
		append(args, q.Limit+1)...)
	if err != nil {
		return domain.Page{}, fmt.Errorf("list memory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var items []domain.Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return domain.Page{}, fmt.Errorf("list memory: %w", err)
		}
		items = append(items, rec)
	}
	if err := rows.Err(); err != nil {
		return domain.Page{}, fmt.Errorf("list memory: %w", err)
	}
	more := len(items) > q.Limit
	if more {
		items = items[:q.Limit]
	}
	return domain.Page{Items: items, More: more}, nil
}

// bm25 weights: a hit in the title counts ten times a hit in the body, a tag five times.
const rank = `bm25(memory_fts, 10.0, 1.0, 5.0)`

// Search returns visible records that hold every word of the query, best match first.
func (s *Repository) Search(ctx context.Context, q domain.SearchQuery) (domain.HitPage, error) {
	match := matchExpression(q.Text)
	if match == "" {
		return domain.HitPage{Items: []domain.Hit{}}, nil
	}
	where, args := visibilityClause(q.Visibility)
	if q.Scope != "" {
		where, args = where+` AND r.scope = ?`, append(args, string(q.Scope))
	}
	args = append([]any{match}, args...)
	rows, err := s.db.SQL().QueryContext(ctx,
		`SELECT `+columns+`, -`+rank+`, snippet(memory_fts, -1, '[', ']', '…', `+fmt.Sprint(snippetTokens)+`)
		 FROM memory_fts JOIN memory_records r ON r.id = memory_fts.record_id
		 WHERE memory_fts MATCH ? AND `+where+`
		 ORDER BY `+rank+`, r.id DESC LIMIT ? OFFSET ?`,
		append(args, q.Limit+1, q.Offset)...)
	if err != nil {
		return domain.HitPage{}, fmt.Errorf("search memory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := []domain.Hit{}
	for rows.Next() {
		var (
			score   float64
			snippet string
		)
		rec, err := scanRecord(rows, &score, &snippet)
		if err != nil {
			return domain.HitPage{}, fmt.Errorf("search memory: %w", err)
		}
		hits = append(hits, domain.Hit{Record: rec, Score: score, Snippet: truncate(snippet, maxSnippet)})
	}
	if err := rows.Err(); err != nil {
		return domain.HitPage{}, fmt.Errorf("search memory: %w", err)
	}
	more := len(hits) > q.Limit
	if more {
		hits = hits[:q.Limit]
	}
	return domain.HitPage{Items: hits, More: more}, nil
}

// visibilityClause is the SQL form of domain.Visibility.
func visibilityClause(v domain.Visibility) (string, []any) {
	if v.All {
		return `1 = 1`, nil
	}
	parts, args := []string{`r.scope = 'common'`}, []any(nil)
	if v.Owner != "" {
		parts, args = append(parts, `(r.scope = 'private' AND r.owner = ?)`), append(args, v.Owner)
	}
	if v.Team != "" {
		parts, args = append(parts, `(r.scope = 'team' AND r.team = ?)`), append(args, v.Team)
	}
	return `(` + strings.Join(parts, ` OR `) + `)`, args
}

// matchExpression turns free text into an FTS5 query that needs every word. Each
// word is quoted, so operators and punctuation in the text are plain text.
func matchExpression(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, w := range words {
		words[i] = `"` + w + `"`
	}
	return strings.Join(words, " ")
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

type scanner interface{ Scan(dest ...any) error }

func scanRecord(s scanner, extra ...any) (domain.Record, error) {
	var (
		r                        domain.Record
		scope, tags, links, kind string
		created, updated         string
	)
	dest := append([]any{&r.ID, &scope, &r.Owner, &r.Team, &r.Key, &r.Title, &r.Body, &tags, &links,
		&kind, &r.Source.Ref, &created, &updated, &r.Revision}, extra...)
	if err := s.Scan(dest...); err != nil {
		return domain.Record{}, err
	}
	r.Scope, r.Source.Kind = domain.Scope(scope), domain.SourceKind(kind)
	var err error
	if r.CreatedAt, err = parseTime(created); err != nil {
		return domain.Record{}, fmt.Errorf("memory %s created_at: %w", r.ID, err)
	}
	if r.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.Record{}, fmt.Errorf("memory %s updated_at: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(tags), &r.Tags); err != nil {
		return domain.Record{}, fmt.Errorf("memory %s tags: %w", r.ID, err)
	}
	if r.Links, err = decodeLinks(links); err != nil {
		return domain.Record{}, fmt.Errorf("memory %s links: %w", r.ID, err)
	}
	return r, nil
}

type linkJSON struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Title string `json:"title,omitempty"`
}

func encodeLists(r domain.Record) (tags, links string, err error) {
	t, err := json.Marshal(nonNil(r.Tags))
	if err != nil {
		return "", "", fmt.Errorf("encode tags of %s: %w", r.ID, err)
	}
	ls := make([]linkJSON, 0, len(r.Links))
	for _, l := range r.Links {
		ls = append(ls, linkJSON(l))
	}
	l, err := json.Marshal(ls)
	if err != nil {
		return "", "", fmt.Errorf("encode links of %s: %w", r.ID, err)
	}
	return string(t), string(l), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func decodeLinks(raw string) ([]domain.Link, error) {
	var ls []linkJSON
	if err := json.Unmarshal([]byte(raw), &ls); err != nil {
		return nil, err
	}
	out := make([]domain.Link, 0, len(ls))
	for _, l := range ls {
		out = append(out, domain.Link(l))
	}
	return out, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
