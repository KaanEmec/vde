package job

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// Status is a job's state (contract §5): queued → running → accepted | rejected | failed,
// or extracted under judge policy never (ADR 0009).
type Status string

// Job statuses. Only StatusAccepted is acceptance; StatusExtracted is a schema-valid
// result no judge looked at, outside contract v1.
const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusAccepted  Status = "accepted"
	StatusRejected  Status = "rejected"
	StatusFailed    Status = "failed"
	StatusExtracted Status = "extracted"
)

// Statuses lists every status, in state-machine order.
var Statuses = []Status{StatusQueued, StatusRunning, StatusAccepted, StatusRejected, StatusFailed, StatusExtracted}

// Terminal reports whether s is one of the final states, which never change.
func (s Status) Terminal() bool {
	return s == StatusAccepted || s == StatusRejected || s == StatusFailed || s == StatusExtracted
}

// Verifications of a terminal job (ADR 0009): what made its outcome. "" while queued or
// running.
const (
	VerificationJudge    = "judge"
	VerificationGrounded = "grounded"
	VerificationNone     = "none"
)

// DefaultVerification is the verification of a terminal job that recorded none (a row
// written before v0.4, or a terminal state written without running the loop): judge
// when accepted, none otherwise; "" when s is not terminal.
func DefaultVerification(s Status) string {
	switch {
	case s == StatusAccepted:
		return VerificationJudge
	case s.Terminal():
		return VerificationNone
	}
	return ""
}

// DefaultJudgePolicy is the judge policy stored for a job created without one
// (config.JudgeAlways; this package does not import config).
const DefaultJudgePolicy = "always"

// Retention is a job's effective retention (contract §10).
type Retention string

// Retention policies.
const (
	RetentionNone    Retention = "none"
	RetentionDefault Retention = "default"
)

// noneExpiry is how long a retention: none job may exist at most (contract §10).
const noneExpiry = 24 * time.Hour

// IdempotencyWindow is how long an Idempotency-Key names its job (contract §4). A job's
// row, metadata only once its content is gone, is kept at least this long after
// created_at, even when its expires_at is earlier.
const IdempotencyWindow = 24 * time.Hour

// Errors returned by Store methods. Messages never carry content.
var (
	ErrNotFound       = errors.New("job: not found")
	ErrDuplicate      = errors.New("job: duplicate")       // unique key taken (idempotency key, token id)
	ErrConflict       = errors.New("job: conflict")        // a transition the state machine refuses, or a lost lease
	ErrQuotaExhausted = errors.New("job: quota exhausted") // the window's count reached the limit
	ErrRateLimited    = errors.New("job: rate limited")    // the minute's shared rate counter reached the limit
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
	Attachments    []Attachment // an email document's attachments, metadata only; nil otherwise

	// Caller content, cleared with the blobs (InputsDeletedAt).
	ExtractorInstructions string
	JudgeInstructions     string
	Schema                string // "" in schema-less mode
	MaxAttempts           int    // the job's attempt limit; 0 means the application's

	// JudgePolicy is the effective judge policy the job runs under: always, never or
	// auto ("" is stored as always). Verification is judge, grounded or none once
	// terminal, "" before.
	JudgePolicy  string
	Verification string
	// GroundingRepaired is set once the job made its one grounding repair (ADR 0009), so
	// a resumed run does not repair again; RecordAttempt stores it and never clears it.
	GroundingRepaired bool

	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	ExpiresAt  time.Time
	LeaseUntil time.Time
	WorkerID   string

	// InputTokens and OutputTokens are the provider token usage of every run of the job,
	// as recorded by RecordAttempt and Finish (metadata, kept with the row).
	InputTokens  int64
	OutputTokens int64

	InputsDeletedAt  time.Time // document, pages, instructions and schema deleted
	ContentDeletedAt time.Time // result, feedback and error deleted too
}

// Attachment is one attachment of an email document: metadata only, never content.
type Attachment struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
}

// Progress is the persisted state after one extractor attempt.
type Progress struct {
	Attempts int
	Result   json.RawMessage
	Feedback json.RawMessage
	// GroundingRepaired: the job made its grounding repair (Job.GroundingRepaired).
	GroundingRepaired bool
	// InputTokens and OutputTokens are the job's usage so far, earlier runs included;
	// the stored counts never decrease.
	InputTokens  int64
	OutputTokens int64
}

// Outcome is a terminal state written by Finish.
type Outcome struct {
	Status          Status // must be terminal
	Attempts        int
	Result          json.RawMessage
	Feedback        json.RawMessage
	Error           json.RawMessage
	JudgeConfidence *float64
	// Verification is judge, grounded or none; "" means DefaultVerification(Status).
	Verification string
	// InputTokens and OutputTokens are the job's total usage, as in Progress.
	InputTokens  int64
	OutputTokens int64
}

// Rate limiter kinds of IncrementRate (rate.shared: true).
const (
	RateRequests = "requests" // every /v1 request
	RateSubmits  = "submits"  // job submissions
)

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

