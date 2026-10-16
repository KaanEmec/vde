-- The attachment list of an email document (name, media type, bytes: metadata only), as
-- a JSON array; NULL for other documents. Cleared with the rest of the content.
ALTER TABLE jobs ADD COLUMN attachments TEXT;
