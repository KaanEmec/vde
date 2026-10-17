-- Judge policy (v0.4, ADR 0009): the job's effective policy, its verification (judge,
-- grounded or none; NULL until terminal, and on rows written before this version, which
-- the store derives from the status) and the terminal status extracted. SQLite cannot
-- change a CHECK in place, so the table is rebuilt with every column of 0002, 0004 and
-- 0006.
CREATE TABLE jobs_new (
    id                     TEXT    PRIMARY KEY,     -- j_<ULID>
    application_id         TEXT    NOT NULL REFERENCES applications (id),
    status                 TEXT    NOT NULL CHECK (status IN ('queued', 'running', 'accepted', 'rejected', 'failed', 'extracted')),
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
    content_deleted_at     INTEGER,                 -- result, feedback and error gone too
    attachments            TEXT,
    input_tokens           INTEGER NOT NULL DEFAULT 0,
    output_tokens          INTEGER NOT NULL DEFAULT 0,
    judge_policy           TEXT    NOT NULL DEFAULT 'always' CHECK (judge_policy IN ('always', 'never', 'auto')),
    verification           TEXT    CHECK (verification IN ('judge', 'grounded', 'none'))
) STRICT;

INSERT INTO jobs_new (id, application_id, status, attempts, result, feedback, error, judge_confidence,
    service_version, representation, media_type, size, page_count, retention, idempotency_key, body_hash,
    extractor_instructions, judge_instructions, schema, max_attempts, created_at, started_at, finished_at,
    expires_at, lease_until, worker_id, inputs_deleted_at, content_deleted_at, attachments, input_tokens,
    output_tokens)
SELECT id, application_id, status, attempts, result, feedback, error, judge_confidence,
    service_version, representation, media_type, size, page_count, retention, idempotency_key, body_hash,
    extractor_instructions, judge_instructions, schema, max_attempts, created_at, started_at, finished_at,
    expires_at, lease_until, worker_id, inputs_deleted_at, content_deleted_at, attachments, input_tokens,
    output_tokens
FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;

CREATE UNIQUE INDEX jobs_idempotency ON jobs (application_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX jobs_queue ON jobs (status, created_at);
CREATE INDEX jobs_application_status ON jobs (application_id, status);
CREATE INDEX jobs_expires ON jobs (expires_at);
