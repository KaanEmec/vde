-- PostgreSQL twin of migrations/0003.
CREATE TABLE quota_usage (
    application_id TEXT    NOT NULL,
    window_start   BIGINT  NOT NULL,
    count          INTEGER NOT NULL,
    PRIMARY KEY (application_id, window_start)
);

CREATE TABLE audit_events (
    id             BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at             BIGINT  NOT NULL,
    kind           TEXT    NOT NULL,
    application_id TEXT    NOT NULL DEFAULT '',
    actor          TEXT    NOT NULL DEFAULT '',
    job_id         TEXT    NOT NULL DEFAULT '',
    details        JSON    NOT NULL DEFAULT '{}'
);

CREATE INDEX audit_events_application_at ON audit_events (application_id, at);
