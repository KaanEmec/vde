-- Shared rate limits (v0.3 epic 07, rate.shared: true): one fixed-window counter per
-- application, limiter kind (requests or submits) and UTC minute, so that every replica
-- counts against the same limit. The sweeper removes windows older than two minutes.
CREATE TABLE rate_windows (
    application_id TEXT    NOT NULL,
    kind           TEXT    NOT NULL CHECK (kind IN ('requests', 'submits')),
    window_start   INTEGER NOT NULL,              -- Unix ms of the minute
    count          INTEGER NOT NULL,
    PRIMARY KEY (application_id, kind, window_start)
) STRICT;

CREATE INDEX rate_windows_start ON rate_windows (window_start);
