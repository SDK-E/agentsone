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

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const moduleName = "agents"

// Migrate brings the society tables up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load agents migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate agents: %w", err)
	}
	return nil
}

// Repository stores the society in core.db.
type Repository struct{ db *store.DB }

var _ domain.Repository = (*Repository)(nil)

// New returns a repository on a migrated database.
func New(db *store.DB) *Repository { return &Repository{db: db} }

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type scanner interface{ Scan(dest ...any) error }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", s, err)
	}
	return t.UTC(), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Agents.

const agentColumns = `name, display_name, kind, is_root, runner, manager, role, description, state,
	cpu_percent, memory_mb, tasks, uid, home, template, created_at, created_by, last_turn_at`

func agentArgs(a domain.Agent) []any {
	var cpu, mem, tasks sql.NullInt64
	if a.Limits != nil {
		cpu = sql.NullInt64{Int64: int64(a.Limits.CPUPercent), Valid: true}
		mem = sql.NullInt64{Int64: int64(a.Limits.MemoryMB), Valid: true}
		tasks = sql.NullInt64{Int64: int64(a.Limits.Tasks), Valid: true}
	}
	var lastTurn sql.NullString
	if a.LastTurnAt != nil {
		lastTurn = sql.NullString{String: formatTime(*a.LastTurnAt), Valid: true}
	}
	return []any{
		a.Name, a.DisplayName, string(a.Kind), boolInt(a.IsRoot), a.Runner, a.Manager, a.Role,
		a.Description, string(a.State), cpu, mem, tasks, a.UID, a.Home, a.Template,
		formatTime(a.CreatedAt), a.CreatedBy, lastTurn,
	}
}

func scanAgent(s scanner) (domain.Agent, error) {
	var (
		a                 domain.Agent
		kind, state, made string
		isRoot            int
		cpu, mem, tasks   sql.NullInt64
		lastTurn          sql.NullString
	)
	err := s.Scan(&a.Name, &a.DisplayName, &kind, &isRoot, &a.Runner, &a.Manager, &a.Role,
		&a.Description, &state, &cpu, &mem, &tasks, &a.UID, &a.Home, &a.Template, &made,
		&a.CreatedBy, &lastTurn)
	if err != nil {
		return domain.Agent{}, err
	}
	a.Kind, a.State, a.IsRoot = domain.Kind(kind), domain.State(state), isRoot == 1
	if cpu.Valid {
		a.Limits = &domain.Limits{CPUPercent: int(cpu.Int64), MemoryMB: int(mem.Int64), Tasks: int(tasks.Int64)}
	}
	if a.CreatedAt, err = parseTime(made); err != nil {
		return domain.Agent{}, fmt.Errorf("agent %s: %w", a.Name, err)
	}
	if lastTurn.Valid {
		t, err := parseTime(lastTurn.String)
		if err != nil {
			return domain.Agent{}, fmt.Errorf("agent %s: %w", a.Name, err)
		}
		a.LastTurnAt = &t
	}
	return a, nil
}

// ListAgents returns every agent sorted by name.
func (r *Repository) ListAgents(ctx context.Context) ([]domain.Agent, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT `+agentColumns+` FROM agents ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("list agents: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	return out, nil
}

// GetAgent returns the named agent.
func (r *Repository) GetAgent(ctx context.Context, name string) (domain.Agent, error) {
	a, err := scanAgent(r.db.SQL().QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agents WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, fmt.Errorf("%w: %s", domain.ErrAgentNotFound, name)
	}
	if err != nil {
		return domain.Agent{}, fmt.Errorf("get agent %s: %w", name, err)
	}
	return a, nil
}

// CreateAgent stores a new agent.
func (r *Repository) CreateAgent(ctx context.Context, a domain.Agent) error {
	if err := r.db.Tx(ctx, func(tx *sql.Tx) error { return insertAgent(ctx, tx, a) }); err != nil {
		return fmt.Errorf("create agent %s: %w", a.Name, err)
	}
	return nil
}

func insertAgent(ctx context.Context, tx *sql.Tx, a domain.Agent) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE name = ?`, a.Name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %s", domain.ErrNameTaken, a.Name)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO agents (`+agentColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, agentArgs(a)...)
	return err
}

