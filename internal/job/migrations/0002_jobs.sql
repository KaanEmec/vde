-- Jobs are the queue (ADR 0003). result, feedback and error are JSON text. The input
-- columns (instructions, schema) are caller content, cleared with the document blobs.
CREATE TABLE jobs (
    id                     TEXT    PRIMARY KEY,     -- j_<ULID>
    application_id         TEXT    NOT NULL REFERENCES applications (id),
    status                 TEXT    NOT NULL CHECK (status IN ('queued', 'running', 'accepted', 'rejected', 'failed')),
    attempts               INTEGER NOT NULL DEFAULT 0,
    result                 TEXT,
    feedback               TEXT,
    error                  TEXT,
    judge_confidence       REAL,
    service_version        TEXT    NOT NULL,
    representation         TEXT    NOT NULL,
    media_type             TEXT    NOT NULL,
    size                   INTEGER NOT NULL,
    page_count             INTEGER NOT NULL DEFAULT 0,
    retention              TEXT    NOT NULL CHECK (retention IN ('none', 'default')),
    idempotency_key        TEXT,
    body_hash              TEXT    NOT NULL DEFAULT '',
    extractor_instructions TEXT    NOT NULL DEFAULT '',
    judge_instructions     TEXT    NOT NULL DEFAULT '',
    schema                 TEXT    NOT NULL DEFAULT '',
    max_attempts           INTEGER NOT NULL DEFAULT 0,
    created_at             INTEGER NOT NULL,
    started_at             INTEGER,
    finished_at            INTEGER,
    expires_at             INTEGER NOT NULL,
    lease_until            INTEGER,
    worker_id              TEXT    NOT NULL DEFAULT '',
    inputs_deleted_at      INTEGER,                 -- document, pages, instructions, schema gone
    content_deleted_at     INTEGER                  -- result, feedback and error gone too
) STRICT;

CREATE UNIQUE INDEX jobs_idempotency ON jobs (application_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX jobs_queue ON jobs (status, created_at);
CREATE INDEX jobs_application_status ON jobs (application_id, status);
CREATE INDEX jobs_expires ON jobs (expires_at);
