package api

import (
	"bytes"
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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/pdf"
)

// Limits of contract §4 that the configuration does not carry; the document, page and
// request limits come from config.Limits.
const (
	maxInstructionsBytes   = 64 << 10
	maxSchemaBytes         = 256 << 10
	maxOptionBytes         = 32 // retention and max_attempts
	maxJudgeBytes          = 16 // the judge part: always, never or auto
	maxIdempotencyKeyChars = 128
	// pdfBusyRetryAfter is the Retry-After of a submission refused because every PDF
	// engine instance stayed busy (503 unavailable).
	pdfBusyRetryAfter = 5 * time.Second
	// queueFullRetryAfter is the Retry-After of a submission refused because
	// limits.max_queued_jobs jobs are queued (503 unavailable).
	queueFullRetryAfter = 5 * time.Second
	// queuedCountTTL is how long a count of the queued jobs is reused, so a flood of
	// submissions costs one count query per second and process.
	queuedCountTTL = time.Second
	// submitsBusyRetryAfter is the Retry-After of a submission refused over
	// limits.max_submits_in_flight (503 unavailable): a slot frees when another
	// submission has been read and stored.
	submitsBusyRetryAfter = 5 * time.Second
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
	partJudge                 = "judge"
)

// Messages of the submit path (errors.go explains the rules).
const (
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
	msgBadJudge               = "The judge part must be always, never or auto."
	msgJudgeNotAllowed        = "The judge part is not allowed: the application does not allow a per-request judge policy."
	msgDocumentUnreadable     = "The document or a page is empty or cannot be decoded."
	msgDocumentTooLarge       = "The document or a page exceeds its size limit."
	msgDocumentMediaType      = "The document's media type is not accepted, or a part's content does not match its media type."
	msgUnsupportedDocument    = "No configured endpoint can read this document."
	msgPDFTooManyPages        = "The PDF has more than %d pages (documents.pdf.max_pages), and no configured endpoint reads PDFs natively."
	msgInvalidSchema          = "The schema part is not a valid JSON Schema 2020-12."
	msgIdempotencyKeyTooLong  = "The Idempotency-Key header exceeds %d characters."
	msgIdempotencyMismatch    = "The Idempotency-Key was used with a different request."
	msgRateLimited            = "The application's request rate limit is exceeded."
	msgSubmitRateLimited      = "The application's submission rate limit is exceeded."
	msgTooManyInProgress      = "The application has reached its limit of %d jobs in progress."
	msgQuotaExhausted         = "The quota of %d jobs per window is spent."
	msgStorageFailed          = "The job could not be stored; submit it again."
	msgSubmitConfigIncomplete = "The service's document configuration is not usable."
	msgPDFBusy                = "The service is busy preparing other PDFs; submit again later."
	msgQueueFull              = "The service's job queue is full; submit again later."
	msgSubmitsBusy            = "The service is busy reading other submissions; submit again later."
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
	// document holds only the media type and filename while documentURL is set: a JSON
	// submission's url is fetched (fetchPending) after the submission limits.
	document              *part
	documentURL           string
	pages                 []part
	extractorInstructions *part
	judgeInstructions     *part
	schema                *part
	retention             *part
	maxAttempts           *part
	judge                 *part
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

// createJob is the submission path POST /v1/jobs and POST /v1/extract share. Its steps
// refuse in the order of the contract checks: the cheap limits before any part is kept,
// then the whole body read under the limits (the idempotency hash covers every part),
// then the work that costs more than reading, and the quota charge last. Nothing is
// stored before every check passed, and no content is logged or quoted in an error.
// done is true when it has answered: a refusal, or the replay of an Idempotency-Key;
// otherwise the job is stored and the pool woken, and the caller answers.
func (s *server) createJob(w http.ResponseWriter, r *http.Request) (_ *job.Job, done bool) {
	app, _ := ApplicationFrom(r.Context())
	cfg := s.deps.Config
	r.Body = &limitedBody{ReadCloser: http.MaxBytesReader(w, r.Body, cfg.Limits.RequestBytes)}

	// The submissions in flight come first, before any part is buffered, for every
	// request, replays included: the slot is what bounds the memory of bodies being read.
	// A saturated service, or an application holding its share, drains the body without
	// keeping it and answers at once.
	release, ok := s.submits.take(app.ID)
	if !ok {
		refuse(w, r, newError(codeUnavailable, msgSubmitsBusy).withRetryAfter(submitsBusyRetryAfter))
		return nil, true
	}
	defer release()
	key := r.Header.Get(headerIdempotencyKey)
	if utf8.RuneCountInString(key) > maxIdempotencyKeyChars {
		refuse(w, r, newError(codeBadRequest, msgIdempotencyKeyTooLong, maxIdempotencyKeyChars))
		return nil, true
	}
	// The queue ceiling refuses before the body is read, except for a request with an
	// Idempotency-Key: a replay creates nothing and must answer 202 (contract §4) even
	// with the queue full, so a keyed request is read in full under its slot first and
	// meets the ceiling only once the lookup found the key unused.
	if key == "" && !s.queueRoom(w, r) {
		return nil, true
	}
	sub, err := s.readBody(r)
	if err != nil {
		refuse(w, r, err)
		return nil, true
	}
	retention, maxAttempts, err := sub.options(app)
	if err != nil {
		refuse(w, r, err)
		return nil, true
	}
	policy, err := sub.judgePolicy(app, cfg)
	if err != nil {
		refuse(w, r, err)
		return nil, true
	}
	if key != "" {
		unlock := s.limits.lockKey(app.ID, key)
		defer unlock()
		if s.replayKeyed(w, r, app, key, sub) {
			return nil, true
		}
	}
	if !s.limitSubmit(w, r, app) {
		return nil, true
	}
	doc, schema, rep, done := s.prepare(w, r, app, sub)
	if done || !s.chargeSubmit(w, r, app) {
		return nil, true
	}

	j := &job.Job{
		ID:                    job.NewID(),
		ApplicationID:         app.ID,
		ServiceVersion:        s.serviceVersionFor(app, policy),
		Representation:        rep.Label(),
		MediaType:             doc.MediaType,
		Size:                  int64(len(doc.Data)),
		PageCount:             len(doc.Pages),
		Retention:             retention,
		IdempotencyKey:        key,
		BodyHash:              sub.hash(),
		Attachments:           jobAttachments(doc.Attachments),
		ExtractorInstructions: string(sub.extractorInstructions.data),
		JudgeInstructions:     string(sub.judgeInstructions.data),
		Schema:                schema,
		MaxAttempts:           maxAttempts,
		JudgePolicy:           string(policy),
		CreatedAt:             s.now(),
	}
	if !s.insert(w, r, app, j, doc) {
		return nil, true
	}
	return j, false
}

// refuse answers err after reading what is left of the body, up to the request limit,
// so the client sees the status instead of a reset connection.
func refuse(w http.ResponseWriter, r *http.Request, err error) {
	drainBody(r)
	writeError(w, err)
}

// readBody reads the whole request under the limits, multipart (readSubmission) or
// JSON (readJSONSubmission), with the part rules.
func (s *server) readBody(r *http.Request) (*submission, error) {
	cfg := s.deps.Config
	if mediaTypeIn(r.Header.Get("Content-Type"), "", "application/json") {
		return s.readJSONSubmission(r, cfg, cfg.Limits.RequestBytes)
	}
	return readSubmission(r, cfg.Limits, cfg.Limits.RequestBytes)
}

// replayKeyed looks up the Idempotency-Key under its lock. A used key is replayed, after
// the document url fetch (the body hash covers the fetched bytes); an unused one meets the
// queue ceiling the request skipped before its body was read. It reports true when it
// has answered.
func (s *server) replayKeyed(w http.ResponseWriter, r *http.Request, app job.Application, key string, sub *submission) bool {
	existing, found := s.findIdempotent(w, r, app, key)
	if existing == nil {
		return true // the lookup failed and was answered
	}
	if found {
		// The replay needs the body hash only: the job was checked when it was created.
		if err := s.fetchPending(r, sub, s.deps.Config.Limits); err != nil {
			refuse(w, r, err)
			return true
		}
		s.answerReplay(w, r, *existing, sub.hash())
		return true
	}
	return !s.queueRoom(w, r)
}

// prepare does the work that costs more than reading, in order: the document url fetch,
// the document checks and image conversion, the schema compilation and the
// representation (the PDF engine). done is true when it has refused.
func (s *server) prepare(w http.ResponseWriter, r *http.Request, app job.Application, sub *submission) (
	doc *document.Document, schema string, rep document.Representation, done bool) {
	cfg := s.deps.Config
	if err := s.fetchPending(r, sub, cfg.Limits); err != nil {
		refuse(w, r, err)
		return nil, "", rep, true
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
		return nil, "", rep, true
	}
	if sub.schema != nil {
		if _, err := extract.CompileSchema(sub.schema.data); err != nil {
			refuse(w, r, newError(codeInvalidSchema, msgInvalidSchema))
			return nil, "", rep, true
		}
		schema = string(sub.schema.data)
	}
	rep, err = selectRepresentation(r.Context(), cfg, app, doc, s.deps.PDF)
	if err != nil {
		s.refuseDocument(w, r, err)
		return nil, "", rep, true
	}
	return doc, schema, rep, false
}

// insert stores the job and its blobs, counts it as queued, sets the job's
// service_version header, records the audit event and wakes the pool. It reports false
// when it has answered: the store failed, or another process created the job with the
// same Idempotency-Key between the lookup and the insert (then the replay answers).
func (s *server) insert(w http.ResponseWriter, r *http.Request, app job.Application, j *job.Job, doc *document.Document) bool {
	ctx := r.Context()
	err := s.storeJob(ctx, j, doc)
	if errors.Is(err, job.ErrDuplicate) && j.IdempotencyKey != "" && s.replay(w, r, app, j.IdempotencyKey, j.BodyHash) {
		return false
	}
	if err != nil {
		s.storeUnavailable(w, r, "job not stored", err)
		return false
	}
	if s.deps.Config.Limits.MaxQueuedJobs > 0 {
		s.queued.add()
	}
	w.Header().Set(headerServiceVersion, j.ServiceVersion) // the version of the job's policy
	s.deps.Audit.JobCreated(ctx, *j)                       // nil-safe
	s.refreshInProgress(ctx, app.ID)
	if s.deps.Wake != nil {
		s.deps.Wake()
	}
	return true
}

// queueRoom applies limits.max_queued_jobs: it reports false after refusing with 503
// when the ceiling is reached or the queue cannot be counted, the body drained first.
func (s *server) queueRoom(w http.ResponseWriter, r *http.Request) bool {
	limit := s.deps.Config.Limits.MaxQueuedJobs
	if limit <= 0 {
		return true
	}
	full, err := s.queued.atLeast(r.Context(), s.deps.Store, limit)
	if err != nil {
		drainBody(r)
		s.storeUnavailable(w, r, "queued jobs not counted", err)
		return false
	}
	if full {
		drainBody(r)
		writeError(w, newError(codeUnavailable, msgQueueFull).withRetryAfter(queueFullRetryAfter))
		return false
	}
	return true
}

// submitSlots bounds the submissions this process reads and prepares at once
// (limits.max_submits_in_flight), and one application's share of them to half the slots,
// at least one, so one application's slow uploads cannot hold every slot for the read
// timeout and starve the others. The applications are bounded by configuration, so the
// per-application counts are too.
type submitSlots struct {
	mu            sync.Mutex
	total, perApp int
	used          int
	byApp         map[string]int
}

func newSubmitSlots(n int) *submitSlots {
	return &submitSlots{total: n, perApp: max(1, n/2), byApp: map[string]int{}}
}

// take holds a slot for app and returns its release, or reports false when every slot,
// or the application's share, is held.
func (p *submitSlots) take(app string) (release func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.used >= p.total || p.byApp[app] >= p.perApp {
		return nil, false
	}
	p.used++
	p.byApp[app]++
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.used--
		if p.byApp[app]--; p.byApp[app] <= 0 {
			delete(p.byApp, app)
		}
	}, true
}

