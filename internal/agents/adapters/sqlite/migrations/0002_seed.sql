-- SPDX-License-Identifier: MIT
-- One row once the machine's first society was seeded: the seed runs once per machine.
CREATE TABLE seed (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    applied_at TEXT    NOT NULL
) STRICT;
