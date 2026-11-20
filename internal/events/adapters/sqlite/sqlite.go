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

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const moduleName = "events"

// Migrate brings the events tables in bus.db up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load events migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate events: %w", err)
	}
	return nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("stored time %q: %w", s, err)
	}
	return t.UTC(), nil
}

func nullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Events stores the event log in bus.db.
type Events struct{ db *store.DB }

var _ domain.EventRepository = (*Events)(nil)

// NewEvents returns the event repository on a migrated database.
func NewEvents(db *store.DB) *Events { return &Events{db: db} }

// Append stores e and returns it with its sequence.
func (r *Events) Append(ctx context.Context, e core.Event) (core.Event, error) {
	var at time.Time
	if e.Time != nil {
		at = *e.Time
	}
	res, err := r.db.SQL().ExecContext(ctx,
		`INSERT INTO events (id, source, type, subject, time, dataschema, data, actor, chartscope, causationid, correlationid)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Source, e.Type, e.Subject, formatTime(at), e.DataSchema, string(e.Data), e.Actor, e.ChartScope,
		e.CausationID, e.CorrelationID)
	if isUnique(err) {
		return core.Event{}, fmt.Errorf("event %s: %w", e.ID, core.ErrConflict)
	}
	if err != nil {
		return core.Event{}, fmt.Errorf("append event: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return core.Event{}, fmt.Errorf("append event: %w", err)
	}
	e.Sequence = core.FormatSequence(uint64(seq)) // #nosec G115 -- rowids are positive
	return e, nil
}

// After returns up to limit events with a sequence larger than after.
func (r *Events) After(ctx context.Context, after uint64, limit int) ([]core.Event, error) {
	rows, err := r.db.SQL().QueryContext(ctx,
		`SELECT seq, id, source, type, subject, time, dataschema, data, actor, chartscope, causationid, correlationid
		 FROM events WHERE seq > ? ORDER BY seq LIMIT ?`, int64(after), limit) // #nosec G115 -- sequences stay far below 2^63
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	var out []core.Event
	for rows.Next() {
		var (
			e         core.Event
			seq       int64
			at, data  string
			schemaURL string
		)
		if err := rows.Scan(&seq, &e.ID, &e.Source, &e.Type, &e.Subject, &at, &schemaURL, &data, &e.Actor,
			&e.ChartScope, &e.CausationID, &e.CorrelationID); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		t, err := parseTime(at)
		if err != nil {
			return nil, err
		}
		e.SpecVersion, e.DataContentType, e.DataSchema = "1.0", "application/json", schemaURL
		e.Time, e.Data = &t, json.RawMessage(data)
		e.Sequence = core.FormatSequence(uint64(seq)) // #nosec G115 -- rowids are positive
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return out, nil
}

// Last returns the largest stored sequence.
func (r *Events) Last(ctx context.Context) (uint64, error) {
	var seq sql.NullInt64
	if err := r.db.SQL().QueryRowContext(ctx, `SELECT MAX(seq) FROM events`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("last event: %w", err)
	}
	return uint64(seq.Int64), nil // #nosec G115 -- rowids are positive
}

// Emitters stores emitters in bus.db.
type Emitters struct{ db *store.DB }

var _ domain.EmitterRepository = (*Emitters)(nil)

// NewEmitters returns the emitter repository on a migrated database.
func NewEmitters(db *store.DB) *Emitters { return &Emitters{db: db} }

const emitterColumns = `id, name, kind, description, event_types, enabled, unit, owner, plugin_id, webhook_path,
	created_at, last_event_at, command, secret`

// Create stores e.
func (r *Emitters) Create(ctx context.Context, e domain.Emitter) error {
	types, err := json.Marshal(nonNil(e.EventTypes))
	if err != nil {
		return fmt.Errorf("encode event types of emitter %q: %w", e.Name, err)
	}
	command, err := json.Marshal(nonNil(e.Command))
	if err != nil {
		return fmt.Errorf("encode command of emitter %q: %w", e.Name, err)
	}
	_, err = r.db.SQL().ExecContext(ctx, `INSERT INTO emitters (`+emitterColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, string(e.Kind), e.Description, string(types), boolInt(e.Enabled), e.Unit, e.Owner, e.PluginID,
		e.WebhookPath, formatTime(e.CreatedAt), nullable(e.LastEventAt), string(command), e.Secret)
	if isUnique(err) {
		return fmt.Errorf("emitter %q: %w", e.Name, domain.ErrNameTaken)
	}
	if err != nil {
		return fmt.Errorf("create emitter: %w", err)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nullable(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// Get returns the emitter with id.
func (r *Emitters) Get(ctx context.Context, id string) (domain.Emitter, error) {
	return r.one(ctx, `WHERE id = ?`, id)
}

// ByName returns the emitter called name.
func (r *Emitters) ByName(ctx context.Context, name string) (domain.Emitter, error) {
	return r.one(ctx, `WHERE name = ?`, name)
}

func (r *Emitters) one(ctx context.Context, where string, arg any) (domain.Emitter, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT `+emitterColumns+` FROM emitters `+where, arg)
	if err != nil {
		return domain.Emitter{}, fmt.Errorf("get emitter: %w", err)
	}
	list, err := scanEmitters(rows)
	if err != nil {
		return domain.Emitter{}, err
	}
	if len(list) == 0 {
		return domain.Emitter{}, fmt.Errorf("emitter %v: %w", arg, core.ErrNotFound)
	}
	return list[0], nil
}

// List returns up to limit emitters after afterID.
func (r *Emitters) List(ctx context.Context, afterID string, limit int) ([]domain.Emitter, error) {
	rows, err := r.db.SQL().QueryContext(ctx,
		`SELECT `+emitterColumns+` FROM emitters WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list emitters: %w", err)
	}
	return scanEmitters(rows)
}

func scanEmitters(rows *sql.Rows) ([]domain.Emitter, error) {
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	var out []domain.Emitter
	for rows.Next() {
		var (
			e                    domain.Emitter
			kind, types, command string
			enabled              int
			created              string
			lastEvent            sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.Name, &kind, &e.Description, &types, &enabled, &e.Unit, &e.Owner, &e.PluginID,
			&e.WebhookPath, &created, &lastEvent, &command, &e.Secret); err != nil {
			return nil, fmt.Errorf("scan emitter: %w", err)
		}
		var err error
		if e.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if e.LastEventAt, err = nullTime(lastEvent); err != nil {
			return nil, err
		}
		e.Kind, e.Enabled = domain.EmitterKind(kind), enabled == 1
		if err := errors.Join(json.Unmarshal([]byte(types), &e.EventTypes), json.Unmarshal([]byte(command), &e.Command)); err != nil {
			return nil, fmt.Errorf("decode emitter %s: %w", e.ID, err)
		}
		if len(e.Command) == 0 {
			e.Command = nil
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list emitters: %w", err)
	}
	return out, nil
}

// SetEnabled turns an emitter on or off.
func (r *Emitters) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := r.db.SQL().ExecContext(ctx, `UPDATE emitters SET enabled = ? WHERE id = ?`, boolInt(enabled), id)
	return affected(res, err, "emitter "+id)
}

// Touch records the time of the newest event of emitter name.
func (r *Emitters) Touch(ctx context.Context, name string, at time.Time) error {
	if _, err := r.db.SQL().ExecContext(ctx, `UPDATE emitters SET last_event_at = ? WHERE name = ?`, formatTime(at), name); err != nil {
		return fmt.Errorf("touch emitter: %w", err)
	}
	return nil
}

func affected(res sql.Result, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", what, core.ErrNotFound)
	}
	return nil
}

// Triggers stores triggers in bus.db.
type Triggers struct{ db *store.DB }

var _ domain.TriggerRepository = (*Triggers)(nil)

// NewTriggers returns the trigger repository on a migrated database.
func NewTriggers(db *store.DB) *Triggers { return &Triggers{db: db} }

type filterJSON struct {
	Types         []string `json:"types,omitempty"`
	Sources       []string `json:"sources,omitempty"`
	SubjectPrefix string   `json:"subject_prefix,omitempty"`
	Actors        []string `json:"actors,omitempty"`
	ChartScope    string   `json:"chartscope,omitempty"`
}

type actionJSON struct {
	Kind     string `json:"kind"`
	Agent    string `json:"agent"`
	Message  string `json:"message,omitempty"`
	Title    string `json:"title,omitempty"`
	Priority string `json:"priority,omitempty"`
}

const triggerColumns = `id, name, owner, filter, action, rate_max, rate_per, enabled, fired_count, dropped_count, last_fired_at, created_at`

func encode(t domain.Trigger) (filter, action []byte, err error) {
	f := filterJSON{Types: t.Filter.Types, Sources: t.Filter.Sources, SubjectPrefix: t.Filter.SubjectPrefix,
		Actors: t.Filter.Actors, ChartScope: t.Filter.ChartScope}
	a := actionJSON{Kind: string(t.Action.Kind), Agent: t.Action.Agent, Message: t.Action.Message,
		Title: t.Action.Title, Priority: t.Action.Priority}
	if filter, err = json.Marshal(f); err != nil {
		return nil, nil, fmt.Errorf("encode filter of trigger %s: %w", t.ID, err)
	}
	if action, err = json.Marshal(a); err != nil {
		return nil, nil, fmt.Errorf("encode action of trigger %s: %w", t.ID, err)
	}
	return filter, action, nil
}

// Create stores t.
func (r *Triggers) Create(ctx context.Context, t domain.Trigger) error {
	filter, action, err := encode(t)
	if err != nil {
		return err
	}
	_, err = r.db.SQL().ExecContext(ctx, `INSERT INTO triggers (`+triggerColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Owner, string(filter), string(action), t.Rate.Max, t.Rate.PerSeconds, boolInt(t.Enabled),
		t.FiredCount, t.DroppedCount, nullable(t.LastFiredAt), formatTime(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("create trigger: %w", err)
	}
	return nil
}

// Get returns the trigger with id.
func (r *Triggers) Get(ctx context.Context, id string) (domain.Trigger, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT `+triggerColumns+` FROM triggers WHERE id = ?`, id)
	if err != nil {
		return domain.Trigger{}, fmt.Errorf("get trigger: %w", err)
	}
	list, err := scanTriggers(rows)
	if err != nil {
		return domain.Trigger{}, err
	}
	if len(list) == 0 {
		return domain.Trigger{}, fmt.Errorf("trigger %s: %w", id, core.ErrNotFound)
	}
	return list[0], nil
}

// List returns up to limit triggers after afterID.
func (r *Triggers) List(ctx context.Context, afterID string, limit int) ([]domain.Trigger, error) {
	rows, err := r.db.SQL().QueryContext(ctx,
		`SELECT `+triggerColumns+` FROM triggers WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	return scanTriggers(rows)
}

// Enabled returns every enabled trigger.
func (r *Triggers) Enabled(ctx context.Context) ([]domain.Trigger, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT `+triggerColumns+` FROM triggers WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list enabled triggers: %w", err)
	}
	return scanTriggers(rows)
}

func scanTriggers(rows *sql.Rows) ([]domain.Trigger, error) {
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	var out []domain.Trigger
	for rows.Next() {
		var (
			t              domain.Trigger
			filter, action string
			enabled        int
			lastFired      sql.NullString
			created        string
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.Owner, &filter, &action, &t.Rate.Max, &t.Rate.PerSeconds, &enabled,
			&t.FiredCount, &t.DroppedCount, &lastFired, &created); err != nil {
			return nil, fmt.Errorf("scan trigger: %w", err)
		}
		var f filterJSON
		var a actionJSON
		if err := errors.Join(json.Unmarshal([]byte(filter), &f), json.Unmarshal([]byte(action), &a)); err != nil {
			return nil, fmt.Errorf("decode trigger %s: %w", t.ID, err)
		}
		var err error
		if t.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if t.LastFiredAt, err = nullTime(lastFired); err != nil {
			return nil, err
		}
		t.Enabled = enabled == 1
		t.Filter = domain.Filter{Types: f.Types, Sources: f.Sources, SubjectPrefix: f.SubjectPrefix, Actors: f.Actors, ChartScope: f.ChartScope}
		t.Action = domain.Action{Kind: domain.ActionKind(a.Kind), Agent: a.Agent, Message: a.Message, Title: a.Title, Priority: a.Priority}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	return out, nil
}

// Update replaces the editable fields of t.
func (r *Triggers) Update(ctx context.Context, t domain.Trigger) error {
	filter, action, err := encode(t)
	if err != nil {
		return err
	}
	res, err := r.db.SQL().ExecContext(ctx,
		`UPDATE triggers SET name = ?, filter = ?, action = ?, rate_max = ?, rate_per = ?, enabled = ? WHERE id = ?`,
		t.Name, string(filter), string(action), t.Rate.Max, t.Rate.PerSeconds, boolInt(t.Enabled), t.ID)
	return affected(res, err, "trigger "+t.ID)
}

// Delete removes the trigger.
func (r *Triggers) Delete(ctx context.Context, id string) error {
	res, err := r.db.SQL().ExecContext(ctx, `DELETE FROM triggers WHERE id = ?`, id)
	return affected(res, err, "trigger "+id)
}

// RecordFire counts a firing.
func (r *Triggers) RecordFire(ctx context.Context, id string, at time.Time) error {
	res, err := r.db.SQL().ExecContext(ctx,
		`UPDATE triggers SET fired_count = fired_count + 1, last_fired_at = ? WHERE id = ?`, formatTime(at), id)
	return affected(res, err, "trigger "+id)
}

// RecordDrop counts a refused match.
func (r *Triggers) RecordDrop(ctx context.Context, id string) error {
	res, err := r.db.SQL().ExecContext(ctx, `UPDATE triggers SET dropped_count = dropped_count + 1 WHERE id = ?`, id)
	return affected(res, err, "trigger "+id)
}
