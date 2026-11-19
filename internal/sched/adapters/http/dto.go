// SPDX-License-Identifier: MIT

package httpadapter

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/domain"
)

// Field names of the wire format live only in the generated types, so a
// contract rename stays a change to this file alone.

func toTask(t domain.Task) api.Task {
	out := api.Task{
		Id: t.ID, Title: t.Title, Detail: t.Detail,
		Status: api.TaskStatus(t.Status), Priority: api.TaskPriority(t.Priority), Source: api.TaskSource(t.Source),
		CreatedBy: t.CreatedBy.String(), CreatedAt: api.Timestamp(t.CreatedAt.UTC()), UpdatedAt: api.Timestamp(t.UpdatedAt.UTC()),
		Assignee: nilIfEmpty(t.Assignee), ParentId: nilIfEmpty(t.ParentID),
		TriggerId: nilIfEmpty(t.TriggerID), Result: nilIfEmpty(t.Result),
	}
	out.DueAt = utcOrNil(t.DueAt)
	return out
}

func toSchedule(s domain.Schedule) api.Schedule {
	return api.Schedule{
		Id: s.ID, Agent: s.Agent, Name: s.Name, Calendar: s.Calendar.String(), Message: s.Message,
		Enabled: s.Enabled, NextRunAt: utcOrNil(s.NextRun), LastRunAt: utcOrNil(s.LastRun),
		CreatedAt: api.Timestamp(s.CreatedAt.UTC()), CreatedBy: s.CreatedBy.String(),
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func utcOrNil(t *time.Time) *api.Timestamp {
	if t == nil {
		return nil
	}
	u := api.Timestamp(t.UTC())
	return &u
}

// timeOrNil is the reverse of utcOrNil.
func timeOrNil(t *api.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	u := time.Time(*t)
	return &u
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// defaultLimit and maxLimit are the page sizes the contract allows.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// pageSize is the limit a request asks for, within the contract's bounds.
func pageSize(p *api.Limit) (int, error) {
	n := valueOr(p, defaultLimit)
	if n < 1 || n > maxLimit {
		return 0, fmt.Errorf("%w: limit must be 1 to %d", core.ErrInvalid, maxLimit)
	}
	return n, nil
}

// encodeCursor hides a row id in an opaque cursor.
func encodeCursor(id string) *api.Cursor {
	c := base64.RawURLEncoding.EncodeToString([]byte(id))
	return &c
}

// decodeCursor reads a cursor made by encodeCursor; nil or empty means the first page.
func decodeCursor(c *api.CursorParameter) (string, error) {
	if c == nil || *c == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(*c)
	if err != nil {
		return "", fmt.Errorf("%w: the cursor is not one this server made", core.ErrInvalid)
	}
	return string(raw), nil
}
