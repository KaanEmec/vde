-- An application's judge settings (v0.4, ADR 0009): policy ('' means roles.judge.policy),
-- whether a request may choose it, and the sample rate of auto (NULL means the default).
ALTER TABLE applications ADD COLUMN judge_policy TEXT NOT NULL DEFAULT ''
    CHECK (judge_policy IN ('', 'always', 'never', 'auto'));
ALTER TABLE applications ADD COLUMN judge_allow_override INTEGER NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN judge_sample_rate REAL;
