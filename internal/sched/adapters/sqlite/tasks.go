// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/SDK-E/agentsone/internal/sched/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// Tasks stores tasks in core.db.
type Tasks struct{ db *store.DB }

var _ domain.TaskRepository = (*Tasks)(nil)

// NewTasks returns a repository on a migrated database.
func NewTasks(db *store.DB) *Tasks { return &Tasks{db: db} }

const taskColumns = `id, title, detail, status, priority, assignee, source, created_by,
	parent_id, trigger_id, result, due_at, created_at, updated_at`

// Insert stores a new task.
func (r *Tasks) Insert(ctx context.Context, t domain.Task) error {
	_, err := r.db.SQL().ExecContext(ctx,
		`INSERT INTO sched_tasks (`+taskColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Title, t.Detail, string(t.Status), string(t.Priority), nullable(t.Assignee),
		string(t.Source), t.CreatedBy.String(), nullable(t.ParentID), nullable(t.TriggerID),
		nullable(t.Result), formatTimePtr(t.DueAt), formatTime(t.CreatedAt), formatTime(t.UpdatedAt))
	if isUnique(err) {
		return fmt.Errorf("task %s: %w", t.ID, domain.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert task %s: %w", t.ID, err)
	}
	return nil
}

// Update replaces a stored task.
func (r *Tasks) Update(ctx context.Context, t domain.Task) error {
	res, err := r.db.SQL().ExecContext(ctx,
		`UPDATE sched_tasks SET title=?, detail=?, status=?, priority=?, assignee=?, source=?, created_by=?,
			parent_id=?, trigger_id=?, result=?, due_at=?, created_at=?, updated_at=? WHERE id=?`,
		t.Title, t.Detail, string(t.Status), string(t.Priority), nullable(t.Assignee),
		string(t.Source), t.CreatedBy.String(), nullable(t.ParentID), nullable(t.TriggerID),
		nullable(t.Result), formatTimePtr(t.DueAt), formatTime(t.CreatedAt), formatTime(t.UpdatedAt), t.ID)
	if err != nil {
		return fmt.Errorf("update task %s: %w", t.ID, err)
	}
	return requireRow(res, "task", t.ID)
}

func requireRow(res sql.Result, kind, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s %s: %w", kind, id, err)
	}
	if n == 0 {
		return fmt.Errorf("%s %s: %w", kind, id, domain.ErrNotFound)
	}
	return nil
}

// Get returns a task.
func (r *Tasks) Get(ctx context.Context, id string) (domain.Task, error) {
	row := r.db.SQL().QueryRowContext(ctx, `SELECT `+taskColumns+` FROM sched_tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if isNoRows(err) {
		return domain.Task{}, fmt.Errorf("task %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	return t, nil
}

// List returns matching tasks, newest first.
func (r *Tasks) List(ctx context.Context, f domain.TaskFilter) ([]domain.Task, error) {
	var where []string
	var args []any
	add := func(clause string, v string) {
		if v != "" {
			where = append(where, clause)
			args = append(args, v)
		}
	}
	add("status = ?", string(f.Status))
	add("assignee = ?", f.Assignee)
	add("source = ?", string(f.Source))
	add("id < ?", f.BeforeID)
	query := `SELECT ` + taskColumns + ` FROM sched_tasks`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY id DESC`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := r.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return out, nil
}

func scanTask(s scanner) (domain.Task, error) {
	var (
		t                                      domain.Task
		status, priority, source, createdBy    string
		assignee, parent, trigger, result, due sql.NullString
		created, updated                       string
	)
	if err := s.Scan(&t.ID, &t.Title, &t.Detail, &status, &priority, &assignee, &source, &createdBy,
		&parent, &trigger, &result, &due, &created, &updated); err != nil {
		return domain.Task{}, err
	}
	actor, err := domain.ParseActor(createdBy)
	if err != nil {
		return domain.Task{}, err
	}
	t.Status, t.Priority, t.Source, t.CreatedBy = domain.Status(status), domain.Priority(priority), domain.Source(source), actor
	t.Assignee, t.ParentID, t.TriggerID, t.Result = assignee.String, parent.String, trigger.String, result.String
	if t.DueAt, err = parseTimePtr(due); err != nil {
		return domain.Task{}, err
	}
	if t.CreatedAt, err = parseTime(created); err != nil {
		return domain.Task{}, err
	}
	if t.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.Task{}, err
	}
	return t, nil
}
