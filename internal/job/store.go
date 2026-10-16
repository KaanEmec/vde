package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// errNotTerminal: Finish was given a status that is not terminal (a programming error).
var errNotTerminal = errors.New("status is not terminal")

// touchInterval bounds how often TouchTokenLastUsed writes for one token.
const touchInterval = time.Minute

// noLimit is the LIMIT of a listing asked for with limit <= 0; both dialects take it.
const noLimit = math.MaxInt64

// sqlStore is the Store on database/sql, shared by SQLite and Postgres. Statements are
// written once with SQLite placeholders (? and ?N) and rewritten to $N for PostgreSQL;
// the few that differ (JSON table functions, the lease's row lock) branch on pg.
// Both keep times as Unix milliseconds and booleans as TRUE/FALSE, which SQLite stores
// as 1 and 0.
type sqlStore struct {
	db *sql.DB
	pg bool
}

// Close closes the database.
func (s *sqlStore) Close() error { return s.db.Close() }

// SchemaVersion returns the highest applied migration.
func (s *sqlStore) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.queryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("job: schema version: %w", err)
	}
	return v, nil
}

// q returns query in the store's dialect.
func (s *sqlStore) q(query string) string {
	if s.pg {
		return rebind(query)
	}
	return query
}

func (s *sqlStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.q(query), args...)
}

func (s *sqlStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.q(query), args...)
}

func (s *sqlStore) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.q(query), args...)
}

// rebind rewrites SQLite placeholders to PostgreSQL ones: ?N becomes $N and each bare ?
// the next $N. A statement uses one style or the other, and no statement has a ? inside
// a string literal.
func rebind(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] != '?' {
			b.WriteByte(query[i])
			continue
		}
		j := i + 1
		for j < len(query) && query[j] >= '0' && query[j] <= '9' {
			j++
		}
		b.WriteByte('$')
		if j > i+1 {
			b.WriteString(query[i+1 : j])
		} else {
			n++
			b.WriteString(strconv.Itoa(n))
		}
		i = j - 1
	}
	return b.String()
}

// querier is what *sql.DB and *sql.Tx share.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}

// Times are stored as Unix milliseconds; a zero time is NULL.
func ms(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func fromMS(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64).UTC()
}

// Truncated to the stored precision so values handed back match what reads return.
func normalize(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }

