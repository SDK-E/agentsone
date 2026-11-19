-- SPDX-License-Identifier: MIT
CREATE TABLE plugins (
    id                TEXT    PRIMARY KEY,
    name              TEXT    NOT NULL,
    version           TEXT    NOT NULL,
    kinds             TEXT    NOT NULL,
    source            TEXT    NOT NULL,
    ref               TEXT    NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    health            TEXT    NOT NULL CHECK (health IN ('ok', 'degraded', 'failed')),
    installed_by_kind TEXT    NOT NULL,
    installed_by_name TEXT    NOT NULL,
    installed_at      TEXT    NOT NULL,
    manifest          TEXT    NOT NULL,
    dir               TEXT    NOT NULL,
    sha256            TEXT    NOT NULL,
    UNIQUE (name, version)
) STRICT;

CREATE INDEX plugins_name_installed ON plugins (name, installed_at DESC);