// Outbox.

// HireAgent stores a and its event in one transaction.
func (r *Repository) HireAgent(ctx context.Context, a domain.Agent, ev domain.Outbound) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := insertAgent(ctx, tx, a); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO outbox (id, created_at, source, actor, chart_scope, type, data)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ev.ID, formatTime(ev.CreatedAt), ev.Source, ev.Actor, ev.ChartScope, ev.Type, string(ev.Data))
		return err
	})
	if err != nil {
		return fmt.Errorf("hire agent %s: %w", a.Name, err)
	}
	return nil
}

// PendingEvents returns up to limit unsent events, oldest first.
func (r *Repository) PendingEvents(ctx context.Context, limit int) ([]domain.Outbound, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT id, created_at, source, actor, chart_scope, type, data
		FROM outbox WHERE sent_at IS NULL ORDER BY created_at, rowid LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Outbound{}
	for rows.Next() {
		var (
			ev         domain.Outbound
			made, data string
		)
		if err := rows.Scan(&ev.ID, &made, &ev.Source, &ev.Actor, &ev.ChartScope, &ev.Type, &data); err != nil {
			return nil, fmt.Errorf("list pending events: %w", err)
		}
		if ev.CreatedAt, err = parseTime(made); err != nil {
			return nil, fmt.Errorf("outbox event %s: %w", ev.ID, err)
		}
		ev.Data = json.RawMessage(data)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending events: %w", err)
	}
	return out, nil
}

// MarkSent marks the event published. A second mark keeps the first time.
func (r *Repository) MarkSent(ctx context.Context, id string, at time.Time) error {
	res, err := r.db.SQL().ExecContext(ctx, `UPDATE outbox SET sent_at = COALESCE(sent_at, ?) WHERE id = ?`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("mark event %s sent: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark event %s sent: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", domain.ErrEventNotFound, id)
	}
	return nil
}

// updateAgent sets every column but name, in agentColumns order.
const updateAgent = `UPDATE agents SET display_name = ?, kind = ?, is_root = ?, runner = ?, manager = ?,
	role = ?, description = ?, state = ?, cpu_percent = ?, memory_mb = ?, tasks = ?, uid = ?, home = ?,
	template = ?, created_at = ?, created_by = ?, last_turn_at = ? WHERE name = ?`

// SaveAgents replaces existing agents in one transaction.
func (r *Repository) SaveAgents(ctx context.Context, agents ...domain.Agent) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, a := range agents {
			args := agentArgs(a)
			res, err := tx.ExecContext(ctx, updateAgent, append(args[1:], a.Name)...)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n == 0 {
				return errors.Join(fmt.Errorf("%w: %s", domain.ErrAgentNotFound, a.Name), err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save agents: %w", err)
	}
	return nil
}

// Charter.

// Charter returns the stored charter, or the zero Charter.
func (r *Repository) Charter(ctx context.Context) (domain.Charter, error) {
	c, err := charterRow(ctx, r.db.SQL())
	if err != nil {
		return domain.Charter{}, fmt.Errorf("get charter: %w", err)
	}
	return c, nil
}

func charterRow(ctx context.Context, q queryer) (domain.Charter, error) {
	var (
		c    domain.Charter
		when string
	)
	err := q.QueryRowContext(ctx, `SELECT text, version, updated_at, updated_by FROM charter WHERE id = 1`).
		Scan(&c.Text, &c.Version, &when, &c.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Charter{}, nil
	}
	if err != nil {
		return domain.Charter{}, err
	}
	if c.UpdatedAt, err = parseTime(when); err != nil {
		return domain.Charter{}, err
	}
	return c, nil
}

// SaveCharter stores c when it is the next version.
func (r *Repository) SaveCharter(ctx context.Context, c domain.Charter) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		current, err := charterRow(ctx, tx)
		if err != nil {
			return err
		}
		if c.Version != current.Version+1 {
			return fmt.Errorf("%w: version %d after %d", domain.ErrStaleVersion, c.Version, current.Version)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO charter (id, text, version, updated_at, updated_by) VALUES (1, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET text = excluded.text, version = excluded.version,
			updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
			c.Text, c.Version, formatTime(c.UpdatedAt), c.UpdatedBy)
		return err
	})
	if err != nil {
		return fmt.Errorf("save charter: %w", err)
	}
	return nil
}