// JSON is stored as TEXT on SQLite (the tables are STRICT) and as json on PostgreSQL,
// which keeps the text byte for byte; nil is NULL.
func jsonText(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

func fromText(v sql.NullString) json.RawMessage {
	if !v.Valid {
		return nil
	}
	return json.RawMessage(v.String)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUnique reports a unique or primary key violation of either driver.
func isUnique(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" // unique_violation
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func affected(res sql.Result) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ---- applications

const applicationColumns = `id, name, max_attempts, quota_limit, quota_window, requests_per_minute,
	submits_per_minute, max_in_progress, retention_default, force_none, html_policy, enabled,
	source, created_at, updated_at`

func scanApplication(r scanner) (Application, error) {
	var a Application
	var created, updated sql.NullInt64
	err := r.Scan(&a.ID, &a.Name, &a.MaxAttempts, &a.QuotaLimit, &a.QuotaWindow, &a.RequestsPerMinute,
		&a.SubmitsPerMinute, &a.MaxInProgress, &a.RetentionDefault, &a.ForceNone, &a.HTMLPolicy, &a.Enabled,
		&a.Source, &created, &updated)
	a.CreatedAt, a.UpdatedAt = fromMS(created), fromMS(updated)
	return a, err
}

// UpsertApplication implements Store.
func (s *sqlStore) UpsertApplication(ctx context.Context, a Application, now time.Time) error {
	_, err := s.exec(ctx, `
		INSERT INTO applications (`+applicationColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, 'config', ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, max_attempts = excluded.max_attempts,
			quota_limit = excluded.quota_limit, quota_window = excluded.quota_window,
			requests_per_minute = excluded.requests_per_minute,
			submits_per_minute = excluded.submits_per_minute,
			max_in_progress = excluded.max_in_progress, retention_default = excluded.retention_default,
			force_none = excluded.force_none, html_policy = excluded.html_policy,
			enabled = TRUE, source = 'config', updated_at = excluded.updated_at`,
		a.ID, a.Name, a.MaxAttempts, a.QuotaLimit, a.QuotaWindow, a.RequestsPerMinute,
		a.SubmitsPerMinute, a.MaxInProgress, a.RetentionDefault, a.ForceNone, a.HTMLPolicy,
		now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("job: upsert application: %w", err)
	}
	return nil
}

// DisableApplicationsNotIn implements Store.
func (s *sqlStore) DisableApplicationsNotIn(ctx context.Context, ids []string, now time.Time) ([]string, error) {
	keep, err := json.Marshal(append([]string{}, ids...))
	if err != nil {
		return nil, fmt.Errorf("job: disable applications: %w", err)
	}
	query := `
		UPDATE applications SET enabled = FALSE, updated_at = ?
		WHERE enabled = TRUE AND source = 'config' AND id NOT IN (SELECT value FROM json_each(?))
		RETURNING id`
	if s.pg {
		query = `
		UPDATE applications SET enabled = FALSE, updated_at = ?
		WHERE enabled = TRUE AND source = 'config' AND id NOT IN (SELECT jsonb_array_elements_text(?::jsonb))
		RETURNING id`
	}
	rows, err := s.query(ctx, query, now.UnixMilli(), string(keep))
	if err != nil {
		return nil, fmt.Errorf("job: disable applications: %w", err)
	}
	defer rows.Close()
	var disabled []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("job: disable applications: %w", err)
		}
		disabled = append(disabled, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("job: disable applications: %w", err)
	}
	return disabled, nil
}

// CreateApplication implements Store.
func (s *sqlStore) CreateApplication(ctx context.Context, a Application, now time.Time) error {
	_, err := s.exec(ctx, `
		INSERT INTO applications (`+applicationColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'admin', ?, ?)`,
		a.ID, a.Name, a.MaxAttempts, a.QuotaLimit, a.QuotaWindow, a.RequestsPerMinute,
		a.SubmitsPerMinute, a.MaxInProgress, a.RetentionDefault, a.ForceNone, a.HTMLPolicy, a.Enabled,
		now.UnixMilli(), now.UnixMilli())
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("job: create application: %w", err)
	}
	return nil
}

// UpdateApplication implements Store.
func (s *sqlStore) UpdateApplication(ctx context.Context, a Application, now time.Time) error {
	res, err := s.exec(ctx, `
		UPDATE applications SET name = ?, max_attempts = ?, quota_limit = ?, quota_window = ?,
			requests_per_minute = ?, submits_per_minute = ?, max_in_progress = ?,
			retention_default = ?, force_none = ?, html_policy = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		a.Name, a.MaxAttempts, a.QuotaLimit, a.QuotaWindow, a.RequestsPerMinute, a.SubmitsPerMinute,
		a.MaxInProgress, a.RetentionDefault, a.ForceNone, a.HTMLPolicy, a.Enabled, now.UnixMilli(), a.ID)
	if err != nil {
		return fmt.Errorf("job: update application: %w", err)
	}
	if n, err := affected(res); err != nil || n == 0 {
		return errors.Join(ErrNotFound, err)
	}
	return nil
}

// GetApplication implements Store.
func (s *sqlStore) GetApplication(ctx context.Context, id string) (Application, error) {
	return s.getApplication(ctx, s.db, id)
}

func (s *sqlStore) getApplication(ctx context.Context, q querier, id string) (Application, error) {
	a, err := scanApplication(q.QueryRowContext(ctx, s.q(`SELECT `+applicationColumns+` FROM applications WHERE id = ?`), id))
	if err != nil {
		return Application{}, fmt.Errorf("job: get application: %w", notFound(err))
	}
	return a, nil
}

// ListApplications implements Store.
func (s *sqlStore) ListApplications(ctx context.Context) ([]Application, error) {
	rows, err := s.query(ctx, `SELECT `+applicationColumns+` FROM applications ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("job: list applications: %w", err)
	}
	defer rows.Close()
	var out []Application
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			return nil, fmt.Errorf("job: list applications: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("job: list applications: %w", err)
	}
	return out, nil
}

// ---- tokens

const tokenColumns = `id, application_id, hash, scopes, label, expires_at, revoked_at, created_at, last_used_at`

func scanToken(r scanner) (Token, error) {
	var t Token
	var scopes string
	var app sql.NullString
	var expires, revoked, created, used sql.NullInt64
	err := r.Scan(&t.ID, &app, &t.Hash, &scopes, &t.Label, &expires, &revoked, &created, &used)
	t.ApplicationID = app.String
	if scopes != "" {
		t.Scopes = strings.Split(scopes, ",")
	}
	t.ExpiresAt, t.RevokedAt, t.CreatedAt, t.LastUsedAt = fromMS(expires), fromMS(revoked), fromMS(created), fromMS(used)
	return t, err
}

// CreateToken implements Store.
func (s *sqlStore) CreateToken(ctx context.Context, t Token) error {
	if t.ExpiresAt.IsZero() || t.CreatedAt.IsZero() || len(t.Hash) == 0 {
		return errors.New("job: create token: hash, created_at and expires_at are required")
	}
	_, err := s.exec(ctx, `INSERT INTO tokens (`+tokenColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, nullString(t.ApplicationID), t.Hash, strings.Join(t.Scopes, ","), t.Label,
		ms(t.ExpiresAt), ms(t.RevokedAt), ms(t.CreatedAt), ms(t.LastUsedAt))
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("job: create token: %w", err)
	}
	return nil
}

// GetTokenByID implements Store.
func (s *sqlStore) GetTokenByID(ctx context.Context, id string) (Token, error) {
	t, err := scanToken(s.queryRow(ctx, `SELECT `+tokenColumns+` FROM tokens WHERE id = ?`, id))
	if err != nil {
		return Token{}, fmt.Errorf("job: get token: %w", notFound(err))
	}
	return t, nil
}

// ListTokens implements Store.
func (s *sqlStore) ListTokens(ctx context.Context, applicationID string) ([]Token, error) {
	rows, err := s.query(ctx,
		`SELECT `+tokenColumns+` FROM tokens WHERE application_id IS NOT DISTINCT FROM ? ORDER BY created_at, id`, nullString(applicationID))
	if err != nil {
		return nil, fmt.Errorf("job: list tokens: %w", err)
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("job: list tokens: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("job: list tokens: %w", err)
	}
	return out, nil
}

// RevokeToken implements Store.
func (s *sqlStore) RevokeToken(ctx context.Context, id string, now time.Time) error {
	res, err := s.exec(ctx, `UPDATE tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`,
		now.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("job: revoke token: %w", err)
	}
	if n, err := affected(res); err != nil || n == 0 {
		return errors.Join(ErrNotFound, err)
	}
	return nil
}

// TouchTokenLastUsed implements Store.
func (s *sqlStore) TouchTokenLastUsed(ctx context.Context, id string, now time.Time) error {
	_, err := s.exec(ctx, `
		UPDATE tokens SET last_used_at = ?
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at <= ?)`,
		now.UnixMilli(), id, now.Add(-touchInterval).UnixMilli())
	if err != nil {
		return fmt.Errorf("job: touch token: %w", err)
	}
	return nil
}

// ---- quota and audit

// IncrementQuota implements Store.
func (s *sqlStore) IncrementQuota(ctx context.Context, applicationID string, windowStart time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, ErrQuotaExhausted
	}
	var count int
	err := s.queryRow(ctx, `
		INSERT INTO quota_usage (application_id, window_start, count) VALUES (?, ?, 1)
		ON CONFLICT (application_id, window_start) DO UPDATE SET count = quota_usage.count + 1
			WHERE quota_usage.count < ?
		RETURNING count`,
		applicationID, windowStart.UnixMilli(), limit).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrQuotaExhausted
	}
	if err != nil {
		return 0, fmt.Errorf("job: increment quota: %w", err)
	}
	return count, nil
}

// GetQuota implements Store.
func (s *sqlStore) GetQuota(ctx context.Context, applicationID string, windowStart time.Time) (int, error) {
	var count int
	err := s.queryRow(ctx,
		`SELECT count FROM quota_usage WHERE application_id = ? AND window_start = ?`,
		applicationID, windowStart.UnixMilli()).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("job: get quota: %w", err)
	}
	return count, nil
}

