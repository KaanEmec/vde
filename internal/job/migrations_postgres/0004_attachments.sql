-- PostgreSQL twin of migrations/0004: the attachment list of an email document as a
-- JSON array (metadata only); NULL for other documents.
ALTER TABLE jobs ADD COLUMN attachments JSONB;
