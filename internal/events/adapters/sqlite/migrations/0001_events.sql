-- SPDX-License-Identifier: MIT
CREATE TABLE events (
    seq           INTEGER PRIMARY KEY AUTOINCREMENT,
    id            TEXT NOT NULL UNIQUE,
    source        TEXT NOT NULL,
    type          TEXT NOT NULL,
    subject       TEXT NOT NULL,
    time          TEXT NOT NULL,
    dataschema    TEXT NOT NULL,
    data          TEXT NOT NULL,
    actor         TEXT NOT NULL,
    chartscope    TEXT NOT NULL,
    causationid   TEXT NOT NULL,
    correlationid TEXT NOT NULL
) STRICT;

CREATE TABLE emitters (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL,
    description   TEXT NOT NULL,
    event_types   TEXT NOT NULL,
    enabled       INTEGER NOT NULL,
    unit          TEXT NOT NULL,
    owner         TEXT NOT NULL,
    plugin_id     TEXT NOT NULL,
    webhook_path  TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_event_at TEXT,
    command       TEXT NOT NULL,
    secret        TEXT NOT NULL
) STRICT;

CREATE TABLE triggers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    owner         TEXT NOT NULL,
    filter        TEXT NOT NULL,
    action        TEXT NOT NULL,
    rate_max      INTEGER NOT NULL,
    rate_per      INTEGER NOT NULL,
    enabled       INTEGER NOT NULL,
    fired_count   INTEGER NOT NULL DEFAULT 0,
    dropped_count INTEGER NOT NULL DEFAULT 0,
    last_fired_at TEXT,
    created_at    TEXT NOT NULL
) STRICT;
