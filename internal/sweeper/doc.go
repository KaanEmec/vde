// Package sweeper is the background loop of the serve process that enforces retention:
// it removes expired jobs with their blobs, finishes running jobs whose worker was lost,
// and drops old quota, token and audit rows and orphan blobs (contract §10, ADR 0003).
// Nothing it logs or audits carries content.
package sweeper
