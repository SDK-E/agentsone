// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/sched/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// Schedules stores schedules in core.db.
type Schedules struct{ db *store.DB }

var _ domain.ScheduleRepository = (*Schedules)(nil)

// NewSchedules returns a repository on a migrated database.
func NewSchedules(db *store.DB) *Schedules { return &Schedules{db: db} }

const scheduleColumns = `id, agent, name, calendar, message, enabled, next_run, last_run, created_at, created_by`

// Insert stores a new schedule.
func (r *Schedules) Insert(ctx context.Context, s domain.Schedule) error {
	_, err := r.db.SQL().ExecContext(ctx,
		`INSERT INTO sched_schedules (`+scheduleColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.Agent, s.Name, s.Calendar.String(), s.Message, boolInt(s.Enabled),
		formatTimePtr(s.NextRun), formatTimePtr(s.LastRun), formatTime(s.CreatedAt), s.CreatedBy.String())
	if isUnique(err) {
		return fmt.Errorf("schedule %s: %w", s.ID, domain.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert schedule %s: %w", s.ID, err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Update replaces a stored schedule.
func (r *Schedules) Update(ctx context.Context, s domain.Schedule) error {
	res, err := r.db.SQL().ExecContext(ctx,
		`UPDATE sched_schedules SET agent=?, name=?, calendar=?, message=?, enabled=?, next_run=?, last_run=?,
			created_at=?, created_by=? WHERE id=?`,
		s.Agent, s.Name, s.Calendar.String(), s.Message, boolInt(s.Enabled),
		formatTimePtr(s.NextRun), formatTimePtr(s.LastRun), formatTime(s.CreatedAt), s.CreatedBy.String(), s.ID)
	if err != nil {
		return fmt.Errorf("update schedule %s: %w", s.ID, err)
	}
	return requireRow(res, "schedule", s.ID)
}

// Get returns a schedule.
func (r *Schedules) Get(ctx context.Context, id string) (domain.Schedule, error) {
	row := r.db.SQL().QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sched_schedules WHERE id = ?`, id)
	s, err := scanSchedule(row)
	if isNoRows(err) {
		return domain.Schedule{}, fmt.Errorf("schedule %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Schedule{}, fmt.Errorf("get schedule %s: %w", id, err)
	}
	return s, nil
}

// Delete removes a schedule.
func (r *Schedules) Delete(ctx context.Context, id string) error {
	res, err := r.db.SQL().ExecContext(ctx, `DELETE FROM sched_schedules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete schedule %s: %w", id, err)
	}
	return requireRow(res, "schedule", id)
}

// List returns matching schedules, oldest first.
func (r *Schedules) List(ctx context.Context, f domain.ScheduleFilter) ([]domain.Schedule, error) {
	var where []string
	var args []any
	if f.Agent != "" {
		where, args = append(where, "agent = ?"), append(args, f.Agent)
	}
	if f.AfterID != "" {
		where, args = append(where, "id > ?"), append(args, f.AfterID)
	}
	query := `SELECT ` + scheduleColumns + ` FROM sched_schedules`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY id`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	return r.query(ctx, query, args...)
}

// Due returns enabled schedules whose next run is at or before now.
func (r *Schedules) Due(ctx context.Context, now time.Time) ([]domain.Schedule, error) {
	return r.query(ctx, `SELECT `+scheduleColumns+` FROM sched_schedules
		WHERE enabled = 1 AND next_run IS NOT NULL AND next_run <= ? ORDER BY id`, formatTime(now))
}

func (r *Schedules) query(ctx context.Context, query string, args ...any) ([]domain.Schedule, error) {
	rows, err := r.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("query schedules: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query schedules: %w", err)
	}
	return out, nil
}

func scanSchedule(s scanner) (domain.Schedule, error) {
	var (
		out                          domain.Schedule
		calendar, createdBy, created string
		enabled                      int
		next, last                   sql.NullString
	)
	if err := s.Scan(&out.ID, &out.Agent, &out.Name, &calendar, &out.Message, &enabled, &next, &last, &created, &createdBy); err != nil {
		return domain.Schedule{}, err
	}
	var err error
	if out.Calendar, err = domain.ParseCalendar(calendar); err != nil {
		return domain.Schedule{}, err
	}
	if out.CreatedBy, err = domain.ParseActor(createdBy); err != nil {
		return domain.Schedule{}, err
	}
	out.Enabled = enabled == 1
	if out.NextRun, err = parseTimePtr(next); err != nil {
		return domain.Schedule{}, err
	}
	if out.LastRun, err = parseTimePtr(last); err != nil {
		return domain.Schedule{}, err
	}
	if out.CreatedAt, err = parseTime(created); err != nil {
		return domain.Schedule{}, err
	}
	return out, nil
}
