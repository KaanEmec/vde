-- PostgreSQL twin of migrations/0007: fixed-window rate counters shared by replicas.
CREATE TABLE rate_windows (
    application_id TEXT    NOT NULL,
    kind           TEXT    NOT NULL CHECK (kind IN ('requests', 'submits')),
    window_start   BIGINT  NOT NULL,
    count          INTEGER NOT NULL,
    PRIMARY KEY (application_id, kind, window_start)
);

CREATE INDEX rate_windows_start ON rate_windows (window_start);
