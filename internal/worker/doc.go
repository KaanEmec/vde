// Package worker runs the in-process worker pool: it leases jobs, runs the extraction
// loop under the job deadline and applies the retention action at the terminal state.
//
// Its Sweeper is the background loop of the worker role that enforces retention: it
// removes expired jobs with their blobs, finishes running jobs whose worker was lost,
// and drops old quota, token and audit rows and orphan blobs (contract §10, ADR 0003).
// Nothing it logs or audits carries content.
package worker
