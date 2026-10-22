-- PostgreSQL twin of migrations/0011: audit events by job id.
CREATE INDEX audit_events_job ON audit_events (job_id, at);