// queuedCount is the number of queued jobs of all applications, read from the store at
// most every queuedCountTTL and counted up by this process's own submissions in between.
// Its zero value is ready. Under a flood the ceiling is soft by what other processes
// accept within one TTL.
type queuedCount struct {
	mu sync.Mutex
	n  int
	at time.Time // when n was read; zero means never
}

// atLeast reports whether limit or more jobs are queued, counting again when the cached
// count is older than queuedCountTTL.
func (q *queuedCount) atLeast(ctx context.Context, store job.Store, limit int) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.at.IsZero() || time.Since(q.at) >= queuedCountTTL {
		n, err := store.CountQueued(ctx)
		if err != nil {
			return false, err
		}
		q.n, q.at = n, time.Now()
	}
	return q.n >= limit, nil
}

// add counts one job this process queued since the last read.
func (q *queuedCount) add() {
	q.mu.Lock()
	q.n++
	q.mu.Unlock()
}

// readSubmission streams the multipart body, enforcing the part rules and the per-part
// limits while reading. It returns the refusal of the first violation.
func readSubmission(r *http.Request, limits config.Limits, requestLimit int64) (*submission, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, newError(codeBadRequest, msgNotMultipart)
	}
	sub := &submission{}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, readError(r, err, requestLimit)
		}
		name := p.FormName()
		slot, limit, err := sub.partSlot(p, name, limits)
		if err != nil {
			return nil, err
		}
		read, err := readPart(p, limit)
		switch {
		case errors.Is(err, errPartTooLarge) && (name == partRetention || name == partMaxAttempts || name == partJudge):
			return nil, optionError(name)
		case errors.Is(err, errPartTooLarge):
			return nil, newError(codePayloadTooLarge, msgPartTooLarge, name)
		case err != nil:
			return nil, readError(r, err, requestLimit)
		}
		if slot == nil {
			sub.pages = append(sub.pages, read)
			continue
		}
		if (name == partExtractorInstructions || name == partJudgeInstructions) && !utf8.Valid(read.data) {
			return nil, newError(codeBadRequest, msgPartNotUTF8, name)
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
			return nil, newError(codeBadRequest, msgPartMissing, required.name)
		}
	}
	return sub, nil
}