// AppendAudit implements Store.
func (s *sqlStore) AppendAudit(ctx context.Context, e AuditEvent) (int64, error) {
	details := "{}"
	if e.Details != nil {
		if !json.Valid(e.Details) {
			return 0, errors.New("job: append audit: details are not JSON")
		}
		details = string(e.Details)
	}
	if e.At.IsZero() || e.Kind == "" {
		return 0, errors.New("job: append audit: at and kind are required")
	}
	var id int64
	err := s.queryRow(ctx, `
		INSERT INTO audit_events (at, kind, application_id, actor, job_id, details)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		e.At.UnixMilli(), e.Kind, e.ApplicationID, e.Actor, e.JobID, details).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("job: append audit: %w", err)
	}
	return id, nil
}

// ---- jobs

const jobColumns = `id, application_id, status, attempts, result, feedback, error, judge_confidence,
	service_version, representation, media_type, size, page_count, retention, idempotency_key,
	body_hash, attachments, extractor_instructions, judge_instructions, schema, max_attempts, created_at,
	started_at, finished_at, expires_at, lease_until, worker_id, inputs_deleted_at,
	content_deleted_at`

func scanJob(r scanner) (Job, error) {
	var j Job
	var result, feedback, jobErr, key, attachments sql.NullString
	var confidence sql.NullFloat64
	var created, started, finished, expires, lease, inputsDeleted, contentDeleted sql.NullInt64
	err := r.Scan(&j.ID, &j.ApplicationID, &j.Status, &j.Attempts, &result, &feedback, &jobErr, &confidence,
		&j.ServiceVersion, &j.Representation, &j.MediaType, &j.Size, &j.PageCount, &j.Retention, &key,
		&j.BodyHash, &attachments, &j.ExtractorInstructions, &j.JudgeInstructions, &j.Schema, &j.MaxAttempts, &created,
		&started, &finished, &expires, &lease, &j.WorkerID, &inputsDeleted,
		&contentDeleted)
	if err != nil {
		return Job{}, err
	}
	j.Result, j.Feedback, j.Error = fromText(result), fromText(feedback), fromText(jobErr)
	if confidence.Valid {
		j.JudgeConfidence = &confidence.Float64
	}
	j.IdempotencyKey = key.String
	if attachments.Valid {
		if err := json.Unmarshal([]byte(attachments.String), &j.Attachments); err != nil {
			return Job{}, fmt.Errorf("attachments column: %w", err)
		}
	}
	j.CreatedAt, j.StartedAt, j.FinishedAt = fromMS(created), fromMS(started), fromMS(finished)
	j.ExpiresAt, j.LeaseUntil = fromMS(expires), fromMS(lease)
	j.InputsDeletedAt, j.ContentDeletedAt = fromMS(inputsDeleted), fromMS(contentDeleted)
	return j, nil
}

// CreateJob implements Store.
func (s *sqlStore) CreateJob(ctx context.Context, j *Job) error {
	if j.ApplicationID == "" || j.ServiceVersion == "" || j.Representation == "" || j.MediaType == "" {
		return errors.New("job: create job: application, service version, representation and media type are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("job: create job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	app, err := s.getApplication(ctx, tx, j.ApplicationID)
	if err != nil {
		return err
	}
	if !app.Enabled {
		return fmt.Errorf("job: create job: application disabled: %w", ErrNotFound)
	}
	if j.ID == "" {
		j.ID = NewID()
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now()
	}
	j.CreatedAt = normalize(j.CreatedAt)
	if j.Retention, j.ExpiresAt, err = expiry(j.CreatedAt, j.Retention, app); err != nil {
		return err
	}
	j.Status, j.Attempts = StatusQueued, 0
	var attachments any // NULL when there are none
	if len(j.Attachments) > 0 {
		b, err := json.Marshal(j.Attachments)
		if err != nil {
			return fmt.Errorf("job: create job: %w", err)
		}
		attachments = string(b)
	}

	_, err = tx.ExecContext(ctx, s.q(`
		INSERT INTO jobs (id, application_id, status, attempts, service_version, representation,
			media_type, size, page_count, retention, idempotency_key, body_hash, attachments,
			extractor_instructions, judge_instructions, schema, max_attempts, created_at, expires_at)
		VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		j.ID, j.ApplicationID, j.Status, j.ServiceVersion, j.Representation,
		j.MediaType, j.Size, j.PageCount, j.Retention, nullString(j.IdempotencyKey), j.BodyHash, attachments,
		j.ExtractorInstructions, j.JudgeInstructions, j.Schema, j.MaxAttempts,
		j.CreatedAt.UnixMilli(), j.ExpiresAt.UnixMilli())
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("job: create job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("job: create job: %w", err)
	}
	return nil
}

