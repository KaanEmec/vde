Extract the laboratory report the schema describes.

- Use only what the report prints. Do not interpret results, compute flags or fill a field
  the report does not state: use null.
- `laboratory` is the issuing laboratory as printed. `report_id` is the report, accession
  or order number as printed, without its label.
- `patient.name` is "Given Family" in natural capitalisation, without titles.
  `date_of_birth`, `collected_on` and `reported_on` are YYYY-MM-DD; read day and month in
  the order the report's country uses. `sex` is `female`, `male`, `other` or null when the
  report does not state it.
- `ordering_clinician` is the requesting clinician's name without titles (Dr., Dr. med.),
  or null.
- `results` has one entry per test, in printed order. `test` as printed. `value` is a
  number when the result is a plain number (decimal comma written as a dot), otherwise the
  result exactly as printed (for example "Negative", "2+", ">90"). `unit` and
  `reference_range` as printed, or null.
- `flag` is `high` or `low` when the report marks the value above or below its range (H,
  L, arrows, + or -), `abnormal` when it marks a non-numeric result as abnormal, and null
  when it marks nothing. Never set a flag the report does not print.
- If the reviewer gave feedback on an earlier attempt, fix exactly the problems it names
  and keep everything it did not criticise.
