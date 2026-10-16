package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/kaanemec/vde/internal/job"
)

// Kind is the type of an audit event. The set is fixed.
type Kind string

// The audit event kinds.
const (
	TokenCreatedKind Kind = "token.created"
	TokenRevokedKind Kind = "token.revoked"
	AppEnabledKind   Kind = "app.enabled"
	AppDisabledKind  Kind = "app.disabled"
	JobCreatedKind   Kind = "job.created"
	JobFinishedKind  Kind = "job.finished"
	JobDeletedKind   Kind = "job.deleted"
	JobExpiredKind   Kind = "job.expired"
	AuthFailedKind   Kind = "auth.failed"
	ConfigLoadedKind Kind = "config.loaded"
)

// Kinds returns every kind, in documentation order.
func Kinds() []Kind {
	return []Kind{
		TokenCreatedKind, TokenRevokedKind, AppEnabledKind, AppDisabledKind, JobCreatedKind,
		JobFinishedKind, JobDeletedKind, JobExpiredKind, AuthFailedKind, ConfigLoadedKind,
	}
}

// Actors that write events, for the actor column.
const (
	ActorWorker  = "worker"
	ActorSweeper = "sweeper"
	ActorAPI     = "api"
	ActorCLI     = "cli"
	ActorConfig  = "config"
	// ActorAdmin prefixes the admin API's events: "admin:<token id>" names the admin
	// token that made the change (the id is the token's public prefix, never a secret).
	ActorAdmin = "admin"
)

// Details is the payload of an event: one of the structs below. Only they implement
// it, so an event carries exactly the fields defined here.
type Details interface {
	// AuditKind is the kind of event the details belong to.
	AuditKind() Kind
}

