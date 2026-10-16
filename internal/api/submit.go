package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
)

// Limits of contract §4 that the configuration does not carry; the document, page and
// request limits come from config.Limits.
const (
	defaultRequestBytes    = 128 << 20 // limits.request_bytes when unset
	maxInstructionsBytes   = 64 << 10
	maxSchemaBytes         = 256 << 10
	maxOptionBytes         = 32 // retention and max_attempts
	maxIdempotencyKeyChars = 128
)

const (
	headerIdempotencyKey      = "Idempotency-Key"
	headerIdempotencyReplayed = "Idempotency-Replayed"
)

// Part names of contract §4. They are the only caller-visible names an error message
// may interpolate.
const (
	partDocument              = "document"
	partPage                  = "page"
	partExtractorInstructions = "extractor_instructions"
	partJudgeInstructions     = "judge_instructions"
	partSchema                = "schema"
	partRetention             = "retention"
	partMaxAttempts           = "max_attempts"
)

// Messages of the submit path (errors.go explains the rules).
const (
	msgSubmitDisabled         = "Job submission is not configured on this service."
	msgNotMultipart           = "The request must be multipart/form-data with a boundary."
	msgMalformedMultipart     = "The multipart body is malformed."
	msgUnknownPart            = "The request has a part this service does not define."
	msgPartMissing            = "The %s part is required."
	msgPartRepeated           = "The %s part may appear only once."
	msgPartNoMediaType        = "The %s part needs a Content-Type."
	msgPartMediaType          = "The %s part has a media type this service does not accept for it."
	msgPartNotUTF8            = "The %s part is not valid UTF-8."
	msgPartTooLarge           = "The %s part exceeds its size limit."
	msgTooManyPages           = "The request has more than %d page parts."
	msgRequestTooLarge        = "The request body exceeds %d bytes."
	msgBadRetention           = "The retention part must be none or default."
	msgBadMaxAttempts         = "The max_attempts part must be a positive integer."
	msgDocumentUnreadable     = "The document or a page is empty or cannot be decoded."
	msgDocumentTooLarge       = "The document or a page exceeds its size limit."
	msgDocumentMediaType      = "The document's media type is not accepted, or a part's content does not match its media type."
	msgUnsupportedDocument    = "No configured endpoint can read this document."
	msgInvalidSchema          = "The schema part is not a valid JSON Schema 2020-12."
	msgIdempotencyKeyTooLong  = "The Idempotency-Key header exceeds %d characters."
	msgIdempotencyMismatch    = "The Idempotency-Key was used with a different request."
	msgRateLimited            = "The application's request rate limit is exceeded."
	msgSubmitRateLimited      = "The application's submission rate limit is exceeded."
	msgTooManyInProgress      = "The application has reached its limit of %d jobs in progress."
	msgQuotaExhausted         = "The quota of %d jobs per window is spent."
	msgStorageFailed          = "The job could not be stored; submit it again."
	msgSubmitConfigIncomplete = "The service's document configuration is not usable."
)

// part is one read part: its declared Content-Type, filename (informational only), bytes
// and their SHA-256, computed while reading.
type part struct {
	mediaType string
	filename  string
	data      []byte
	sum       [sha256.Size]byte
}

// submission is the parts of one POST /v1/jobs request; nil means the part was absent.
type submission struct {
	document              *part
	pages                 []part
	extractorInstructions *part
	judgeInstructions     *part
	schema                *part
	retention             *part
	maxAttempts           *part
}

// submitJob is POST /v1/jobs (contract §4, §6 step 1): createJob, then 202 with the
// queued job.
func (s *server) submitJob(w http.ResponseWriter, r *http.Request) {
	j, done := s.createJob(w, r)
	if done {
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, jobToResponse(j))
}

