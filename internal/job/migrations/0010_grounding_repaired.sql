-- Grounding repair (v0.4, ADR 0009): whether the job made its one grounding repair, so a
-- resumed run does not repair again once a judge's feedback replaced the repair's.
ALTER TABLE jobs ADD COLUMN grounding_repaired INTEGER NOT NULL DEFAULT 0;
