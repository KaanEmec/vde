-- PostgreSQL twin of migrations/0002. result, feedback and error are json (not jsonb):
-- the stored text comes back byte for byte, as on SQLite.
CREATE TABLE jobs (
    id                     TEXT    PRIMARY KEY,     -- j_<ULID>
    application_id         TEXT    NOT NULL REFERENCES applications (id),
    status                 TEXT    NOT NULL CHECK (status IN ('queued', 'running', 'accepted', 'rejected', 'failed')),
    attempts               INTEGER NOT NULL DEFAULT 0,
    result                 JSON,
    feedback               JSON,
    error                  JSON,
    judge_confidence       DOUBLE PRECISION,
    service_version        TEXT    NOT NULL,
    representation         TEXT    NOT NULL,
    media_type             TEXT    NOT NULL,
    size                   BIGINT  NOT NULL,
    page_count             INTEGER NOT NULL DEFAULT 0,
    retention              TEXT    NOT NULL CHECK (retention IN ('none', 'default')),
    idempotency_key        TEXT,
    body_hash              TEXT    NOT NULL DEFAULT '',
    extractor_instructions TEXT    NOT NULL DEFAULT '',
    judge_instructions     TEXT    NOT NULL DEFAULT '',
    schema                 TEXT    NOT NULL DEFAULT '',
    max_attempts           INTEGER NOT NULL DEFAULT 0,
    created_at             BIGINT  NOT NULL,
    started_at             BIGINT,
    finished_at            BIGINT,
    expires_at             BIGINT  NOT NULL,
    lease_until            BIGINT,
    worker_id              TEXT    NOT NULL DEFAULT '',
    inputs_deleted_at      BIGINT,                  -- document, pages, instructions, schema gone
    content_deleted_at     BIGINT                   -- result, feedback and error gone too
);

CREATE UNIQUE INDEX jobs_idempotency ON jobs (application_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX jobs_queue ON jobs (status, created_at);
CREATE INDEX jobs_application_status ON jobs (application_id, status);
CREATE INDEX jobs_expires ON jobs (expires_at);