// createJob is the submission path POST /v1/jobs and POST /v1/extract share. It reads
// the whole request under the limits, multipart or JSON (submit_json.go), then refuses
// or accepts in this order: parts and limits, media type, schema, representation,
// idempotency, rate limits, quota. Nothing is stored before every check passed. No
// content is ever logged or quoted in an error. done is true when it has answered: a
// refusal, or the replay of an Idempotency-Key; otherwise the job is stored and the
// pool woken, and the caller answers.
func (s *server) createJob(w http.ResponseWriter, r *http.Request) (_ *job.Job, done bool) {
	ctx := r.Context()
	app, _ := ApplicationFrom(ctx)
	cfg := s.deps.Config
	if s.deps.Blobs == nil || cfg == nil {
		closeIfBodyPending(w, r)
		writeError(w, r, newError(codeUnavailable, msgSubmitDisabled))
		return nil, true
	}
	requestLimit := cfg.Limits.RequestBytes
	if requestLimit <= 0 {
		requestLimit = defaultRequestBytes
	}
	r.Body = &limitedBody{ReadCloser: http.MaxBytesReader(w, r.Body, requestLimit)}
	// Every refusal first reads what is left of the body, up to the request limit, so
	// the client sees the status instead of a reset connection.
	refuse := func(e apiError) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeError(w, r, e)
	}

	key := r.Header.Get(headerIdempotencyKey)
	if utf8.RuneCountInString(key) > maxIdempotencyKeyChars {
		refuse(newError(codeBadRequest, msgIdempotencyKeyTooLong, maxIdempotencyKeyChars))
		return nil, true
	}
	var sub *submission
	var e apiError
	var ok bool
	if mediaTypeIn(r.Header.Get("Content-Type"), "", "application/json") {
		sub, e, ok = s.readJSONSubmission(r, cfg, requestLimit)
	} else {
		sub, e, ok = readSubmission(r, cfg.Limits, requestLimit)
	}
	if !ok {
		refuse(e)
		return nil, true
	}
	retention, maxAttempts, e, ok := sub.options(app)
	if !ok {
		refuse(e)
		return nil, true
	}

	pages := make([]document.Page, len(sub.pages))
	for i, p := range sub.pages {
		pages[i] = document.Page{MediaType: p.mediaType, Data: p.data}
	}
	doc, err := document.New(document.Input{
		MediaType: sub.document.mediaType, Data: sub.document.data, Filename: sub.document.filename, Pages: pages,
	}, cfg.Limits)
	if err != nil {
		s.refuseDocument(w, r, err)
		return nil, true
	}
	var schema string
	if sub.schema != nil {
		if _, err := extract.CompileSchema(sub.schema.data); err != nil {
			refuse(newError(codeInvalidSchema, msgInvalidSchema))
			return nil, true
		}
		schema = string(sub.schema.data)
	}
	rep, err := selectRepresentation(cfg, app, doc)
	if err != nil {
		s.refuseDocument(w, r, err)
		return nil, true
	}

	bodyHash := sub.hash()
	if key != "" {
		unlock := s.limits.lockKey(app.ID, key)
		defer unlock()
		if s.replay(w, r, app, key, bodyHash) {
			return nil, true
		}
	}
	if !s.admit(w, r, app) {
		return nil, true
	}

	j := &job.Job{
		ID:                    job.NewID(),
		ApplicationID:         app.ID,
		ServiceVersion:        s.serviceVersion(app),
		Representation:        rep.Kind.String(),
		MediaType:             doc.MediaType,
		Size:                  int64(len(doc.Data)),
		PageCount:             len(doc.Pages),
		Retention:             retention,
		IdempotencyKey:        key,
		BodyHash:              bodyHash,
		Attachments:           jobAttachments(doc.Attachments),
		ExtractorInstructions: string(sub.extractorInstructions.data),
		JudgeInstructions:     string(sub.judgeInstructions.data),
		Schema:                schema,
		MaxAttempts:           maxAttempts,
		CreatedAt:             s.now(),
	}
	err = s.storeJob(ctx, j, doc)
	if errors.Is(err, job.ErrDuplicate) && key != "" && s.replay(w, r, app, key, bodyHash) {
		return nil, true // another process created the job with this key between lookup and insert
	}
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "job not stored",
			slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
		writeError(w, r, newError(codeUnavailable, msgStorageFailed))
		return nil, true
	}
	s.deps.Audit.JobCreated(ctx, *j) // nil-safe
	s.refreshInProgress(ctx, app.ID)
	if s.deps.Wake != nil {
		s.deps.Wake()
	}
	return j, false
}