// Application sources: where an application row comes from.
const (
	// SourceConfig: declared in the YAML; vde serve rewrites every field at start and
	// disables the row when the entry is removed.
	SourceConfig = "config"
	// SourceAdmin: created over the admin API; kept as stored across restarts.
	SourceAdmin = "admin"
)

// Application is one row of the applications table, mirrored from the configuration or
// created over the admin API (Source).
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
	// JudgePolicy is applications[].judge.policy: "" means roles.judge.policy.
	// JudgeAllowOverride lets a request choose the policy; JudgeSampleRate nil means
	// config.DefaultSampleRate.
	JudgePolicy        string
	JudgeAllowOverride bool
	JudgeSampleRate    *float64
	Enabled            bool
	Source             string // SourceConfig or SourceAdmin; "" reads as SourceConfig
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Token is one row of the tokens table. Only the hash of the secret is stored.
type Token struct {
	ID            string // the clear token id, also the display prefix
	ApplicationID string // "" for an admin token
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

// JobFilter selects jobs for ListJobs; zero fields match everything.
type JobFilter struct {
	ApplicationID string
	Status        Status
	Since         time.Time // created at or after Since
}

// Store is the persistence the service needs, and nothing more (ADR 0002). Times passed
// in are the caller's clock so tests can control it.
type Store interface {
	// UpsertApplication inserts or updates an application from the configuration,
	// enables it and marks it SourceConfig.
	UpsertApplication(ctx context.Context, a Application, now time.Time) error
	// DisableApplicationsNotIn disables every enabled SourceConfig application whose id
	// is not in ids and returns the ids it disabled. SourceAdmin rows are left alone.
	DisableApplicationsNotIn(ctx context.Context, ids []string, now time.Time) ([]string, error)
	// CreateApplication inserts a as SourceAdmin with a.Enabled; ErrDuplicate when the
	// id is taken.
	CreateApplication(ctx context.Context, a Application, now time.Time) error
	// UpdateApplication rewrites the settings, name and enabled flag of an existing
	// application, keeping its source and created_at; ErrNotFound when it does not exist.
	UpdateApplication(ctx context.Context, a Application, now time.Time) error
	GetApplication(ctx context.Context, id string) (Application, error)
	ListApplications(ctx context.Context) ([]Application, error)

	// CreateToken stores t; ErrDuplicate when its id is taken.
	CreateToken(ctx context.Context, t Token) error
	GetTokenByID(ctx context.Context, id string) (Token, error)
	// ListTokens returns the application's tokens; applicationID "" lists the admin tokens.
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

	// IncrementRate counts one request of kind (RateRequests or RateSubmits) in the
	// application's fixed window starting at windowStart and returns the new count, or
	// ErrRateLimited when the count already reached limit. One UPSERT, shared by every
	// process on the database (rate.shared: true).
	IncrementRate(ctx context.Context, applicationID, kind string, windowStart time.Time, limit int) (int, error)
	// DeleteRateBefore removes the rate windows of every application that started before
	// before and returns how many.
	DeleteRateBefore(ctx context.Context, before time.Time) (int64, error)

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
	// MarkContentDeleted clears the columns of what and records when. After clearing
	// it checkpoints the WAL (best effort), so the cleared content does not survive in it.
	MarkContentDeleted(ctx context.Context, id string, what Content, now time.Time) error
	// DeleteJob removes the row and checkpoints the WAL as MarkContentDeleted does.
	DeleteJob(ctx context.Context, id string) error
	// FindByIdempotencyKey returns the application's job submitted with key.
	FindByIdempotencyKey(ctx context.Context, applicationID, key string) (Job, error)
	// ListContentExpired returns up to limit terminal jobs whose expires_at is not after
	// now and whose content is not deleted yet, oldest expires_at first: their content is
	// due for deletion.
	ListContentExpired(ctx context.Context, now time.Time, limit int) ([]Job, error)
	// ListExpired returns up to limit jobs whose row is due for deletion: expires_at and
	// created_at + IdempotencyWindow both not after now, oldest expires_at first. The row
	// outlives a shorter expires_at so that the idempotency key keeps naming the job.
	ListExpired(ctx context.Context, now time.Time, limit int) ([]Job, error)
	// ListStaleRunning returns up to limit running jobs created at or before createdBefore
	// (their time limit and grace are over: a lost worker), oldest first.
	ListStaleRunning(ctx context.Context, createdBefore time.Time, limit int) ([]Job, error)
	// CountRunning returns the number of the application's running jobs.
	CountRunning(ctx context.Context, applicationID string) (int, error)
	// CountInProgress returns the number of the application's queued and running jobs.
	CountInProgress(ctx context.Context, applicationID string) (int, error)
	// OldestQueuedAt returns the created_at of the oldest queued job of any application,
	// the zero time when none is queued (the queue age of /healthz on an API-only process).
	OldestQueuedAt(ctx context.Context) (time.Time, error)
	// ListJobs returns up to limit jobs matching f, newest first; limit <= 0 means no
	// limit.
	ListJobs(ctx context.Context, f JobFilter, limit int) ([]Job, error)
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
