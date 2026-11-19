-- SPDX-License-Identifier: MIT
CREATE TABLE runner_summaries (
    agent          TEXT PRIMARY KEY,
    text           TEXT NOT NULL,
    compacted_at   TEXT NOT NULL,
    paused_turn_id TEXT NOT NULL
) STRICT;
