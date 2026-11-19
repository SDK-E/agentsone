-- SPDX-License-Identifier: MIT
CREATE TABLE bus_channels (
    name            TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('public', 'team')),
    description     TEXT NOT NULL,
    team            TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    last_message_at TEXT
) STRICT;

CREATE TABLE bus_messages (
    seq            INTEGER PRIMARY KEY AUTOINCREMENT,
    id             TEXT NOT NULL UNIQUE,
    channel        TEXT,
    recipient      TEXT,
    sender         TEXT NOT NULL,
    body           TEXT NOT NULL,
    reply_to       TEXT,
    correlation_id TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    CHECK ((channel IS NULL) <> (recipient IS NULL))
) STRICT;

CREATE INDEX bus_messages_channel ON bus_messages (channel, seq);
CREATE INDEX bus_messages_recipient ON bus_messages (recipient, seq);
CREATE INDEX bus_messages_sender ON bus_messages (sender, seq);
