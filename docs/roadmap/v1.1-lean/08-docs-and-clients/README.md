# Epic 08: Documentation and example clients

Version: v1.1 · Status: in progress · Depends on: 00; 08-01 after the code epics, whose jobs edit docs too

One home for each fact, no statement the code contradicts, and no example client that
nothing runs. This epic covers the "web" and "mobile" parts of the repository: the Node
and Swift example clients. Docs tests constrain edits: `internal/config/docs_test.go`,
`internal/config/schema_test.go`, `internal/ground/docs_test.go`,
`internal/provider/docs_test.go`, `test/conformance/checklist_test.go` and
`musts_test.go`, `internal/config/examples_test.go`; run `make check` after each docs
change.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Deduplicate and de-stale the documentation](01-dedupe-and-destale.md) | planned |
| 02 | [Configuration reference from one source](02-config-reference-single-source.md) | done |
| 03 | [Example clients: Node (web) and Swift (mobile)](03-example-clients.md) | done |
| 04 | [History of the done versions](04-roadmap-history.md) | planned |
