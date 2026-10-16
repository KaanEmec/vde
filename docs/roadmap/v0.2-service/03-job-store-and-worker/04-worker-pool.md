# Job: Worker pool and job deadline

Version: v0.2 · Epic: 03 Job store and worker · Status: done · Depends on: 03, v0.1 05-05

## Goal

A fixed number of goroutines that lease jobs, load the blobs and compiled schema, run
`extract.Run` with the right deadline and progress callback, write the outcome and apply
retention actions, all inside the `serve` process.

## Context

ADR 0003. Contract §6 time limit (default 5 min, max 8), §10 (delete document and pages
at terminal state for `retention: none`).

## Tasks

- [x] `internal/worker.Pool{Size, PollInterval}`: each worker loops: lease (or sleep
      `PollInterval`, default 500 ms, with a wake-up channel the submit handler pings so
      latency is not the poll interval), build `extract.Input` (representation from the
      job row and blobs, instructions and schema from the row, deadline `created_at +
      job_timeout`), run, `Finish`.
- [x] Per-application `max_in_progress` enforced at lease time (count running per app)
      so one application cannot occupy every worker.
- [x] After `Finish`: if retention is `none` or the application forces it, `DeleteAll`
      blobs and `MarkContentDeleted(document)`; instructions and schema columns cleared
      too (they are caller content).
- [x] Graceful stop: stop leasing, wait for running jobs up to the shutdown grace, then
      return; jobs still running keep their lease and are resumed after restart.
- [x] Metrics hooks (epic 07): job duration, attempts, terminal status counts.
- [x] Integration test: submit through the store, run the pool with the fake provider,
      assert the terminal row and that blobs are gone for `retention: none`.

## Acceptance criteria

- Worker count and poll interval configurable; a job submitted is picked up within
  100 ms in tests thanks to the wake-up channel.

## Out of scope

Multiple processes (v0.3 horizontal scaling), per-job priority.

## Outcome

Done in `internal/worker` (`New(store, blobs, cfg, roles, Options)`, `Start`, `Wake`,
`Stop`, `Observer`, `NopObserver`). Deviations:

- Options: `Size` (4), `PollInterval` (500 ms), `Lease` (1 min), `WorkerID`, `Observer`,
  `Logger`; the `serve` command maps configuration onto them (no config keys added here).
- The representation is selected again from the blobs with the current configuration;
  when its kind differs from the one decided at submit the job fails `internal`.
- Retention at the terminal state: blobs `DeleteAll`, then
  `MarkContentDeleted(ContentInputs)` for `retention: none` or a configuration that forces
  none; `Finish` and retention use a context detached from the shutdown grace (10 s cap).
- Metrics are an `Observer` interface (`JobStarted`, `JobFinished(app, status, attempts,
  duration since created_at)`); Prometheus comes with epic 07.
