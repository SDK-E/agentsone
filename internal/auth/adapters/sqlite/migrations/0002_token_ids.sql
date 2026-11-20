-- SPDX-License-Identifier: MIT
-- Tokens were kept under a hex id while the API answered and took a
-- different tok_ id, so no token issued through the API could be revoked
-- through it. From now on the store keeps the contract's tok_ id. Every
-- live token of the earlier form is revoked here: none of them can be
-- revoked by its id, and the daemon no longer accepts its value. Runners
-- get a fresh token when they next connect, which every runner does after
-- the daemon starts; a tool that held an issued token needs a new one.
-- Sign-in sessions live in auth_sessions and are not touched.
--
-- auth_legacy_revokes lists the ids revoked here until the module has
-- written one audit record for each (the audit log is not open while
-- migrations run). It holds ids only, never a value or a hash.
CREATE TABLE auth_legacy_revokes (
    id TEXT PRIMARY KEY
) STRICT;

INSERT INTO auth_legacy_revokes (id)
SELECT id FROM auth_tokens
WHERE revoked_at IS NULL AND substr(id, 1, 4) <> 'tok_';

UPDATE auth_tokens
SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE revoked_at IS NULL AND substr(id, 1, 4) <> 'tok_';