// partSlot applies the part rules to the part p, named name, before it is read: it
// returns the submission field the part goes to (nil for a page) and its size limit, or
// the refusal (an unknown or repeated part, a missing or wrong media type, a page too
// many).
func (sub *submission) partSlot(p *multipart.Part, name string, limits config.Limits) (**part, int64, error) {
	var slot **part
	var limit int64
	switch name {
	case partDocument:
		slot, limit = &sub.document, limits.DocumentBytes
		if p.Header.Get("Content-Type") == "" {
			return nil, 0, newError(codeBadRequest, msgPartNoMediaType, partDocument)
		}
	case partPage:
		if len(sub.pages) == limits.Pages {
			return nil, 0, newError(codePayloadTooLarge, msgTooManyPages, limits.Pages)
		}
		limit = limits.PageBytes
	case partExtractorInstructions, partJudgeInstructions:
		slot, limit = &sub.extractorInstructions, maxInstructionsBytes
		if name == partJudgeInstructions {
			slot = &sub.judgeInstructions
		}
		if !mediaTypeIn(p.Header.Get("Content-Type"), "text/plain", "text/plain", "text/markdown") {
			return nil, 0, newError(codeBadRequest, msgPartMediaType, name)
		}
	case partSchema:
		slot, limit = &sub.schema, maxSchemaBytes
		if !mediaTypeIn(p.Header.Get("Content-Type"), "", "application/schema+json", "application/json") {
			return nil, 0, newError(codeBadRequest, msgPartMediaType, name)
		}
	case partRetention:
		slot, limit = &sub.retention, maxOptionBytes
	case partMaxAttempts:
		slot, limit = &sub.maxAttempts, maxOptionBytes
	case partJudge:
		slot, limit = &sub.judge, maxJudgeBytes
		if !mediaTypeIn(p.Header.Get("Content-Type"), "text/plain", "text/plain") {
			return nil, 0, newError(codeBadRequest, msgPartMediaType, name)
		}
	default:
		return nil, 0, newError(codeBadRequest, msgUnknownPart)
	}
	if slot != nil && *slot != nil {
		return nil, 0, newError(codeBadRequest, msgPartRepeated, name)
	}
	return slot, limit, nil
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
	switch name {
	case partRetention:
		return newError(codeBadRequest, msgBadRetention)
	case partJudge:
		return newError(codeBadRequest, msgBadJudge)
	}
	return newError(codeBadRequest, msgBadMaxAttempts)
}

