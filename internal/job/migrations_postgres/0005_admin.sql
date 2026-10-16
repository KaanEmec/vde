-- PostgreSQL twin of migrations/0005: applications record their source; admin tokens
-- have no application and the single scope admin. PostgreSQL drops NOT NULL in place.
ALTER TABLE applications ADD COLUMN source TEXT NOT NULL DEFAULT 'config'
    CHECK (source IN ('config', 'admin'));

ALTER TABLE tokens ALTER COLUMN application_id DROP NOT NULL;
ALTER TABLE tokens ADD CONSTRAINT tokens_admin_scope
    CHECK ((application_id IS NULL) = (scopes = 'admin'));
