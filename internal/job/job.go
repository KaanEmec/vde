package job

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// Status is a job's state (contract §5): queued → running → accepted | rejected | failed.
type Status string

// Job statuses. Only StatusAccepted is acceptance.
const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusAccepted Status = "accepted"
	StatusRejected Status = "rejected"
	StatusFailed   Status = "failed"
)

// Terminal reports whether s is one of the final states, which never change.
func (s Status) Terminal() bool {
	return s == StatusAccepted || s == StatusRejected || s == StatusFailed
}

// Retention is a job's effective retention (contract §10).
type Retention string

// Retention policies.
const (
	RetentionNone    Retention = "none"
	RetentionDefault Retention = "default"
)

// noneExpiry is how long a retention: none job may exist at most (contract §10).
const noneExpiry = 24 * time.Hour

// Errors returned by Store methods. Messages never carry content.
var (
	ErrNotFound       = errors.New("job: not found")
	ErrDuplicate      = errors.New("job: duplicate")       // unique key taken (idempotency key, token id)
	ErrConflict       = errors.New("job: conflict")        // a transition the state machine refuses, or a lost lease
	ErrQuotaExhausted = errors.New("job: quota exhausted") // the window's count reached the limit
)

// NewID returns a new job id, j_<ULID>.
func NewID() string { return "j_" + ulid.Make().String() }

// Job is one row of the jobs table. A zero time.Time is a NULL column. Result, Feedback
// and Error are JSON text, nil when NULL.
type Job struct {
	ID              string
	ApplicationID   string
	Status          Status
	Attempts        int
	Result          json.RawMessage // the last parseable candidate
	Feedback        json.RawMessage // array of feedback entries
	Error           json.RawMessage // {"code","message"} when failed
	JudgeConfidence *float64
	ServiceVersion  string

	Representation string // document.Kind name: native, pages or text
	MediaType      string // the document's normalized media type
	Size           int64  // document bytes
	PageCount      int    // page blobs page-01 … page-NN
	Retention      Retention
	IdempotencyKey string // "" when none
	BodyHash       string

	// Caller content, cleared with the blobs (InputsDeletedAt).
	ExtractorInstructions string
	JudgeInstructions     string
	Schema                string // "" in schema-less mode
	MaxAttempts           int    // the job's attempt limit; 0 means the application's

	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	ExpiresAt  time.Time
	LeaseUntil time.Time
	WorkerID   string

	InputsDeletedAt  time.Time // document, pages, instructions and schema deleted
	ContentDeletedAt time.Time // result, feedback and error deleted too
}

// Progress is the persisted state after one extractor attempt.
type Progress struct {
	Attempts int
	Result   json.RawMessage
	Feedback json.RawMessage
}

// Outcome is a terminal state written by Finish.
type Outcome struct {
	Status          Status // must be terminal
	Attempts        int
	Result          json.RawMessage
	Feedback        json.RawMessage
	Error           json.RawMessage
	JudgeConfidence *float64
}

// Content selects what MarkContentDeleted clears.
type Content int

// Content deletion scopes.
const (
	// ContentInputs: the document and pages (blobs, deleted by the caller), the
	// instructions and the schema. The job stays readable. Done at the terminal state
	// for retention: none.
	ContentInputs Content = iota + 1
	// ContentAll: the inputs plus result, feedback and error; the job reads as gone.
	// Done at the first terminal read for retention: none. Only one caller wins: a
	// second call returns ErrNotFound.
	ContentAll
)

