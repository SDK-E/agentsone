-- SPDX-License-Identifier: MIT
CREATE TABLE sched_tasks (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    detail     TEXT NOT NULL,
    status     TEXT NOT NULL,
    priority   TEXT NOT NULL,
    assignee   TEXT,
    source     TEXT NOT NULL,
    created_by TEXT NOT NULL,
    parent_id  TEXT,
    trigger_id TEXT,
    result     TEXT,
    due_at     TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE INDEX sched_tasks_assignee ON sched_tasks (assignee) WHERE assignee IS NOT NULL;
CREATE INDEX sched_tasks_status ON sched_tasks (status);

CREATE TABLE sched_schedules (
    id         TEXT PRIMARY KEY,
    agent      TEXT NOT NULL,
    name       TEXT NOT NULL,
    calendar   TEXT NOT NULL,
    message    TEXT NOT NULL,
    enabled    INTEGER NOT NULL,
    next_run   TEXT,
    last_run   TEXT,
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL
) STRICT;

CREATE INDEX sched_schedules_agent ON sched_schedules (agent);
CREATE INDEX sched_schedules_due ON sched_schedules (next_run) WHERE enabled = 1 AND next_run IS NOT NULL;
