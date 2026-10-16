// Package blob defines the Store interface for a job's document and page images and its
// filesystem implementation (ADR 0002). Blobs are written atomically and deleted
// together; their content is never listed or logged.
package blob