// TokenCreated: a token was issued. Never the token or its hash.
type TokenCreated struct {
	TokenID   string    `json:"token_id"`
	Scopes    []string  `json:"scopes,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// TokenRevoked: a token was revoked.
type TokenRevoked struct {
	TokenID string `json:"token_id"`
}

// AppEnabled: an application was enabled, by the configuration (created or listed
// again) or over the admin API (created, or PATCH enabled: true).
type AppEnabled struct{}

// AppDisabled: an application was disabled, removed from the configuration or by the
// admin API (PATCH enabled: false).
type AppDisabled struct{}

// JobCreated: a job was accepted (contract §12): who, when and what kind of document,
// never the document.
type JobCreated struct {
	CreatedAt      time.Time `json:"created_at"`
	MediaType      string    `json:"media_type"`
	Size           int64     `json:"bytes"`
	PageCount      int       `json:"pages"`
	Retention      string    `json:"retention"`
	ServiceVersion string    `json:"service_version"`
}

// JobFinished: a job reached a terminal state.
type JobFinished struct {
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	ErrorCode      string `json:"error_code,omitempty"`
	ServiceVersion string `json:"service_version"`
}

// Reasons a job was deleted, for JobDeleted.
const (
	DeletedByRequest = "request"    // DELETE /v1/jobs/{id}
	DeletedFirstRead = "first_read" // retention: none, first terminal GET
	DeletedByAdmin   = "admin"      // DELETE /admin/v1/jobs/{id}, any state
)

// JobDeleted: the caller's content was deleted before expiry.
type JobDeleted struct {
	Reason    string `json:"reason"` // DeletedByRequest, DeletedFirstRead or DeletedByAdmin
	Retention string `json:"retention"`
}

// JobExpired: the sweeper removed a job at its expires_at.
type JobExpired struct {
	Status    string `json:"status"`
	Retention string `json:"retention"`
}

// AuthFailed: a request was refused. Reason is a fixed code, never the token.
type AuthFailed struct {
	Reason string `json:"reason"`
}

// ConfigLoaded: the service started with a configuration.
type ConfigLoaded struct {
	ServiceVersion string `json:"service_version"`
}

// AuditKind implements Details.
func (TokenCreated) AuditKind() Kind { return TokenCreatedKind }

// AuditKind implements Details.
func (TokenRevoked) AuditKind() Kind { return TokenRevokedKind }

// AuditKind implements Details.
func (AppEnabled) AuditKind() Kind { return AppEnabledKind }

// AuditKind implements Details.
func (AppDisabled) AuditKind() Kind { return AppDisabledKind }

// AuditKind implements Details.
func (JobCreated) AuditKind() Kind { return JobCreatedKind }

// AuditKind implements Details.
func (JobFinished) AuditKind() Kind { return JobFinishedKind }

// AuditKind implements Details.
func (JobDeleted) AuditKind() Kind { return JobDeletedKind }

// AuditKind implements Details.
func (JobExpired) AuditKind() Kind { return JobExpiredKind }

// AuditKind implements Details.
func (AuthFailed) AuditKind() Kind { return AuthFailedKind }

// AuditKind implements Details.
func (ConfigLoaded) AuditKind() Kind { return ConfigLoadedKind }

// ErrUnknownKind: Decode met a kind outside the fixed set.
var ErrUnknownKind = errors.New("audit: unknown kind")

// Decode returns the typed details of a stored event. It is strict: a field the struct
// does not define is an error, which is how a test notices free-form data.
func Decode(e job.AuditEvent) (Details, error) {
	var d Details
	switch Kind(e.Kind) {
	case TokenCreatedKind:
		d = &TokenCreated{}
	case TokenRevokedKind:
		d = &TokenRevoked{}
	case AppEnabledKind:
		d = &AppEnabled{}
	case AppDisabledKind:
		d = &AppDisabled{}
	case JobCreatedKind:
		d = &JobCreated{}
	case JobFinishedKind:
		d = &JobFinished{}
	case JobDeletedKind:
		d = &JobDeleted{}
	case JobExpiredKind:
		d = &JobExpired{}
	case AuthFailedKind:
		d = &AuthFailed{}
	case ConfigLoadedKind:
		d = &ConfigLoaded{}
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, e.Kind)
	}
	raw := e.Details
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(d); err != nil {
		return nil, fmt.Errorf("audit: decode %s details: %w", e.Kind, err)
	}
	return deref(d), nil
}

// deref turns the decoded pointer back into the value type.
func deref(d Details) Details {
	switch v := d.(type) {
	case *TokenCreated:
		return *v
	case *TokenRevoked:
		return *v
	case *AppEnabled:
		return *v
	case *AppDisabled:
		return *v
	case *JobCreated:
		return *v
	case *JobFinished:
		return *v
	case *JobDeleted:
		return *v
	case *JobExpired:
		return *v
	case *AuthFailed:
		return *v
	default:
		return *d.(*ConfigLoaded)
	}
}

// Appender is the part of job.Store a Recorder needs.
type Appender interface {
	AppendAudit(ctx context.Context, e job.AuditEvent) (int64, error)
}

// Options configure a Recorder.
type Options struct {
	// Logger, when set, receives every event as an info line with audit=true
	// (audit.log: true). Nil writes the table only.
	Logger *slog.Logger
	// Clock gives the event time; time.Now when nil.
	Clock func() time.Time
}

// Recorder appends audit events to the store. A nil *Recorder discards events, so
// components that take an optional recorder need no checks.
type Recorder struct {
	store Appender
	opts  Options
	actor string
}

// New returns a recorder on store.
func New(store Appender, opts Options) *Recorder {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Recorder{store: store, opts: opts}
}

// WithActor returns a recorder that tags its job events with actor (ActorWorker,
// ActorSweeper, …). It shares the store and logger.
func (r *Recorder) WithActor(actor string) *Recorder {
	if r == nil {
		return nil
	}
	c := *r
	c.actor = actor
	return &c
}

// Record appends one event: the kind comes from d. actor overrides the recorder's
// default when not empty. An error from the store is returned; the log line is written
// only for events that were stored.
func (r *Recorder) Record(ctx context.Context, actor, applicationID, jobID string, d Details) error {
	if r == nil {
		return nil
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("audit: marshal %s details: %w", d.AuditKind(), err)
	}
	if actor == "" {
		actor = r.actor
	}
	e := job.AuditEvent{
		At: r.opts.Clock(), Kind: string(d.AuditKind()), ApplicationID: applicationID,
		Actor: actor, JobID: jobID, Details: raw,
	}
	if _, err := r.store.AppendAudit(ctx, e); err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Kind, err)
	}
	if l := r.opts.Logger; l != nil {
		attrs := []slog.Attr{
			slog.Bool("audit", true), slog.String("kind", e.Kind),
			slog.String("application_id", applicationID), slog.String("job_id", jobID),
			slog.String("actor", actor), slog.String("details", string(raw)),
		}
		l.LogAttrs(ctx, slog.LevelInfo, "audit", attrs...)
	}
	return nil
}

// warn records d and logs a failure instead of returning it: a job must not fail
// because its audit row could not be written.
func (r *Recorder) warn(ctx context.Context, applicationID, jobID string, d Details) {
	if r == nil {
		return
	}
	if err := r.Record(ctx, "", applicationID, jobID, d); err != nil {
		if l := r.opts.Logger; l != nil {
			l.WarnContext(ctx, "audit event not written", "kind", string(d.AuditKind()), "job_id", jobID, "error", err)
		}
	}
}

// JobCreated records job.created for a stored job.
func (r *Recorder) JobCreated(ctx context.Context, j job.Job) {
	r.warn(ctx, j.ApplicationID, j.ID, JobCreated{
		CreatedAt: j.CreatedAt, MediaType: j.MediaType, Size: j.Size, PageCount: j.PageCount,
		Retention: string(j.Retention), ServiceVersion: j.ServiceVersion,
	})
}

// JobFinished records job.finished for a job in its terminal state (Status, Attempts
// and Error set).
func (r *Recorder) JobFinished(ctx context.Context, j job.Job) {
	r.warn(ctx, j.ApplicationID, j.ID, JobFinished{
		Status: string(j.Status), Attempts: j.Attempts, ErrorCode: errorCode(j.Error), ServiceVersion: j.ServiceVersion,
	})
}

// JobDeleted records job.deleted; reason is DeletedByRequest or DeletedFirstRead.
func (r *Recorder) JobDeleted(ctx context.Context, j job.Job, reason string) {
	r.warn(ctx, j.ApplicationID, j.ID, JobDeleted{Reason: reason, Retention: string(j.Retention)})
}

// JobExpired records job.expired for a job the sweeper removed.
func (r *Recorder) JobExpired(ctx context.Context, j job.Job) {
	r.warn(ctx, j.ApplicationID, j.ID, JobExpired{Status: string(j.Status), Retention: string(j.Retention)})
}

// errorCode extracts the code of a job's error JSON; the message is never copied.
func errorCode(raw json.RawMessage) string {
	var e struct {
		Code string `json:"code"`
	}
	if raw == nil || json.Unmarshal(raw, &e) != nil {
		return ""
	}
	return e.Code
}

// ParseSince reads the start of a listing window as vde audit list and the admin API
// accept it: an RFC 3339 time, a date (midnight UTC), or a positive duration back from
// now (24h, 7d).
func ParseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return now.Add(-time.Duration(n) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("since %q is not an RFC 3339 time, a date or a positive duration (24h, 7d)", s)
}