// readSubmission streams the multipart body, enforcing the part rules and the per-part
// limits while reading. It reports false with the refusal on the first violation.
func readSubmission(r *http.Request, limits config.Limits, requestLimit int64) (*submission, apiError, bool) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, newError(codeBadRequest, msgNotMultipart), false
	}
	maxPages := limits.Pages
	if maxPages <= 0 {
		maxPages = document.DefaultPages
	}
	sub := &submission{}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, readError(r, err, requestLimit), false
		}
		name := p.FormName()
		var slot **part
		var limit int64
		switch name {
		case partDocument:
			slot, limit = &sub.document, limits.DocumentBytes
			if limit <= 0 {
				limit = document.DefaultDocumentBytes
			}
			if p.Header.Get("Content-Type") == "" {
				return nil, newError(codeBadRequest, msgPartNoMediaType, partDocument), false
			}
		case partPage:
			if len(sub.pages) == maxPages {
				return nil, newError(codePayloadTooLarge, msgTooManyPages, maxPages), false
			}
			limit = limits.PageBytes
			if limit <= 0 {
				limit = document.DefaultPageBytes
			}
		case partExtractorInstructions, partJudgeInstructions:
			slot, limit = &sub.extractorInstructions, maxInstructionsBytes
			if name == partJudgeInstructions {
				slot = &sub.judgeInstructions
			}
			if !mediaTypeIn(p.Header.Get("Content-Type"), "text/plain", "text/plain", "text/markdown") {
				return nil, newError(codeBadRequest, msgPartMediaType, name), false
			}
		case partSchema:
			slot, limit = &sub.schema, maxSchemaBytes
			if !mediaTypeIn(p.Header.Get("Content-Type"), "", "application/schema+json", "application/json") {
				return nil, newError(codeBadRequest, msgPartMediaType, name), false
			}
		case partRetention:
			slot, limit = &sub.retention, maxOptionBytes
		case partMaxAttempts:
			slot, limit = &sub.maxAttempts, maxOptionBytes
		default:
			return nil, newError(codeBadRequest, msgUnknownPart), false
		}
		if slot != nil && *slot != nil {
			return nil, newError(codeBadRequest, msgPartRepeated, name), false
		}
		read, err := readPart(p, limit)
		switch {
		case errors.Is(err, errPartTooLarge) && (name == partRetention || name == partMaxAttempts):
			return nil, optionError(name), false
		case errors.Is(err, errPartTooLarge):
			return nil, newError(codePayloadTooLarge, msgPartTooLarge, name), false
		case err != nil:
			return nil, readError(r, err, requestLimit), false
		}
		if slot == nil {
			sub.pages = append(sub.pages, read)
			continue
		}
		if (name == partExtractorInstructions || name == partJudgeInstructions) && !utf8.Valid(read.data) {
			return nil, newError(codeBadRequest, msgPartNotUTF8, name), false
		}
		*slot = &read
	}
	for _, required := range []struct {
		name string
		p    *part
	}{
		{partDocument, sub.document},
		{partExtractorInstructions, sub.extractorInstructions},
		{partJudgeInstructions, sub.judgeInstructions},
	} {
		if required.p == nil {
			return nil, newError(codeBadRequest, msgPartMissing, required.name), false
		}
	}
	return sub, newError(codeInternal, msgInternal), true
}

var errPartTooLarge = errors.New("api: part exceeds its limit")

// readPart reads p up to limit bytes, hashing them as they arrive.
func readPart(p *multipart.Part, limit int64) (part, error) {
	h := sha256.New()
	data, err := io.ReadAll(io.TeeReader(io.LimitReader(p, limit+1), h))
	if err != nil {
		return part{}, err
	}
	if int64(len(data)) > limit {
		return part{}, errPartTooLarge
	}
	out := part{mediaType: p.Header.Get("Content-Type"), filename: p.FileName(), data: data}
	h.Sum(out.sum[:0])
	return out, nil
}

// readError maps a failure reading the body: over the request limit is 413, anything
// else malformed multipart. multipart.Reader does not always wrap the body's error, so
// the body itself records that the limit was hit.
func readError(r *http.Request, err error, requestLimit int64) apiError {
	var tooLarge *http.MaxBytesError
	if lb, ok := r.Body.(*limitedBody); errors.As(err, &tooLarge) || ok && lb.tooLarge {
		return newError(codePayloadTooLarge, msgRequestTooLarge, requestLimit)
	}
	return newError(codeBadRequest, msgMalformedMultipart)
}

// limitedBody is the request body behind http.MaxBytesReader, recording whether a read
// went over the limit.
type limitedBody struct {
	io.ReadCloser
	tooLarge bool
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		b.tooLarge = true
	}
	return n, err
}

// mediaTypeIn reports whether the Content-Type value names one of types, ignoring
// parameters; an absent value counts as missing.
func mediaTypeIn(value, missing string, types ...string) bool {
	if value == "" {
		value = missing
	}
	name, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	for _, t := range types {
		if name == t {
			return true
		}
	}
	return false
}

