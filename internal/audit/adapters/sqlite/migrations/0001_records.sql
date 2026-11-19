-- SPDX-License-Identifier: MIT
CREATE TABLE audit_records (
    seq        INTEGER PRIMARY KEY,
    time       TEXT    NOT NULL,
    actor_kind TEXT    NOT NULL,
    actor_name TEXT    NOT NULL,
    actor_uid  INTEGER,
    action     TEXT    NOT NULL,
    target_kind TEXT   NOT NULL,
    target_id  TEXT    NOT NULL,
    outcome    TEXT    NOT NULL,
    details    TEXT    NOT NULL,
    origin     TEXT    NOT NULL,
    prev_hash  TEXT    NOT NULL,
    hash       TEXT    NOT NULL UNIQUE
) STRICT;
