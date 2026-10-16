// Package worker runs the in-process worker pool: it leases jobs, runs the extraction
// loop under the job deadline and applies the retention action at the terminal state.
package worker