// Goals.

const goalColumns = `id, owner, title, detail, status, parent_id, created_at, updated_at`

func scanGoal(s scanner) (domain.Goal, error) {
	var (
		g                  domain.Goal
		status, made, edit string
	)
	if err := s.Scan(&g.ID, &g.Owner, &g.Title, &g.Detail, &status, &g.ParentID, &made, &edit); err != nil {
		return domain.Goal{}, err
	}
	g.Status = domain.GoalStatus(status)
	var err error
	if g.CreatedAt, err = parseTime(made); err != nil {
		return domain.Goal{}, err
	}
	if g.UpdatedAt, err = parseTime(edit); err != nil {
		return domain.Goal{}, err
	}
	return g, nil
}

// CreateGoal stores g.
func (r *Repository) CreateGoal(ctx context.Context, g domain.Goal) error {
	_, err := r.db.SQL().ExecContext(ctx, `INSERT INTO goals (`+goalColumns+`) VALUES (?,?,?,?,?,?,?,?)`,
		g.ID, g.Owner, g.Title, g.Detail, string(g.Status), g.ParentID, formatTime(g.CreatedAt), formatTime(g.UpdatedAt))
	if err != nil {
		return fmt.Errorf("create goal %s: %w", g.ID, err)
	}
	return nil
}