// Application is one row of the applications table, mirrored from the configuration.
type Application struct {
	ID                string
	Name              string
	MaxAttempts       int
	QuotaLimit        int
	QuotaWindow       string // ISO 8601 duration such as P1D
	RequestsPerMinute int
	SubmitsPerMinute  int
	MaxInProgress     int
	RetentionDefault  string // a duration such as 24h, or none
	ForceNone         bool
	HTMLPolicy        string // "" means documents.html
	Enabled           bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Token is one row of the tokens table. Only the hash of the secret is stored.
type Token struct {
	ID            string // the clear token id, also the display prefix
	ApplicationID string
	Hash          []byte // SHA-256 of the secret part
	Scopes        []string
	Label         string
	ExpiresAt     time.Time
	RevokedAt     time.Time
	CreatedAt     time.Time
	LastUsedAt    time.Time
}

// AuditEvent is one append-only audit record. Details is JSON built from fixed structs,
// never content.
type AuditEvent struct {
	ID            int64 // assigned by AppendAudit
	At            time.Time
	Kind          string
	ApplicationID string
	Actor         string
	JobID         string
	Details       json.RawMessage // nil means {}
}

// AuditFilter selects audit events for ListAudit; zero fields match everything.
type AuditFilter struct {
	ApplicationID string
	Kind          string
	JobID         string
	Since         time.Time // events at or after Since
}

// Store is the persistence the service needs, and nothing more (ADR 0002). Times passed
// in are the caller's clock so tests can control it.
type Store interface {
	// UpsertApplication inserts or updates an application from the configuration and
	// enables it.
	UpsertApplication(ctx context.Context, a Application, now time.Time) error
	// DisableApplicationsNotIn disables every enabled application whose id is not in
	// ids and returns the ids it disabled.
	DisableApplicationsNotIn(ctx context.Context, ids []string, now time.Time) ([]string, error)
	GetApplication(ctx context.Context, id string) (Application, error)
	ListApplications(ctx context.Context) ([]Application, error)

	// CreateToken stores t; ErrDuplicate when its id is taken.
	CreateToken(ctx context.Context, t Token) error
	GetTokenByID(ctx context.Context, id string) (Token, error)
	ListTokens(ctx context.Context, applicationID string) ([]Token, error)
	// RevokeToken sets revoked_at once; revoking a revoked token is not an error.
	RevokeToken(ctx context.Context, id string, now time.Time) error
	// TouchTokenLastUsed sets last_used_at to now when it is unset or older than a
	// minute, to keep polls from writing on every request.
	TouchTokenLastUsed(ctx context.Context, id string, now time.Time) error
	// DeleteTokensBefore removes tokens revoked or expired before before and returns
	// how many.
	DeleteTokensBefore(ctx context.Context, before time.Time) (int64, error)

	// IncrementQuota charges one submission to the window and returns the new count, or
	// ErrQuotaExhausted when the count already reached limit.
	IncrementQuota(ctx context.Context, applicationID string, windowStart time.Time, limit int) (int, error)
	// GetQuota returns the window's count, 0 when nothing was charged.
	GetQuota(ctx context.Context, applicationID string, windowStart time.Time) (int, error)
	// DeleteQuotaBefore removes the application's quota windows that started before
	// before and returns how many rows it removed.
	DeleteQuotaBefore(ctx context.Context, applicationID string, before time.Time) (int64, error)

	// AppendAudit appends e and returns its id.
	AppendAudit(ctx context.Context, e AuditEvent) (int64, error)
	// ListAudit returns up to limit events matching f, oldest first; limit <= 0 means no limit.
	ListAudit(ctx context.Context, f AuditFilter, limit int) ([]AuditEvent, error)
	// DeleteAuditBefore removes audit events older than before and returns how many.
	DeleteAuditBefore(ctx context.Context, before time.Time) (int64, error)

	// CreateJob inserts j as queued. It assigns ID (when empty), Status, Attempts and
	// CreatedAt (when zero), resolves Retention against the application (force_none, or
	// a retention default of none, make it none) and sets ExpiresAt: created_at + 24h
	// for none, + the application's retention otherwise. ErrNotFound when the
	// application is unknown or disabled; ErrDuplicate when the idempotency key is
	// taken.
	CreateJob(ctx context.Context, j *Job) error
	// GetJob returns the application's job; another application's job is ErrNotFound.
	GetJob(ctx context.Context, applicationID, id string) (Job, error)
	// GetJobAny returns a job regardless of its application (worker, sweeper).
	GetJobAny(ctx context.Context, id string) (Job, error)
	// LeaseNextQueued moves the oldest queued job, or a running job whose lease expired
	// before now, to running with worker_id and lease_until, in one statement. An
	// application listed in maxInProgress with a positive value is skipped while that
	// many of its jobs hold a live lease. ErrNotFound when no job is eligible.
	LeaseNextQueued(ctx context.Context, workerID string, now, leaseUntil time.Time, maxInProgress map[string]int) (Job, error)
	// RenewLease extends the lease of a running job held by workerID; ErrConflict when
	// the lease is no longer this worker's.
	RenewLease(ctx context.Context, id, workerID string, leaseUntil time.Time) error
	// RecordAttempt persists progress after an extractor attempt and renews the lease.
	// ErrConflict when the job is not running under workerID or attempts would
	// decrease.
	RecordAttempt(ctx context.Context, id, workerID string, p Progress, leaseUntil time.Time) error
	// Finish writes the terminal state of a running job. workerID "" finishes it under
	// any lease (the sweeper). ErrConflict when the job is not running (a terminal
	// status never changes) or is leased by another worker.
	Finish(ctx context.Context, id, workerID string, o Outcome, now time.Time) error
	// MarkContentDeleted clears the columns of what and records when.
	MarkContentDeleted(ctx context.Context, id string, what Content, now time.Time) error
	// DeleteJob removes the row.
	DeleteJob(ctx context.Context, id string) error
	// FindByIdempotencyKey returns the application's job submitted with key.
	FindByIdempotencyKey(ctx context.Context, applicationID, key string) (Job, error)
	// ListExpired returns up to limit jobs whose expires_at is not after now, oldest
	// first.
	ListExpired(ctx context.Context, now time.Time, limit int) ([]Job, error)
	// ListStaleRunning returns up to limit running jobs created at or before createdBefore
	// (their time limit and grace are over: a lost worker), oldest first.
	ListStaleRunning(ctx context.Context, createdBefore time.Time, limit int) ([]Job, error)
	// CountRunning returns the number of the application's running jobs.
	CountRunning(ctx context.Context, applicationID string) (int, error)
}

// expiry resolves a job's effective retention and its expires_at from the request and
// the application.
func expiry(created time.Time, requested Retention, app Application) (Retention, time.Time, error) {
	if requested == RetentionNone || app.ForceNone || app.RetentionDefault == string(RetentionNone) {
		return RetentionNone, created.Add(noneExpiry), nil
	}
	d, err := time.ParseDuration(app.RetentionDefault)
	if err != nil || d <= 0 {
		return "", time.Time{}, errors.New("job: application retention is neither none nor a positive duration")
	}
	return RetentionDefault, created.Add(d), nil
}
