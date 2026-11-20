-- SPDX-License-Identifier: MIT
-- Events stored with the society change they announce, published after the
-- change commits. sent_at stays NULL until the events store has the event.
CREATE TABLE outbox (
    id          TEXT PRIMARY KEY,
    created_at  TEXT NOT NULL,
    source      TEXT NOT NULL,
    actor       TEXT NOT NULL,
    chart_scope TEXT NOT NULL,
    type        TEXT NOT NULL,
    data        TEXT NOT NULL,
    sent_at     TEXT
) STRICT;

CREATE INDEX outbox_pending ON outbox (created_at) WHERE sent_at IS NULL;
