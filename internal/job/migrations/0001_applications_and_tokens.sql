-- Applications mirror the configuration (upserted at start); tokens belong to one.
-- Times are Unix milliseconds, UTC.
CREATE TABLE applications (
    id                  TEXT    PRIMARY KEY,
    name                TEXT    NOT NULL DEFAULT '',
    max_attempts        INTEGER NOT NULL,
    quota_limit         INTEGER NOT NULL,
    quota_window        TEXT    NOT NULL,
    requests_per_minute INTEGER NOT NULL,
    submits_per_minute  INTEGER NOT NULL,
    max_in_progress     INTEGER NOT NULL,
    retention_default   TEXT    NOT NULL,           -- a duration such as 24h, or none
    force_none          INTEGER NOT NULL DEFAULT 0,
    html_policy         TEXT    NOT NULL DEFAULT '',
    enabled             INTEGER NOT NULL DEFAULT 1,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
) STRICT;

CREATE TABLE tokens (
    id             TEXT    PRIMARY KEY,             -- the clear prefix of the token
    application_id TEXT    NOT NULL REFERENCES applications (id),
    hash           BLOB    NOT NULL,                -- SHA-256 of the secret part
    scopes         TEXT    NOT NULL,                -- comma-separated
    label          TEXT    NOT NULL DEFAULT '',
    expires_at     INTEGER NOT NULL,
    revoked_at     INTEGER,
    created_at     INTEGER NOT NULL,
    last_used_at   INTEGER
) STRICT;

CREATE INDEX tokens_application ON tokens (application_id);
