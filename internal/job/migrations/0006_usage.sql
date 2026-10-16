-- Provider token usage per job (TravelInbox live test, defect D1): the input and output
-- tokens of every provider call the job's runs made, accumulated across re-leases.
ALTER TABLE jobs ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
