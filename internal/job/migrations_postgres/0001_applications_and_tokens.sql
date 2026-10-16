-- PostgreSQL twin of migrations/0001: the same columns, times as Unix milliseconds
-- (BIGINT) like SQLite, booleans as BOOLEAN.
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
    force_none          BOOLEAN NOT NULL DEFAULT FALSE,
    html_policy         TEXT    NOT NULL DEFAULT '',
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          BIGINT  NOT NULL,
    updated_at          BIGINT  NOT NULL
);

CREATE TABLE tokens (
    id             TEXT    PRIMARY KEY,             -- the clear prefix of the token
    application_id TEXT    NOT NULL REFERENCES applications (id),
    hash           BYTEA   NOT NULL,                -- SHA-256 of the secret part
    scopes         TEXT    NOT NULL,                -- comma-separated
    label          TEXT    NOT NULL DEFAULT '',
    expires_at     BIGINT  NOT NULL,
    revoked_at     BIGINT,
    created_at     BIGINT  NOT NULL,
    last_used_at   BIGINT
);

CREATE INDEX tokens_application ON tokens (application_id);