func optionError(name string) apiError {
	if name == partRetention {
		return newError(codeBadRequest, msgBadRetention)
	}
	return newError(codeBadRequest, msgBadMaxAttempts)
}

// options reads retention (default when absent) and max_attempts (0, the
// application's limit, when absent; clamped to it otherwise).
func (sub *submission) options(app job.Application) (job.Retention, int, apiError, bool) {
	retention := job.RetentionDefault
	if sub.retention != nil {
		switch v := strings.TrimSpace(string(sub.retention.data)); v {
		case string(job.RetentionNone), string(job.RetentionDefault):
			retention = job.Retention(v)
		default:
			return "", 0, optionError(partRetention), false
		}
	}
	attempts := 0
	if sub.maxAttempts != nil {
		v := strings.TrimSpace(string(sub.maxAttempts.data))
		if v == "" || v[0] == '0' || len(v) > 9 || strings.Trim(v, "0123456789") != "" {
			return "", 0, optionError(partMaxAttempts), false
		}
		attempts, _ = strconv.Atoi(v) // nine digits at most: no overflow
		if app.MaxAttempts > 0 {
			attempts = min(attempts, app.MaxAttempts)
		}
	}
	return retention, attempts, newError(codeInternal, msgInternal), true
}

// hash is the idempotency body hash: SHA-256 over name, declared media type and
// content hash of every part, parts sorted by name and pages in request order. The
// multipart boundary and filenames do not count.
func (sub *submission) hash() string {
	h := sha256.New()
	write := func(h hash.Hash, name string, p *part) {
		if p != nil {
			_, _ = fmt.Fprintf(h, "%s\x00%s\x00%x\n", name, p.mediaType, p.sum)
		}
	}
	write(h, partDocument, sub.document)
	write(h, partExtractorInstructions, sub.extractorInstructions)
	write(h, partJudgeInstructions, sub.judgeInstructions)
	write(h, partMaxAttempts, sub.maxAttempts)
	for i := range sub.pages {
		write(h, partPage+"["+strconv.Itoa(i)+"]", &sub.pages[i])
	}
	write(h, partRetention, sub.retention)
	write(h, partSchema, sub.schema)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// selectRepresentation chooses what both roles read, from the configured groups'
// capabilities and the application's HTML policy.
func selectRepresentation(cfg *config.Config, app job.Application, doc *document.Document) (document.Representation, error) {
	policy, err := document.ResolveHTMLPolicy(cfg.Documents.HTML, app.HTMLPolicy)
	if err != nil {
		return document.Representation{}, err
	}
	return document.SelectWith(doc, cfg.Capabilities(cfg.Roles.Extractor.Group),
		cfg.Capabilities(cfg.Roles.Judge.Group), policy, document.NewImagePolicy(cfg.Documents.Images))
}

// refuseDocument answers a refused document (*document.Error) with its contract code
// and a fixed message; any other error is a configuration problem and answers 500.
func (s *server) refuseDocument(w http.ResponseWriter, r *http.Request, err error) {
	var de *document.Error
	if !errors.As(err, &de) {
		s.log.LogAttrs(r.Context(), slog.LevelError, "document configuration not usable",
			slog.String("request_id", requestIDFrom(r.Context())))
		writeError(w, r, newError(codeInternal, msgSubmitConfigIncomplete))
		return
	}
	e := newError(codeBadRequest, msgDocumentUnreadable)
	switch de.Code {
	case document.CodePayloadTooLarge:
		e = newError(codePayloadTooLarge, msgDocumentTooLarge)
	case document.CodeUnsupportedMediaType:
		e = newError(codeUnsupportedMediaType, msgDocumentMediaType)
	case document.CodeUnsupportedDocument:
		e = newError(codeUnsupportedDocument, msgUnsupportedDocument)
	}
	writeError(w, r, e)
}

// replay answers a submission whose Idempotency-Key the application used before: 202
// with the existing job as it is now (in its content-deleted form, replayResponse, once
// its content is gone) and Idempotency-Replayed: true when the body hash matches, 422 idempotency_mismatch when it does not. It reports false, having written
// nothing, when the key is unused. A replay charges no quota and runs nothing.
func (s *server) replay(w http.ResponseWriter, r *http.Request, app job.Application, key, bodyHash string) bool {
	ctx := r.Context()
	existing, err := s.deps.Store.FindByIdempotencyKey(ctx, app.ID, key)
	switch {
	case errors.Is(err, job.ErrNotFound):
		return false
	case err != nil:
		s.log.LogAttrs(ctx, slog.LevelError, "idempotency lookup failed",
			slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
		writeError(w, r, newError(codeUnavailable, msgStorageFailed))
	case existing.BodyHash != bodyHash:
		writeError(w, r, newError(codeIdempotencyMismatch, msgIdempotencyMismatch))
	default:
		w.Header().Set(headerIdempotencyReplayed, "true")
		w.Header().Set("Location", "/v1/jobs/"+existing.ID)
		writeJSON(w, http.StatusAccepted, replayResponse(existing, s.now()))
	}
	return true
}

// admit applies the submission limits, in order: submissions per minute, jobs in
// progress, quota. It charges the quota and updates X-Quota-Remaining, or answers 429
// (or 503 when the store fails) and reports false.
func (s *server) admit(w http.ResponseWriter, r *http.Request, app job.Application) bool {
	ctx := r.Context()
	now := s.now()
	if wait, ok := s.limits.take(app.ID, bucketSubmits, app.SubmitsPerMinute, now); !ok {
		writeError(w, r, newError(codeRateLimited, msgSubmitRateLimited).withRetryAfter(wait))
		return false
	}
	storeFailed := func(what string, err error) bool {
		s.log.LogAttrs(ctx, slog.LevelError, what,
			slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
		writeError(w, r, newError(codeUnavailable, msgStorageFailed))
		return false
	}
	if app.MaxInProgress > 0 { // queued and running: the ceiling holds before the workers lease
		inProgress, err := s.deps.Store.CountInProgress(ctx, app.ID)
		if err != nil {
			return storeFailed("jobs in progress not counted", err)
		}
		if inProgress >= app.MaxInProgress {
			writeError(w, r, newError(codeRateLimited, msgTooManyInProgress, app.MaxInProgress).
				withRetryAfter(inProgressRetryAfter))
			return false
		}
	}
	q, err := s.currentQuota(ctx, app)
	if err != nil {
		return storeFailed("quota not readable", err)
	}
	q, err = s.chargeQuota(ctx, app, q)
	if errors.Is(err, job.ErrQuotaExhausted) {
		q.Remaining = 0
		s.setQuotaHeaders(w.Header(), app, q)
		writeError(w, r, newError(codeQuotaExhausted, msgQuotaExhausted, q.Limit).withRetryAfter(untilReset(now, q.Reset)))
		return false
	}
	if err != nil {
		return storeFailed("quota not charged", err)
	}
	s.setQuotaHeaders(w.Header(), app, q)
	return true
}

// storeJob writes the document and page blobs, then the row. On any failure it removes
// the blobs it wrote.
func (s *server) storeJob(ctx context.Context, j *job.Job, doc *document.Document) (err error) {
	defer func() {
		if err != nil {
			if derr := s.deps.Blobs.DeleteAll(context.WithoutCancel(ctx), j.ID); derr != nil {
				s.log.LogAttrs(ctx, slog.LevelWarn, "blobs of an unstored job not deleted",
					slog.String("request_id", requestIDFrom(ctx)), slog.String("error", derr.Error()))
			}
		}
	}()
	if err := s.deps.Blobs.Put(ctx, j.ID, blob.Document, strings.NewReader(string(doc.Data))); err != nil {
		return err
	}
	for i, p := range doc.Pages {
		if err := s.deps.Blobs.Put(ctx, j.ID, blob.Page(i+1), strings.NewReader(string(p.Data))); err != nil {
			return err
		}
	}
	return s.deps.Store.CreateJob(ctx, j)
}

// serviceVersion is the service_version of the application's jobs.
func (s *server) serviceVersion(app job.Application) string {
	if s.deps.AppServiceVersion != nil {
		if v := s.deps.AppServiceVersion(app); v != "" {
			return v
		}
	}
	return s.deps.ServiceVersion
}

// refreshInProgress sets the application's vde_jobs_in_progress gauge from the store
// (queued plus running), after a submission or a deletion.
func (s *server) refreshInProgress(ctx context.Context, appID string) {
	if s.deps.Metrics == nil {
		return
	}
	if n, err := s.deps.Store.CountInProgress(ctx, appID); err == nil {
		s.deps.Metrics.SetJobsInProgress(appID, n)
	}
}