// GetJob implements Store.
func (s *sqlStore) GetJob(ctx context.Context, applicationID, id string) (Job, error) {
	j, err := scanJob(s.queryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE id = ? AND application_id = ?`, id, applicationID))
	if err != nil {
		return Job{}, fmt.Errorf("job: get job: %w", notFound(err))
	}
	return j, nil
}

// GetJobAny implements Store.
func (s *sqlStore) GetJobAny(ctx context.Context, id string) (Job, error) {
	j, err := scanJob(s.queryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if err != nil {
		return Job{}, fmt.Errorf("job: get job: %w", notFound(err))
	}
	return j, nil
}

// leaseQuery is the one statement of LeaseNextQueued: ?1 now, ?2 lease_until, ?3 worker
// id, ?4 the maxInProgress limits as a JSON object. limitOf looks up the application's
// limit in ?4 and lock is the row-lock clause of the candidate subquery.
func leaseQuery(limitOf, lock string) string {
	return `
		UPDATE jobs SET status = 'running', started_at = COALESCE(started_at, ?1),
			lease_until = ?2, worker_id = ?3
		WHERE id = (
			SELECT j.id FROM jobs j
			WHERE (j.status = 'queued' OR (j.status = 'running' AND j.lease_until < ?1))
			  AND (
				COALESCE(` + limitOf + `, 0) <= 0
				OR (SELECT COUNT(*) FROM jobs r
				    WHERE r.application_id = j.application_id AND r.status = 'running'
				      AND r.lease_until >= ?1)
				   < ` + limitOf + `
			  )
			ORDER BY j.created_at, j.id
			LIMIT 1 ` + lock + `
		)
		RETURNING ` + jobColumns
}

var (
	// SQLite serializes writers, so the subquery and the update cannot interleave.
	leaseSQLite = leaseQuery(`(SELECT l.value FROM json_each(?4) l WHERE l.key = j.application_id)`, "")
	// PostgreSQL: the candidate row is locked by the subquery and rows another
	// transaction holds are skipped, so concurrent replicas never take the same job.
	leasePostgres = leaseQuery(
		`(SELECT l.value::integer FROM jsonb_each_text(?4::jsonb) l WHERE l.key = j.application_id)`,
		"FOR UPDATE SKIP LOCKED")
)

// LeaseNextQueued implements Store. The candidate is chosen and taken in one UPDATE, so
// two workers never lease the same job. On PostgreSQL, concurrent leases may each count
// the running jobs before the other commits, so an application can briefly exceed its
// max_in_progress by the number of replicas leasing at the same instant.
func (s *sqlStore) LeaseNextQueued(ctx context.Context, workerID string, now, leaseUntil time.Time, maxInProgress map[string]int) (Job, error) {
	if maxInProgress == nil {
		maxInProgress = map[string]int{}
	}
	limits, err := json.Marshal(maxInProgress)
	if err != nil {
		return Job{}, fmt.Errorf("job: lease: %w", err)
	}
	query := leaseSQLite
	if s.pg {
		query = leasePostgres
	}
	j, err := scanJob(s.queryRow(ctx, query, now.UnixMilli(), leaseUntil.UnixMilli(), workerID, string(limits)))
	if err != nil {
		return Job{}, notFound(err)
	}
	return j, nil
}

// RenewLease implements Store.
func (s *sqlStore) RenewLease(ctx context.Context, id, workerID string, leaseUntil time.Time) error {
	res, err := s.exec(ctx,
		`UPDATE jobs SET lease_until = ? WHERE id = ? AND status = 'running' AND worker_id = ?`,
		leaseUntil.UnixMilli(), id, workerID)
	if err != nil {
		return fmt.Errorf("job: renew lease: %w", err)
	}
	return s.oneRow(ctx, res, id)
}

// RecordAttempt implements Store.
func (s *sqlStore) RecordAttempt(ctx context.Context, id, workerID string, p Progress, leaseUntil time.Time) error {
	res, err := s.exec(ctx, `
		UPDATE jobs SET attempts = ?, result = ?, feedback = ?, lease_until = ?
		WHERE id = ? AND status = 'running' AND worker_id = ? AND attempts <= ?`,
		p.Attempts, jsonText(p.Result), jsonText(p.Feedback), leaseUntil.UnixMilli(),
		id, workerID, p.Attempts)
	if err != nil {
		return fmt.Errorf("job: record attempt: %w", err)
	}
	return s.oneRow(ctx, res, id)
}

// Finish implements Store.
func (s *sqlStore) Finish(ctx context.Context, id, workerID string, o Outcome, now time.Time) error {
	if !o.Status.Terminal() {
		return fmt.Errorf("job: finish: %w: %q", errNotTerminal, o.Status)
	}
	var confidence any
	if o.JudgeConfidence != nil {
		confidence = *o.JudgeConfidence
	}
	res, err := s.exec(ctx, `
		UPDATE jobs SET status = ?1, attempts = CASE WHEN attempts > ?2 THEN attempts ELSE ?2 END, result = ?3, feedback = ?4,
			error = ?5, judge_confidence = ?6, finished_at = ?7, lease_until = NULL
		WHERE id = ?8 AND status = 'running' AND (?9 = '' OR worker_id = ?9)`,
		o.Status, o.Attempts, jsonText(o.Result), jsonText(o.Feedback), jsonText(o.Error),
		confidence, now.UnixMilli(), id, workerID)
	if err != nil {
		return fmt.Errorf("job: finish: %w", err)
	}
	return s.oneRow(ctx, res, id)
}

// MarkContentDeleted implements Store.
func (s *sqlStore) MarkContentDeleted(ctx context.Context, id string, what Content, now time.Time) error {
	var res sql.Result
	var err error
	switch what {
	case ContentInputs:
		res, err = s.exec(ctx, `
			UPDATE jobs SET extractor_instructions = '', judge_instructions = '', schema = '',
				inputs_deleted_at = COALESCE(inputs_deleted_at, ?)
			WHERE id = ?`, now.UnixMilli(), id)
	case ContentAll:
		res, err = s.exec(ctx, `
			UPDATE jobs SET extractor_instructions = '', judge_instructions = '', schema = '',
				result = NULL, feedback = NULL, error = NULL, judge_confidence = NULL, attachments = NULL,
				inputs_deleted_at = COALESCE(inputs_deleted_at, ?1), content_deleted_at = ?1
			WHERE id = ?2 AND content_deleted_at IS NULL
			  AND status IN ('accepted', 'rejected', 'failed')`, now.UnixMilli(), id)
	default:
		return fmt.Errorf("job: mark content deleted: unknown content %d", what)
	}
	if err != nil {
		return fmt.Errorf("job: mark content deleted: %w", err)
	}
	n, err := affected(res)
	if err != nil {
		return fmt.Errorf("job: mark content deleted: %w", err)
	}
	if n > 0 {
		s.checkpoint(ctx)
		return nil
	}
	j, err := s.GetJobAny(ctx, id)
	switch {
	case err != nil:
		return err
	case !j.ContentDeletedAt.IsZero():
		return ErrNotFound // someone else deleted it first
	}
	return ErrConflict // not terminal yet
}

// DeleteJob implements Store.
func (s *sqlStore) DeleteJob(ctx context.Context, id string) error {
	res, err := s.exec(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("job: delete job: %w", err)
	}
	if n, err := affected(res); err != nil || n == 0 {
		return errors.Join(ErrNotFound, err)
	}
	s.checkpoint(ctx)
	return nil
}

// checkpoint copies the WAL into the database file and truncates it, after a statement
// that deleted job content. secure_delete zeroes the deleted content in the database
// pages, but the WAL keeps the earlier page images, content included, until a
// checkpoint. Best effort: a failure, or readers that keep it from completing, is logged
// at debug and never fails the caller; the next checkpoint catches up.
func (s *sqlStore) checkpoint(ctx context.Context) {
	if s.pg {
		return // no WAL file to scrub; see Postgres
	}
	var busy, frames, done int
	err := s.queryRow(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &done)
	if err != nil || busy != 0 {
		slog.DebugContext(ctx, "wal checkpoint after content deletion incomplete",
			slog.String("component", "job"), slog.Int("busy", busy), slog.Any("error", err))
	}
}

// FindByIdempotencyKey implements Store.
func (s *sqlStore) FindByIdempotencyKey(ctx context.Context, applicationID, key string) (Job, error) {
	j, err := scanJob(s.queryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE application_id = ? AND idempotency_key = ?`, applicationID, key))
	if err != nil {
		return Job{}, fmt.Errorf("job: find by idempotency key: %w", notFound(err))
	}
	return j, nil
}