// judgePolicy is the job's effective judge policy (ADR 0009): the judge part when the
// application allows override, else the application's, else roles.judge.policy. The part
// is refused when the application does not allow override, whatever its value.
func (sub *submission) judgePolicy(app job.Application, cfg *config.Config) (config.JudgePolicy, error) {
	if sub.judge == nil {
		return appJudgePolicy(app, cfg), nil
	}
	if !app.JudgeAllowOverride {
		return "", newError(codeBadRequest, msgJudgeNotAllowed)
	}
	p, err := config.ParseJudgePolicy(strings.TrimSpace(string(sub.judge.data)))
	if err != nil {
		return "", optionError(partJudge)
	}
	return p, nil
}

// appJudgePolicy is the application's effective judge policy: its own, else the role's.
func appJudgePolicy(app job.Application, cfg *config.Config) config.JudgePolicy {
	if p, err := config.ParseJudgePolicy(app.JudgePolicy); err == nil {
		return p
	}
	if cfg == nil {
		return config.JudgeAlways
	}
	return cfg.RolePolicy()
}

// options reads retention (default when absent) and max_attempts (0, the
// application's limit, when absent; clamped to it otherwise).
func (sub *submission) options(app job.Application) (job.Retention, int, error) {
	retention := job.RetentionDefault
	if sub.retention != nil {
		switch v := strings.TrimSpace(string(sub.retention.data)); v {
		case string(job.RetentionNone), string(job.RetentionDefault):
			retention = job.Retention(v)
		default:
			return "", 0, optionError(partRetention)
		}
	}
	attempts := 0
	if sub.maxAttempts != nil {
		v := strings.TrimSpace(string(sub.maxAttempts.data))
		if v == "" || v[0] == '0' || len(v) > 9 || strings.Trim(v, "0123456789") != "" {
			return "", 0, optionError(partMaxAttempts)
		}
		attempts, _ = strconv.Atoi(v) // nine digits at most: no overflow
		if app.MaxAttempts > 0 {
			attempts = min(attempts, app.MaxAttempts)
		}
	}
	return retention, attempts, nil
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
	write(h, partJudge, sub.judge)
	write(h, partJudgeInstructions, sub.judgeInstructions)
	write(h, partMaxAttempts, sub.maxAttempts)
	for i := range sub.pages {
		write(h, partPage+"["+strconv.Itoa(i)+"]", &sub.pages[i])
	}
	write(h, partRetention, sub.retention)
	write(h, partSchema, sub.schema)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// selectRepresentation is extract.SelectRepresentation with the application's HTML and
// PDF policies: a PDF no group reads natively is opened here with engine, and a PDF that
// cannot be prepared is refused (415). The worker selects again with the same policies.
func selectRepresentation(ctx context.Context, cfg *config.Config, app job.Application, doc *document.Document, engine document.PDFEngine) (document.Representation, error) {
	policy, err := document.ResolveHTMLPolicy(cfg.Documents.HTML, app.HTMLPolicy)
	if err != nil {
		return document.Representation{}, err
	}
	rep, _, err := extract.SelectRepresentation(ctx, cfg, doc, policy, cfg.PDFModeOf(app.ID), engine)
	return rep, err
}

// refuseDocument answers a refused document (*document.Error) with its contract code
// and a fixed message; any other error is a configuration problem and answers 500.
func (s *server) refuseDocument(w http.ResponseWriter, r *http.Request, err error) {
	var de *document.Error
	if errors.Is(err, pdf.ErrBusy) {
		// Every pdfium instance stayed busy for the engine's wait: nothing is wrong with
		// the document or the configuration.
		writeError(w, newError(codeUnavailable, msgPDFBusy).withRetryAfter(pdfBusyRetryAfter))
		return
	}
	if !errors.As(err, &de) {
		s.log.LogAttrs(r.Context(), slog.LevelError, "document configuration not usable",
			slog.String("request_id", requestIDFrom(r.Context())))
		writeError(w, newError(codeInternal, msgSubmitConfigIncomplete))
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
		if errors.Is(err, pdf.ErrTooManyPages) {
			e = newError(codeUnsupportedDocument, msgPDFTooManyPages, s.deps.Config.Documents.PDF.MaxPages)
		}
	}
	writeError(w, e)
}

// replay answers a submission whose Idempotency-Key the application used before
// (answerReplay) and reports true; it reports false, having written nothing, when the
// key is unused.
func (s *server) replay(w http.ResponseWriter, r *http.Request, app job.Application, key, bodyHash string) bool {
	existing, found := s.findIdempotent(w, r, app, key)
	if found {
		s.answerReplay(w, r, *existing, bodyHash)
	}
	return existing == nil || found
}

// findIdempotent looks up the application's job with the Idempotency-Key. It returns the
// job and true when there is one, a non-nil empty job and false when the key is unused,
// and nil after answering 503 when the store fails.
func (s *server) findIdempotent(w http.ResponseWriter, r *http.Request, app job.Application, key string) (*job.Job, bool) {
	ctx := r.Context()
	existing, err := s.deps.Store.FindByIdempotencyKey(ctx, app.ID, key)
	switch {
	case errors.Is(err, job.ErrNotFound):
		return &job.Job{}, false
	case err != nil:
		s.storeUnavailable(w, r, "idempotency lookup failed", err)
		return nil, false
	}
	return &existing, true
}

// answerReplay answers the replay of an existing job: 202 with the job as it is now and
// Idempotency-Replayed: true when the body hash matches, 422 idempotency_mismatch when it
// does not (replayResponse has the forms). A replay deletes nothing: a terminal
// retention: none job not read yet answers without its content, so the first GET
// /v1/jobs/{id} still delivers the result and deletes it (contract §10), which is the
// client's path after a poll timeout and a resubmission with the same key (§13). A replay
// charges no quota and runs nothing.
func (s *server) answerReplay(w http.ResponseWriter, r *http.Request, existing job.Job, bodyHash string) {
	if existing.BodyHash != bodyHash {
		writeError(w, newError(codeIdempotencyMismatch, msgIdempotencyMismatch))
		return
	}
	w.Header().Set(headerIdempotencyReplayed, "true")
	w.Header().Set("Location", "/v1/jobs/"+existing.ID)
	writeJSON(w, http.StatusAccepted, replayResponse(existing, s.now()))
}

// limitSubmit applies the submission limits that cost no work on the document, in
// order: submissions per minute, jobs in progress, and a read-only look at the quota, so
// a spent quota is refused before the document url fetch, the schema and the PDF engine
// (chargeSubmit charges it after them). It answers 429 (or 503 when the store fails) and
// reports false when one refuses.
func (s *server) limitSubmit(w http.ResponseWriter, r *http.Request, app job.Application) bool {
	ctx := r.Context()
	now := s.now()
	if wait, ok := s.takeRate(ctx, app.ID, bucketSubmits, app.SubmitsPerMinute, now); !ok {
		writeError(w, newError(codeRateLimited, msgSubmitRateLimited).withRetryAfter(wait))
		return false
	}
	if app.MaxInProgress > 0 { // queued and running: the ceiling holds before the workers lease
		inProgress, err := s.deps.Store.CountInProgress(ctx, app.ID)
		if err != nil {
			s.storeUnavailable(w, r, "jobs in progress not counted", err)
			return false
		}
		if inProgress >= app.MaxInProgress {
			writeError(w, newError(codeRateLimited, msgTooManyInProgress, app.MaxInProgress).
				withRetryAfter(inProgressRetryAfter))
			return false
		}
	}
	q, err := s.currentQuota(ctx, app)
	if err != nil {
		s.storeUnavailable(w, r, "quota not readable", err)
		return false
	}
	if q.Remaining <= 0 {
		s.setQuotaHeaders(w.Header(), app, q)
		writeError(w, newError(codeQuotaExhausted, msgQuotaExhausted, q.Limit).withRetryAfter(untilReset(now, q.Reset)))
		return false
	}
	return true
}

// storeUnavailable logs a store failure as what and answers 503 unavailable: the client
// may submit again.
func (s *server) storeUnavailable(w http.ResponseWriter, r *http.Request, what string, err error) {
	ctx := r.Context()
	s.log.LogAttrs(ctx, slog.LevelError, what,
		slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
	writeError(w, newError(codeUnavailable, msgStorageFailed))
}

// drainBody reads what is left of the request body, up to the request limit, so the
// client sees the status of a refusal instead of a reset connection.
func drainBody(r *http.Request) { _, _ = io.Copy(io.Discard, r.Body) }

// chargeSubmit charges the quota and updates X-Quota-Remaining, or answers 429 (or 503
// when the store fails) and reports false.
func (s *server) chargeSubmit(w http.ResponseWriter, r *http.Request, app job.Application) bool {
	ctx := r.Context()
	now := s.now()
	q, err := s.currentQuota(ctx, app)
	if err != nil {
		s.storeUnavailable(w, r, "quota not readable", err)
		return false
	}
	q, err = s.chargeQuota(ctx, app, q)
	if errors.Is(err, job.ErrQuotaExhausted) {
		q.Remaining = 0
		s.setQuotaHeaders(w.Header(), app, q)
		writeError(w, newError(codeQuotaExhausted, msgQuotaExhausted, q.Limit).withRetryAfter(untilReset(now, q.Reset)))
		return false
	}
	if err != nil {
		s.storeUnavailable(w, r, "quota not charged", err)
		return false
	}
	s.setQuotaHeaders(w.Header(), app, q)
	return true
}

// storeJob writes the document and page blobs, then the row. The blobs are read from the
// bytes in place, without a copy. On any failure it removes
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
	if err := s.deps.Blobs.Put(ctx, j.ID, blob.Document, bytes.NewReader(doc.Data)); err != nil {
		return err
	}
	for i, p := range doc.Pages {
		if err := s.deps.Blobs.Put(ctx, j.ID, blob.Page(i+1), bytes.NewReader(p.Data)); err != nil {
			return err
		}
	}
	return s.deps.Store.CreateJob(ctx, j)
}

// serviceVersion is the service_version of the application's jobs under its own judge
// policy.
func (s *server) serviceVersion(app job.Application) string {
	return s.serviceVersionFor(app, appJudgePolicy(app, s.deps.Config))
}

// serviceVersionFor is the service_version of the application's jobs run under policy.
func (s *server) serviceVersionFor(app job.Application, policy config.JudgePolicy) string {
	if s.deps.AppServiceVersion != nil {
		if v := s.deps.AppServiceVersion(app, policy); v != "" {
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
