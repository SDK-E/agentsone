// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const moduleName = "router"

// Migrate brings the router tables up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load router migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate router: %w", err)
	}
	return nil
}

// Repository keeps lines and assignments in the core database.
type Repository struct{ db *store.DB }

var (
	_ domain.LineRepository       = (*Repository)(nil)
	_ domain.AssignmentRepository = (*Repository)(nil)
)

// New returns a repository on a migrated database.
func New(db *store.DB) *Repository { return &Repository{db: db} }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Put sets the agent's line.
func (r *Repository) Put(ctx context.Context, l domain.StoredLine) error {
	_, err := r.db.SQL().ExecContext(ctx,
		`INSERT INTO router_lines (agent, line, updated) VALUES (?, ?, ?)
		 ON CONFLICT(agent) DO UPDATE SET line = excluded.line, updated = excluded.updated`,
		l.Agent, l.Line.String(), formatTime(l.Updated))
	if err != nil {
		return fmt.Errorf("put line of %s: %w", l.Agent, err)
	}
	return nil
}

// List returns every line sorted by agent.
func (r *Repository) List(ctx context.Context) ([]domain.StoredLine, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT agent, line, updated FROM router_lines ORDER BY agent`)
	if err != nil {
		return nil, fmt.Errorf("list lines: %w", err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	var out []domain.StoredLine
	for rows.Next() {
		var agent, text, updated string
		if err := rows.Scan(&agent, &text, &updated); err != nil {
			return nil, fmt.Errorf("scan line: %w", err)
		}
		l, err := domain.NewLine(text)
		if err != nil {
			return nil, fmt.Errorf("stored line of %s: %w", agent, err)
		}
		at, err := time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("stored time of %s: %w", agent, err)
		}
		out = append(out, domain.StoredLine{Agent: agent, Line: l, Updated: at})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list lines: %w", err)
	}
	return out, nil
}

// Delete forgets the agent's line.
func (r *Repository) Delete(ctx context.Context, agent string) error {
	if _, err := r.db.SQL().ExecContext(ctx, `DELETE FROM router_lines WHERE agent = ?`, agent); err != nil {
		return fmt.Errorf("delete line of %s: %w", agent, err)
	}
	return nil
}

type hopJSON struct {
	Router   string `json:"router"`
	HandedTo string `json:"handed_to"`
	Reason   string `json:"reason"`
}

type shortlistedJSON struct {
	Agent   string  `json:"agent"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
	OneLine string  `json:"one_line"`
}

// Save stores a.
func (r *Repository) Save(ctx context.Context, a domain.Assignment) error {
	hops := make([]hopJSON, len(a.Hops))
	for i, h := range a.Hops {
		hops[i] = hopJSON(h)
	}
	list := make([]shortlistedJSON, len(a.Shortlist))
	for i, s := range a.Shortlist {
		list[i] = shortlistedJSON(s)
	}
	hopsRaw, err := json.Marshal(hops)
	if err != nil {
		return fmt.Errorf("encode hops: %w", err)
	}
	listRaw, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("encode shortlist: %w", err)
	}
	_, err = r.db.SQL().ExecContext(ctx,
		`INSERT INTO router_assignments
		 (id, text, chosen, router, hops, shortlist, chat_path, chat_message_id, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Text, a.Chosen, a.Router, string(hopsRaw), string(listRaw), a.ChatPath, a.ChatMessageID,
		a.CreatedBy, formatTime(a.CreatedAt))
	if err != nil {
		return fmt.Errorf("save assignment %s: %w", a.ID, err)
	}
	return nil
}

// Get returns the assignment with id.
func (r *Repository) Get(ctx context.Context, id string) (domain.Assignment, error) {
	var a domain.Assignment
	var hopsRaw, listRaw, created string
	err := r.db.SQL().QueryRowContext(ctx,
		`SELECT id, text, chosen, router, hops, shortlist, chat_path, chat_message_id, created_by, created_at
		 FROM router_assignments WHERE id = ?`, id).
		Scan(&a.ID, &a.Text, &a.Chosen, &a.Router, &hopsRaw, &listRaw, &a.ChatPath, &a.ChatMessageID, &a.CreatedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Assignment{}, fmt.Errorf("assignment %s: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("get assignment %s: %w", id, err)
	}
	var hops []hopJSON
	var list []shortlistedJSON
	if err := json.Unmarshal([]byte(hopsRaw), &hops); err != nil {
		return domain.Assignment{}, fmt.Errorf("decode hops of %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(listRaw), &list); err != nil {
		return domain.Assignment{}, fmt.Errorf("decode shortlist of %s: %w", id, err)
	}
	for _, h := range hops {
		a.Hops = append(a.Hops, domain.Hop(h))
	}
	for _, s := range list {
		a.Shortlist = append(a.Shortlist, domain.Shortlisted(s))
	}
	if a.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return domain.Assignment{}, fmt.Errorf("stored time of %s: %w", id, err)
	}
	return a, nil
}