// ListExpired implements Store. Both bounds are checked, so the expires_at index
// serves the query.
func (s *sqlStore) ListExpired(ctx context.Context, now time.Time, limit int) ([]Job, error) {
	return s.queryJobs(ctx, "list expired",
		`SELECT `+jobColumns+` FROM jobs WHERE expires_at <= ? AND created_at <= ? ORDER BY expires_at, id LIMIT ?`,
		now.UnixMilli(), now.Add(-IdempotencyWindow).UnixMilli(), limit)
}

// ListContentExpired implements Store.
func (s *sqlStore) ListContentExpired(ctx context.Context, now time.Time, limit int) ([]Job, error) {
	return s.queryJobs(ctx, "list content expired", `
		SELECT `+jobColumns+` FROM jobs
		WHERE expires_at <= ? AND content_deleted_at IS NULL AND status IN ('accepted', 'rejected', 'failed')
		ORDER BY expires_at, id LIMIT ?`,
		now.UnixMilli(), limit)
}

// queryJobs runs a query over jobColumns and scans every row; what names it in errors.
func (s *sqlStore) queryJobs(ctx context.Context, what, query string, args ...any) ([]Job, error) {
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("job: %s: %w", what, err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("job: %s: %w", what, err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("job: %s: %w", what, err)
	}
	return out, nil
}

// CountRunning implements Store.
func (s *sqlStore) CountRunning(ctx context.Context, applicationID string) (int, error) {
	var n int
	err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM jobs WHERE application_id = ? AND status = 'running'`, applicationID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("job: count running: %w", err)
	}
	return n, nil
}

// CountInProgress implements Store.
func (s *sqlStore) CountInProgress(ctx context.Context, applicationID string) (int, error) {
	var n int
	err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM jobs WHERE application_id = ? AND status IN ('queued', 'running')`, applicationID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("job: count in progress: %w", err)
	}
	return n, nil
}

