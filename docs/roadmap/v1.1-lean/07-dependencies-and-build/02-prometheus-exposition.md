# Job: Prometheus text exposition without the client library

Version: v1.1 · Epic: 07 Dependencies and build · Status: dropped (owner decision L4, 2026-10-19: keep the client library) · Depends on: 02-01; owner decision L4, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17)

## Goal

`internal/metrics` writes the Prometheus text format itself, removing
`prometheus/client_golang` and the eight modules it pulls in (`google.golang.org/protobuf`
among them). Every series name, label and value served on `/metrics` stays.

## Context

- 13 metric families (counters, gauges, histograms with labels), one const collector,
  Go and process collectors behind `metrics.go_runtime` (a frozen configuration key).
- Cost today: about 0.9 MB of symbols (protobuf 0.67 MB), 8 modules. A hand-written
  exposition (`# HELP`, `# TYPE`, label escaping, histogram buckets) is about 250 lines;
  `go_*` series come from `runtime/metrics`, `process_*` from `/proc` on Linux. Their
  names may differ from client_golang's: metrics are an operator surface, so a changed
  `go_*` name is a CHANGELOG entry, but the `vde_*` series must not change.
- Medium risk; adds about as many lines as it removes, so it is a dependency win, not a
  line win. The owner decides whether that trade is wanted (default: no, unless binary
  size or supply chain matters for the deployment).

## Tasks

- [x] Owner decision L4 (2026-10-19): keep the client library.
- [ ] Freeze the current output as a golden file.
  - Skill: none (test).
  - Prompt: "Add a test that serves /metrics with fixed values for every vde_* family
    and compares the text with a golden file (series, labels, HELP and TYPE lines);
    commit it before any change. Guardrails: skills.md."
- [ ] Replace the library.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: implement counter, gauge and histogram
    vectors and the text exposition in internal/metrics with the standard library only;
    keep metrics.go_runtime working from runtime/metrics and /proc; the golden test must
    pass unchanged for vde_* series. Remove client_golang with `go mod tidy` and its
    overview row. Guardrails: skills.md." Then "/golang-refactoring Execute mode: step
    <n>."
- [ ] Gate.
  - Skill: REVIEW, GODEPS.
  - Prompt: `/code-review high <first>^..<last>`; record the binary size difference.

## Acceptance criteria

- The golden test passes; `go.mod` has no `prometheus` module; a Prometheus server
  scrapes the endpoint without errors (`promtool check metrics` on the output).

## Out of scope

- New metrics.
