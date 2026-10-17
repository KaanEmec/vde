-- PostgreSQL twin of migrations/0008: the job's effective judge policy, its
-- verification (NULL until terminal and on older rows, derived by the store) and the
-- terminal status extracted.
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check
    CHECK (status IN ('queued', 'running', 'accepted', 'rejected', 'failed', 'extracted'));
ALTER TABLE jobs ADD COLUMN judge_policy TEXT NOT NULL DEFAULT 'always'
    CHECK (judge_policy IN ('always', 'never', 'auto'));
ALTER TABLE jobs ADD COLUMN verification TEXT
    CHECK (verification IN ('judge', 'grounded', 'none'));
