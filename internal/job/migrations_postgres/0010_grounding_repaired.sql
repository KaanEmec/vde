-- PostgreSQL twin of migrations/0010: whether the job made its grounding repair.
ALTER TABLE jobs ADD COLUMN grounding_repaired BOOLEAN NOT NULL DEFAULT FALSE;
