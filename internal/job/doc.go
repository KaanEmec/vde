// Package job defines the persistence rows (jobs, applications, tokens, audit events),
// the job state machine, the Store interface and its SQLite and PostgreSQL implementations with
// embedded migrations and leases (ADR 0002, ADR 0003). Transitions are enforced in the
// store's statements: a terminal status never changes and attempts never decrease.
package job
