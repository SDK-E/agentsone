-- SPDX-License-Identifier: MIT
CREATE TABLE router_lines (
    agent   TEXT PRIMARY KEY,
    line    TEXT NOT NULL,
    updated TEXT NOT NULL
) STRICT;

CREATE TABLE router_assignments (
    id              TEXT PRIMARY KEY,
    text            TEXT NOT NULL,
    chosen          TEXT NOT NULL,
    router          TEXT NOT NULL,
    hops            TEXT NOT NULL,
    shortlist       TEXT NOT NULL,
    chat_path       TEXT NOT NULL,
    chat_message_id TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      TEXT NOT NULL
) STRICT;
