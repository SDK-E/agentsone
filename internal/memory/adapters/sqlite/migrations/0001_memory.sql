-- SPDX-License-Identifier: MIT
CREATE TABLE memory_records (
    id          TEXT    PRIMARY KEY,
    scope       TEXT    NOT NULL CHECK (scope IN ('private', 'team', 'common')),
    owner       TEXT    NOT NULL,
    team        TEXT    NOT NULL,
    key         TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    body        TEXT    NOT NULL,
    tags        TEXT    NOT NULL,
    links       TEXT    NOT NULL,
    source_kind TEXT    NOT NULL,
    source_ref  TEXT    NOT NULL,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL,
    revision    INTEGER NOT NULL CHECK (revision >= 1)
) STRICT;

-- One record per owner, scope and key; records without a key are not constrained.
CREATE UNIQUE INDEX memory_records_key ON memory_records (owner, scope, key) WHERE key <> '';
CREATE INDEX memory_records_scope ON memory_records (scope, id);
CREATE INDEX memory_records_owner ON memory_records (owner, id);

-- Full-text index over title, body and tags. The repository keeps it in step with
-- memory_records inside the same transaction.
CREATE VIRTUAL TABLE memory_fts USING fts5 (
    title,
    body,
    tags,
    record_id UNINDEXED,
    tokenize = 'unicode61'
);
