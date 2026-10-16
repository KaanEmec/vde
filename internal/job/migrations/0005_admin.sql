-- Admin API (epic 11). Applications record where they come from: the configuration
-- (rewritten at every start, disabled when removed) or the admin API (kept as stored).
ALTER TABLE applications ADD COLUMN source TEXT NOT NULL DEFAULT 'config'
    CHECK (source IN ('config', 'admin'));

-- Admin tokens live beside application tokens with no application and the single scope
-- admin; SQLite cannot drop NOT NULL in place, so the table is rebuilt.
CREATE TABLE tokens_new (
    id             TEXT    PRIMARY KEY,             -- the clear prefix of the token
    application_id TEXT    REFERENCES applications (id), -- NULL for an admin token
    hash           BLOB    NOT NULL,                -- SHA-256 of the secret part
    scopes         TEXT    NOT NULL,                -- comma-separated
    label          TEXT    NOT NULL DEFAULT '',
    expires_at     INTEGER NOT NULL,
    revoked_at     INTEGER,
    created_at     INTEGER NOT NULL,
    last_used_at   INTEGER,
    CHECK ((application_id IS NULL) = (scopes = 'admin'))
) STRICT;

INSERT INTO tokens_new (id, application_id, hash, scopes, label, expires_at, revoked_at, created_at, last_used_at)
    SELECT id, application_id, hash, scopes, label, expires_at, revoked_at, created_at, last_used_at FROM tokens;
DROP TABLE tokens;
ALTER TABLE tokens_new RENAME TO tokens;
CREATE INDEX tokens_application ON tokens (application_id);
