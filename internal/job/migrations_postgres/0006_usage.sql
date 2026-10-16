-- PostgreSQL twin of migrations/0006: provider token usage per job.
ALTER TABLE jobs ADD COLUMN input_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN output_tokens BIGINT NOT NULL DEFAULT 0;
