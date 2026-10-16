-- Quota counters per application and window; the append-only audit trail (never content).
CREATE TABLE quota_usage (
    application_id TEXT    NOT NULL,
    window_start   INTEGER NOT NULL,
    count          INTEGER NOT NULL,
    PRIMARY KEY (application_id, window_start)
) STRICT;

CREATE TABLE audit_events (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    at             INTEGER NOT NULL,
    kind           TEXT    NOT NULL,
    application_id TEXT    NOT NULL DEFAULT '',
    actor          TEXT    NOT NULL DEFAULT '',
    job_id         TEXT    NOT NULL DEFAULT '',
    details        TEXT    NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX audit_events_application_at ON audit_events (application_id, at);
