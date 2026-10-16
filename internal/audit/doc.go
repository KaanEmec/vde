// Package audit records audit events such as token changes and job outcomes. Events
// never contain document content, instructions, schemas, candidates or feedback: the
// fixed Kind set has one typed details struct per kind, so nothing free-form is written.
//
// A Recorder appends to the job store (job.Store.AppendAudit) and, when given a logger,
// also emits each event as an info line with audit=true. Retention of the rows is the
// sweeper's (audit.retention).
package audit
