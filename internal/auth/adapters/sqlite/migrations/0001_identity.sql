-- SPDX-License-Identifier: MIT
CREATE TABLE auth_humans (
    name          TEXT    PRIMARY KEY,
    display_name  TEXT    NOT NULL,
    uid           INTEGER NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    last_login_at TEXT
) STRICT;

CREATE TABLE auth_sessions (
    id         TEXT PRIMARY KEY,
    human      TEXT NOT NULL REFERENCES auth_humans(name) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
) STRICT;
CREATE INDEX auth_sessions_human ON auth_sessions(human);

CREATE TABLE auth_tokens (
    id           TEXT    PRIMARY KEY,
    hash         BLOB    NOT NULL,
    subject_kind TEXT    NOT NULL,
    subject_name TEXT    NOT NULL,
    scopes       TEXT    NOT NULL,
    issued_at    TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    revoked_at   TEXT
) STRICT;

CREATE TABLE auth_setup (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
