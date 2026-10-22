-- Audit events by job id (v1.3): vde audit list --job and GET /admin/v1/audit?job_id=
-- find the error code of a job whose row retention: none has deleted.
CREATE INDEX audit_events_job ON audit_events (job_id, at);
