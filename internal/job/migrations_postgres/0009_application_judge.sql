-- PostgreSQL twin of migrations/0009: an application's judge settings.
ALTER TABLE applications ADD COLUMN judge_policy TEXT NOT NULL DEFAULT ''
    CHECK (judge_policy IN ('', 'always', 'never', 'auto'));
ALTER TABLE applications ADD COLUMN judge_allow_override BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE applications ADD COLUMN judge_sample_rate DOUBLE PRECISION;
