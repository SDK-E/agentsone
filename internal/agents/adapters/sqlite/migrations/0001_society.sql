-- SPDX-License-Identifier: MIT
CREATE TABLE agents (
    name         TEXT    PRIMARY KEY,
    display_name TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    is_root      INTEGER NOT NULL CHECK (is_root IN (0, 1)),
    runner       TEXT    NOT NULL,
    manager      TEXT    NOT NULL,
    role         TEXT    NOT NULL,
    description  TEXT    NOT NULL,
    state        TEXT    NOT NULL,
    cpu_percent  INTEGER,
    memory_mb    INTEGER,
    tasks        INTEGER,
    uid          INTEGER NOT NULL,
    home         TEXT    NOT NULL,
    template     TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    created_by   TEXT    NOT NULL,
    last_turn_at TEXT
) STRICT;

CREATE INDEX agents_manager ON agents (manager);

CREATE TABLE charter (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    text       TEXT    NOT NULL,
    version    INTEGER NOT NULL,
    updated_at TEXT    NOT NULL,
    updated_by TEXT    NOT NULL
) STRICT;

CREATE TABLE goals (
    id         TEXT PRIMARY KEY,
    owner      TEXT NOT NULL,
    title      TEXT NOT NULL,
    detail     TEXT NOT NULL,
    status     TEXT NOT NULL,
    parent_id  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE INDEX goals_owner ON goals (owner, id);

CREATE TABLE budget_pool (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    currency               TEXT    NOT NULL,
    total_micros           INTEGER NOT NULL,
    spent_micros           INTEGER NOT NULL,
    defence_enabled        INTEGER NOT NULL CHECK (defence_enabled IN (0, 1)),
    defence_reserved_micros INTEGER NOT NULL,
    defence_spent_micros   INTEGER NOT NULL,
    period_start           TEXT    NOT NULL,
    period_end             TEXT    NOT NULL,
    check_interval_seconds INTEGER NOT NULL,
    as_of                  TEXT    NOT NULL
) STRICT;

CREATE TABLE budget_allocations (
    agent            TEXT    PRIMARY KEY,
    allocated_micros INTEGER NOT NULL
) STRICT;

CREATE TABLE budget_usage (
    agent        TEXT    PRIMARY KEY,
    spent_micros INTEGER NOT NULL
) STRICT;

CREATE TABLE chat_messages (
    id            TEXT PRIMARY KEY,
    agent         TEXT NOT NULL,
    role          TEXT NOT NULL,
    author        TEXT NOT NULL,
    text          TEXT NOT NULL,
    assignment_id TEXT NOT NULL,
    created_at    TEXT NOT NULL
) STRICT;

CREATE INDEX chat_messages_agent ON chat_messages (agent, id);