// ListJobs implements Store.
func (s *sqlStore) ListJobs(ctx context.Context, f JobFilter, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = noLimit
	}
	rows, err := s.query(ctx, `
		SELECT `+jobColumns+` FROM jobs
		WHERE (?1 = '' OR application_id = ?1) AND (?2 = '' OR status = ?2) AND created_at >= ?3
		ORDER BY created_at DESC, id DESC LIMIT ?4`,
		f.ApplicationID, string(f.Status), f.Since.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("job: list jobs: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("job: list jobs: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("job: list jobs: %w", err)
	}
	return out, nil
}

// oneRow turns an update that matched no row into ErrNotFound (no such job) or
// ErrConflict (the state machine or the lease refused it).
func (s *sqlStore) oneRow(ctx context.Context, res sql.Result, id string) error {
	n, err := affected(res)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var one int
	err = s.queryRow(ctx, `SELECT 1 FROM jobs WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

// ListStaleRunning implements Store.
func (s *sqlStore) ListStaleRunning(ctx context.Context, createdBefore time.Time, limit int) ([]Job, error) {
	rows, err := s.query(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE status = 'running' AND created_at <= ? ORDER BY created_at, id LIMIT ?`,
		createdBefore.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("job: list stale running: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("job: list stale running: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("job: list stale running: %w", err)
	}
	return out, nil
}

// DeleteQuotaBefore implements Store.
func (s *sqlStore) DeleteQuotaBefore(ctx context.Context, applicationID string, before time.Time) (int64, error) {
	res, err := s.exec(ctx,
		`DELETE FROM quota_usage WHERE application_id = ? AND window_start < ?`, applicationID, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("job: delete quota: %w", err)
	}
	return affected(res)
}

// DeleteTokensBefore implements Store.
func (s *sqlStore) DeleteTokensBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.exec(ctx,
		`DELETE FROM tokens WHERE expires_at < ?1 OR (revoked_at IS NOT NULL AND revoked_at < ?1)`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("job: delete tokens: %w", err)
	}
	return affected(res)
}

// ListAudit implements Store.
func (s *sqlStore) ListAudit(ctx context.Context, f AuditFilter, limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = noLimit
	}
	rows, err := s.query(ctx, `
		SELECT id, at, kind, application_id, actor, job_id, details FROM audit_events
		WHERE (?1 = '' OR application_id = ?1) AND (?2 = '' OR kind = ?2) AND (?3 = '' OR job_id = ?3) AND at >= ?4
		ORDER BY at, id LIMIT ?5`,
		f.ApplicationID, f.Kind, f.JobID, f.Since.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("job: list audit: %w", err)
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var at int64
		var details string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.ApplicationID, &e.Actor, &e.JobID, &details); err != nil {
			return nil, fmt.Errorf("job: list audit: %w", err)
		}
		e.At, e.Details = time.UnixMilli(at).UTC(), json.RawMessage(details)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("job: list audit: %w", err)
	}
	return out, nil
}

// DeleteAuditBefore implements Store.
func (s *sqlStore) DeleteAuditBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.exec(ctx, `DELETE FROM audit_events WHERE at < ?`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("job: delete audit: %w", err)
	}
	return affected(res)
}
