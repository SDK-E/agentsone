-- SPDX-License-Identifier: MIT
CREATE TABLE themes (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL UNIQUE,
    version      TEXT    NOT NULL,
    builtin      INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0, 1)),
    plugin_id    TEXT    NOT NULL DEFAULT '',
    installed_at TEXT    NOT NULL,
    document     TEXT    NOT NULL
) STRICT;

CREATE INDEX themes_plugin ON themes (plugin_id) WHERE plugin_id != '';

-- One row per human. A theme somebody selected cannot be deleted.
CREATE TABLE theme_selection (
    human    TEXT PRIMARY KEY,
    theme_id TEXT NOT NULL REFERENCES themes (id) ON DELETE RESTRICT,
    mode     TEXT NOT NULL CHECK (mode IN ('system', 'light', 'dark'))
) STRICT;

CREATE INDEX theme_selection_theme ON theme_selection (theme_id);