// GetGoal returns the goal with id.
func (r *Repository) GetGoal(ctx context.Context, id string) (domain.Goal, error) {
	g, err := scanGoal(r.db.SQL().QueryRowContext(ctx, `SELECT `+goalColumns+` FROM goals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %s", domain.ErrGoalNotFound, id)
	}
	if err != nil {
		return domain.Goal{}, fmt.Errorf("get goal %s: %w", id, err)
	}
	return g, nil
}

func affectedOne(res sql.Result, err error, notFound error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound
	}
	return nil
}

// SaveGoal replaces an existing goal.
func (r *Repository) SaveGoal(ctx context.Context, g domain.Goal) error {
	res, err := r.db.SQL().ExecContext(ctx, `UPDATE goals SET owner = ?, title = ?, detail = ?, status = ?,
		parent_id = ?, created_at = ?, updated_at = ? WHERE id = ?`,
		g.Owner, g.Title, g.Detail, string(g.Status), g.ParentID, formatTime(g.CreatedAt), formatTime(g.UpdatedAt), g.ID)
	if err := affectedOne(res, err, fmt.Errorf("%w: %s", domain.ErrGoalNotFound, g.ID)); err != nil {
		return fmt.Errorf("save goal: %w", err)
	}
	return nil
}

// DeleteGoal removes the goal with id.
func (r *Repository) DeleteGoal(ctx context.Context, id string) error {
	res, err := r.db.SQL().ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, id)
	if err := affectedOne(res, err, fmt.Errorf("%w: %s", domain.ErrGoalNotFound, id)); err != nil {
		return fmt.Errorf("delete goal: %w", err)
	}
	return nil
}

// ListGoals returns matching goals sorted by ID.
func (r *Repository) ListGoals(ctx context.Context, f domain.GoalFilter) ([]domain.Goal, error) {
	query := `SELECT ` + goalColumns + ` FROM goals WHERE 1 = 1`
	var args []any
	if f.Owner != "" {
		query += ` AND owner = ?`
		args = append(args, f.Owner)
	}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, string(f.Status))
	}
	if f.AfterID != "" {
		query += ` AND id > ?`
		args = append(args, f.AfterID)
	}
	query += ` ORDER BY id`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := r.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Goal{}
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, fmt.Errorf("list goals: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	return out, nil
}

// Budget.

// Pool returns the last pushed pool, with Known false before any push.
func (r *Repository) Pool(ctx context.Context) (domain.Pool, error) {
	var (
		p                domain.Pool
		enabled          int
		start, end, asOf string
	)
	err := r.db.SQL().QueryRowContext(ctx, `SELECT currency, total_micros, spent_micros, defence_enabled,
		defence_reserved_micros, defence_spent_micros, period_start, period_end, check_interval_seconds, as_of
		FROM budget_pool WHERE id = 1`).Scan(&p.Currency, &p.TotalMicros, &p.SpentMicros, &enabled,
		&p.Defence.ReservedMicros, &p.Defence.SpentMicros, &start, &end, &p.CheckIntervalSeconds, &asOf)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Pool{}, nil
	}
	if err != nil {
		return domain.Pool{}, fmt.Errorf("get budget pool: %w", err)
	}
	p.Defence.Enabled, p.Known = enabled == 1, true
	for _, f := range []struct {
		dst *time.Time
		src string
	}{{&p.PeriodStart, start}, {&p.PeriodEnd, end}, {&p.AsOf, asOf}} {
		if *f.dst, err = parseTime(f.src); err != nil {
			return domain.Pool{}, fmt.Errorf("get budget pool: %w", err)
		}
	}
	return p, nil
}

// SavePool stores p as the one pool.
func (r *Repository) SavePool(ctx context.Context, p domain.Pool) error {
	_, err := r.db.SQL().ExecContext(ctx, `INSERT INTO budget_pool (id, currency, total_micros, spent_micros,
		defence_enabled, defence_reserved_micros, defence_spent_micros, period_start, period_end,
		check_interval_seconds, as_of) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET currency = excluded.currency, total_micros = excluded.total_micros,
		spent_micros = excluded.spent_micros, defence_enabled = excluded.defence_enabled,
		defence_reserved_micros = excluded.defence_reserved_micros, defence_spent_micros = excluded.defence_spent_micros,
		period_start = excluded.period_start, period_end = excluded.period_end,
		check_interval_seconds = excluded.check_interval_seconds, as_of = excluded.as_of`,
		p.Currency, p.TotalMicros, p.SpentMicros, boolInt(p.Defence.Enabled), p.Defence.ReservedMicros,
		p.Defence.SpentMicros, formatTime(p.PeriodStart), formatTime(p.PeriodEnd), p.CheckIntervalSeconds, formatTime(p.AsOf))
	if err != nil {
		return fmt.Errorf("save budget pool: %w", err)
	}
	return nil
}

func amounts(ctx context.Context, q queryer, query string) (map[string]int64, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var (
			agent string
			v     int64
		)
		if err := rows.Scan(&agent, &v); err != nil {
			return nil, err
		}
		out[agent] = v
	}
	return out, rows.Err()
}

// Allocations returns every explicit allocation.
func (r *Repository) Allocations(ctx context.Context) (map[string]int64, error) {
	out, err := amounts(ctx, r.db.SQL(), `SELECT agent, allocated_micros FROM budget_allocations`)
	if err != nil {
		return nil, fmt.Errorf("list allocations: %w", err)
	}
	return out, nil
}

// SetAllocation stores or, for nil, removes agent's allocation.
func (r *Repository) SetAllocation(ctx context.Context, agent string, amount *int64) error {
	var err error
	if amount == nil {
		_, err = r.db.SQL().ExecContext(ctx, `DELETE FROM budget_allocations WHERE agent = ?`, agent)
	} else {
		_, err = r.db.SQL().ExecContext(ctx, `INSERT INTO budget_allocations (agent, allocated_micros) VALUES (?, ?)
			ON CONFLICT (agent) DO UPDATE SET allocated_micros = excluded.allocated_micros`, agent, *amount)
	}
	if err != nil {
		return fmt.Errorf("set allocation of %s: %w", agent, err)
	}
	return nil
}

// Usage returns recorded spending per agent.
func (r *Repository) Usage(ctx context.Context) (map[string]int64, error) {
	out, err := amounts(ctx, r.db.SQL(), `SELECT agent, spent_micros FROM budget_usage`)
	if err != nil {
		return nil, fmt.Errorf("list usage: %w", err)
	}
	return out, nil
}

// AddUsage adds micros to agent's spending.
func (r *Repository) AddUsage(ctx context.Context, agent string, micros int64) error {
	_, err := r.db.SQL().ExecContext(ctx, `INSERT INTO budget_usage (agent, spent_micros) VALUES (?, ?)
		ON CONFLICT (agent) DO UPDATE SET spent_micros = spent_micros + excluded.spent_micros`, agent, micros)
	if err != nil {
		return fmt.Errorf("add usage of %s: %w", agent, err)
	}
	return nil
}

// Freezes.

// Freezes returns the active freeze scopes, sorted.
func (r *Repository) Freezes(ctx context.Context) ([]string, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT scope FROM freezes ORDER BY scope`)
	if err != nil {
		return nil, fmt.Errorf("list freezes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, fmt.Errorf("list freezes: %w", err)
		}
		out = append(out, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list freezes: %w", err)
	}
	return out, nil
}

// AddFreeze stores scope; it is a no-op for an active scope.
func (r *Repository) AddFreeze(ctx context.Context, scope string) error {
	if _, err := r.db.SQL().ExecContext(ctx, `INSERT INTO freezes (scope) VALUES (?) ON CONFLICT (scope) DO NOTHING`, scope); err != nil {
		return fmt.Errorf("add freeze %s: %w", scope, err)
	}
	return nil
}

// Seed record.

// SeedApplied reports whether the seed was recorded.
func (r *Repository) SeedApplied(ctx context.Context) (bool, error) {
	var n int
	if err := r.db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM seed`).Scan(&n); err != nil {
		return false, fmt.Errorf("read seed record: %w", err)
	}
	return n > 0, nil
}

// MarkSeedApplied records the seed; it keeps the first record.
func (r *Repository) MarkSeedApplied(ctx context.Context, at time.Time) (bool, error) {
	res, err := r.db.SQL().ExecContext(ctx, `INSERT INTO seed (id, applied_at) VALUES (1, ?) ON CONFLICT (id) DO NOTHING`, formatTime(at))
	if err != nil {
		return false, fmt.Errorf("record seed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record seed: %w", err)
	}
	return n == 1, nil
}

// RemoveFreeze removes scope; it is a no-op for an inactive scope.
func (r *Repository) RemoveFreeze(ctx context.Context, scope string) error {
	if _, err := r.db.SQL().ExecContext(ctx, `DELETE FROM freezes WHERE scope = ?`, scope); err != nil {
		return fmt.Errorf("remove freeze %s: %w", scope, err)
	}
	return nil
}

// Chat.

// AppendChat stores m.
func (r *Repository) AppendChat(ctx context.Context, m domain.ChatMessage) error {
	_, err := r.db.SQL().ExecContext(ctx, `INSERT INTO chat_messages (id, agent, role, author, text, assignment_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, m.ID, m.Agent, string(m.Role), m.Author, m.Text, m.AssignmentID, formatTime(m.CreatedAt))
	if err != nil {
		return fmt.Errorf("append chat message %s: %w", m.ID, err)
	}
	return nil
}

// ListChat returns agent's messages newest first.
func (r *Repository) ListChat(ctx context.Context, agent, beforeID string, limit int) ([]domain.ChatMessage, error) {
	query := `SELECT id, agent, role, author, text, assignment_id, created_at FROM chat_messages WHERE agent = ?`
	args := []any{agent}
	if beforeID != "" {
		query += ` AND id < ?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list chat of %s: %w", agent, err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.ChatMessage{}
	for rows.Next() {
		var (
			m          domain.ChatMessage
			role, when string
		)
		if err := rows.Scan(&m.ID, &m.Agent, &role, &m.Author, &m.Text, &m.AssignmentID, &when); err != nil {
			return nil, fmt.Errorf("list chat of %s: %w", agent, err)
		}
		m.Role = domain.ChatRole(role)
		if m.CreatedAt, err = parseTime(when); err != nil {
			return nil, fmt.Errorf("list chat of %s: %w", agent, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list chat of %s: %w", agent, err)
	}
	return out, nil
}
