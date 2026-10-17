package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
	"github.com/kaanemec/vde/internal/worker"
)

// The roles read text and page images but not PDF natively, so a PDF needs pages.
const submitTestConfig = `
endpoints:
  ext:
    provider: fake
    models: {ext-model: {accepts: [text/plain, image/png, image/jpeg]}}
  rev:
    provider: fake
    models: {judge-model: {accepts: [text/plain, image/png, image/jpeg]}}
groups:
  extraction:
    endpoints: [{endpoint: ext, model: ext-model}]
    failover: {max_tries: 1, max_total_time: 1m}
  review:
    endpoints: [{endpoint: rev, model: judge-model}]
    failover: {max_tries: 1, max_total_time: 1m}
roles:
  extractor: {group: extraction}
  judge: {group: review}
limits: {max_attempts: 3, job_timeout: 5m}
applications:
  - {id: travelinbox-test, name: Test, rate: {max_in_progress: 2}}
`

// Content sentinels: none may appear in logs or error bodies.
const (
	submitSentinel     = "SENTINEL"
	submitDocText      = "SENTINEL-DOCUMENT: Ada Lovelace, booking LX42QZ"
	submitInstructions = "SENTINEL-INSTRUCTIONS: extract the name."
	submitSchema       = `{"type":"object","required":["name"],"properties":{"name":{"type":"string","description":"SENTINEL-SCHEMA"}}}`
	submitCandidate    = `{"name":"Ada Lovelace"}`
)

type submitLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *submitLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *submitLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// submitClock is a settable clock; zero means the real one.
type submitClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *submitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.t.IsZero() {
		return time.Now()
	}
	return c.t
}

func (c *submitClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type submitRigOptions struct {
	app       func(*job.Application) // edits the application row
	cfg       func(*config.Config)   // edits the configuration after validation
	wrapStore func(job.Store) job.Store
	now       time.Time // fixed clock; zero is the real clock
	// pool runs a worker pool on fake providers answering ext and judge (an accepted
	// candidate when nil). It needs the real clock.
	pool       bool
	ext, judge fake.Script
	// notify wires a worker.Notifier between the pool and POST /v1/extract.
	notify bool
	// appVersion is Deps.AppServiceVersion; nil leaves it unset.
	appVersion func(job.Application, config.JudgePolicy) string
	// pdf is the PDF engine of submit and the pool (Deps.PDF, worker.Options.PDF).
	pdf document.PDFEngine
}

type submitRig struct {
	s          *server
	h          http.Handler
	db         *job.SQLite
	blobsDir   string
	cfg        *config.Config
	clock      *submitClock
	logs       *submitLog
	token      string
	ext, judge *fake.Provider
}

func newSubmitRig(t *testing.T, o submitRigOptions) *submitRig {
	t.Helper()
	cfg, err := config.Parse([]byte(submitTestConfig), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate().Err(false); err != nil {
		t.Fatal(err)
	}
	if o.cfg != nil {
		o.cfg(cfg)
	}
	dir := t.TempDir()
	db, err := job.Open(filepath.Join(dir, "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	app := job.Application{ID: testApp, Name: "Test", MaxAttempts: 3, QuotaLimit: 100, QuotaWindow: "P1D",
		RequestsPerMinute: 300, SubmitsPerMinute: 30, MaxInProgress: 20, RetentionDefault: "24h"}
	if o.app != nil {
		o.app(&app)
	}
	if err := db.UpsertApplication(t.Context(), app, time.Now()); err != nil {
		t.Fatal(err)
	}
	blobsDir := filepath.Join(dir, "blobs")
	blobs, err := blob.NewFS(blobsDir)
	if err != nil {
		t.Fatal(err)
	}
	rg := &submitRig{db: db, blobsDir: blobsDir, cfg: cfg, clock: &submitClock{t: o.now}, logs: &submitLog{}}
	var store job.Store = db
	if o.wrapStore != nil {
		store = o.wrapStore(db)
	}
	logger := slog.New(slog.NewTextHandler(rg.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	deps := Deps{
		Config: cfg, Store: store, ServiceVersion: testServiceVersion, Logger: logger, Now: rg.clock.Now,
		Extractor: fakeHealth(true), Judge: fakeHealth(true), Blobs: blobs,
		AppServiceVersion: o.appVersion, PDF: o.pdf,
	}
	if o.pool {
		if o.ext == nil {
			o.ext = fake.Script{{Answer: submitCandidate, Repeat: true}}
		}
		if o.judge == nil {
			o.judge = fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}
		}
		rg.ext, rg.judge = fake.New(o.ext), fake.New(o.judge)
		providers := map[*config.Endpoint]provider.Provider{cfg.Endpoints["ext"]: rg.ext, cfg.Endpoints["rev"]: rg.judge}
		roles, err := extract.NewRoles(cfg, func(e *config.Endpoint) (provider.Provider, error) { return providers[e], nil })
		if err != nil {
			t.Fatal(err)
		}
		popts := worker.Options{Size: 2, PollInterval: 20 * time.Millisecond, Logger: logger, PDF: o.pdf}
		if o.notify {
			n := worker.NewNotifier()
			deps.Completions = n
			popts.OnFinish = func(j job.Job) { n.Notify(j.ID) }
		}
		pool := worker.New(db, blobs, cfg, roles, popts)
		pool.Start(t.Context())
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = pool.Stop(ctx)
		})
		deps.Wake = pool.Wake
	}
	rg.s = newServer(deps)
	rg.h = handlerOf(rg.s)
	rg.token = issueToken(t, db, testApp, []string{auth.ScopeJobs}, time.Now().Add(5*365*24*time.Hour))
	t.Cleanup(func() {
		if logs := rg.logs.String(); strings.Contains(logs, submitSentinel) {
			t.Errorf("content in the logs:\n%s", logs)
		}
	})
	return rg
}

// mpart is one multipart part of a test request.
type mpart struct {
	name, ctype, filename string
	data                  []byte
}

func validParts() []mpart {
	return []mpart{
		{name: partDocument, ctype: "text/plain; charset=utf-8", filename: "booking.txt", data: []byte(submitDocText)},
		{name: partExtractorInstructions, ctype: "text/markdown", data: []byte(submitInstructions)},
		{name: partJudgeInstructions, ctype: "text/markdown", data: []byte(submitInstructions)},
		{name: partSchema, ctype: "application/schema+json", data: []byte(submitSchema)},
	}
}

// withPart returns parts with p replacing the part of the same name, or added.
func withPart(parts []mpart, p mpart) []mpart {
	out := make([]mpart, 0, len(parts)+1)
	replaced := false
	for _, q := range parts {
		if q.name == p.name && !replaced {
			out, replaced = append(out, p), true
			continue
		}
		out = append(out, q)
	}
	if !replaced {
		out = append(out, p)
	}
	return out
}

func withoutPart(parts []mpart, name string) []mpart {
	var out []mpart
	for _, q := range parts {
		if q.name != name {
			out = append(out, q)
		}
	}
	return out
}

func multipartBody(t *testing.T, parts []mpart) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		disposition := fmt.Sprintf(`form-data; name=%q`, p.name)
		if p.filename != "" {
			disposition += fmt.Sprintf(`; filename=%q`, p.filename)
		}
		h.Set("Content-Disposition", disposition)
		if p.ctype != "" {
			h.Set("Content-Type", p.ctype)
		}
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(p.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), mw.FormDataContentType()
}

// post sends a raw POST /v1/jobs with the rig's token.
func (rg *submitRig) post(body []byte, contentType string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Authorization", "Bearer "+rg.token)
	r.Header.Set("Content-Type", contentType)
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	rg.h.ServeHTTP(w, r)
	return w
}

func (rg *submitRig) submit(t *testing.T, parts []mpart, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, parts)
	return rg.post(body, ct, mod)
}

func (rg *submitRig) get(target string) *httptest.ResponseRecorder {
	return serve(rg.h, http.MethodGet, target, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+rg.token) })
}

func idempotencyKey(key string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(headerIdempotencyKey, key) }
}

// quotaUsed is the application's charged submissions in the window containing the
// rig's now.
func (rg *submitRig) quotaUsed(t *testing.T) int {
	t.Helper()
	start, _, err := quotaWindow(rg.clock.Now(), "P1D")
	if err != nil {
		t.Fatal(err)
	}
	n, err := rg.db.GetQuota(t.Context(), testApp, start)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// blobJobs counts the job directories in the blob store.
func (rg *submitRig) blobJobs(t *testing.T) int {
	t.Helper()
	jobs := map[string]bool{}
	err := filepath.WalkDir(rg.blobsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			jobs[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(jobs)
}

func (rg *submitRig) providerCalls() int {
	if rg.ext == nil {
		return 0
	}
	return len(rg.ext.Calls()) + len(rg.judge.Calls())
}

func (rg *submitRig) waitTerminal(t *testing.T, id string) job.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := rg.db.GetJobAny(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status.Terminal() {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s not terminal in time", id)
	return job.Job{}
}

// decodeJob checks a 202 and returns the job body.
func decodeJob(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("job body %q: %v", w.Body.String(), err)
	}
	if loc, id := w.Header().Get("Location"), body["id"]; loc != fmt.Sprintf("/v1/jobs/%v", id) {
		t.Errorf("Location = %q, want /v1/jobs/%v", loc, id)
	}
	return body
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const pdfBytes = "%PDF-1.7\n1 0 obj << >> endobj\ntrailer << >>\n%%EOF\n"

var rfc3339UTC = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

// TestSubmitQueuedJob checks the 202 body against the contract §15 example of a queued
// job and the stored row.
func TestSubmitQueuedJob(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{})
	w := rg.submit(t, validParts(), nil)
	body := decodeJob(t, w)

	var example map[string]any
	contractExample := `{"id": "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB", "status": "queued", "result": null, "feedback": [], "attempts": 0, "service_version": "vde-1.4.0+c0ffee12", "error": null, "created_at": "2026-10-16T09:12:03Z", "finished_at": null, "expires_at": "2026-10-17T09:12:03Z"}`
	if err := json.Unmarshal([]byte(contractExample), &example); err != nil {
		t.Fatal(err)
	}
	for k := range example {
		if _, ok := body[k]; !ok {
			t.Errorf("field %s missing", k)
		}
	}
	for k := range body {
		if _, ok := example[k]; !ok && k != "judge_confidence" && k != "verification" {
			t.Errorf("unexpected field %s (attachments are only for email)", k)
		}
	}
	for _, k := range []string{"status", "result", "attempts", "error", "finished_at"} {
		if fmt.Sprint(body[k]) != fmt.Sprint(example[k]) {
			t.Errorf("%s = %v, want %v", k, body[k], example[k])
		}
	}
	if fb, ok := body["feedback"].([]any); !ok || len(fb) != 0 {
		t.Errorf("feedback = %#v, want []", body["feedback"])
	}
	if body["judge_confidence"] != nil {
		t.Errorf("judge_confidence = %v, want null", body["judge_confidence"])
	}
	if body["service_version"] != testServiceVersion {
		t.Errorf("service_version = %v", body["service_version"])
	}
	id, _ := body["id"].(string)
	if !strings.HasPrefix(id, "j_") {
		t.Errorf("id = %q", id)
	}
	created, _ := body["created_at"].(string)
	expires, _ := body["expires_at"].(string)
	if !rfc3339UTC.MatchString(created) || !rfc3339UTC.MatchString(expires) {
		t.Fatalf("timestamps %q %q are not RFC 3339 UTC seconds", created, expires)
	}
	c, _ := time.Parse(time.RFC3339, created)
	e, _ := time.Parse(time.RFC3339, expires)
	if e.Sub(c) != 24*time.Hour {
		t.Errorf("expires_at - created_at = %s, want the application's 24h", e.Sub(c))
	}
	if w.Header().Get(headerIdempotencyReplayed) != "" {
		t.Error("a first submission is not a replay")
	}
	if got := w.Header().Get(headerQuotaRemaining); got != "99" {
		t.Errorf("X-Quota-Remaining = %q, want 99 after charging", got)
	}

	j, err := rg.db.GetJob(t.Context(), testApp, id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Representation != "text" || j.MediaType != "text/plain; charset=utf-8" || j.Size != int64(len(submitDocText)) ||
		j.ExtractorInstructions != submitInstructions || j.Schema != submitSchema || j.MaxAttempts != 0 ||
		j.Retention != job.RetentionDefault || j.BodyHash == "" || j.ServiceVersion != testServiceVersion {
		t.Errorf("stored job = %+v", j)
	}
	if rg.blobJobs(t) != 1 || rg.quotaUsed(t) != 1 {
		t.Errorf("blob jobs %d, quota used %d; want 1 and 1", rg.blobJobs(t), rg.quotaUsed(t))
	}
}

// email is a message/rfc822 document with one PDF attachment, ticket.pdf.
const email = "From: a@example.com\r\nTo: b@example.com\r\nSubject: Booking\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b1\"\r\n\r\n" +
	"--b1\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + submitDocText + "\r\n" +
	"--b1\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"ticket.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjcK\r\n--b1--\r\n"

func TestSubmitAccepted(t *testing.T) {
	t.Parallel()
	png := pngBytes(t)
	tests := []struct {
		name  string
		parts func() []mpart
		check func(t *testing.T, j job.Job, body map[string]any)
	}{
		{
			name: "PDF with pages reads the pages",
			parts: func() []mpart {
				p := withPart(validParts(), mpart{name: partDocument, ctype: "application/pdf", data: []byte(pdfBytes)})
				return append(p,
					mpart{name: partPage, ctype: "image/png", data: png},
					mpart{name: partPage, ctype: "image/jpeg", data: jpegBytes(t)})
			},
			check: func(t *testing.T, j job.Job, _ map[string]any) {
				if j.Representation != "pages" || j.PageCount != 2 || j.MediaType != "application/pdf" {
					t.Errorf("job = %s %d %s, want pages 2 application/pdf", j.Representation, j.PageCount, j.MediaType)
				}
			},
		},
		{
			name: "PNG image",
			parts: func() []mpart {
				return withPart(validParts(), mpart{name: partDocument, ctype: "image/png", data: png})
			},
			check: func(t *testing.T, j job.Job, _ map[string]any) {
				if j.Representation != "native" {
					t.Errorf("representation = %s, want native", j.Representation)
				}
			},
		},
		{
			name:  "schema-less",
			parts: func() []mpart { return withoutPart(validParts(), partSchema) },
			check: func(t *testing.T, j job.Job, _ map[string]any) {
				if j.Schema != "" {
					t.Errorf("schema = %q, want schema-less", j.Schema)
				}
			},
		},
		{
			name: "retention none and max_attempts clamped",
			parts: func() []mpart {
				return append(validParts(),
					mpart{name: partRetention, ctype: "text/plain", data: []byte("none")},
					mpart{name: partMaxAttempts, ctype: "text/plain", data: []byte("9")})
			},
			check: func(t *testing.T, j job.Job, _ map[string]any) {
				if j.Retention != job.RetentionNone || j.MaxAttempts != 3 {
					t.Errorf("retention %s max_attempts %d, want none 3", j.Retention, j.MaxAttempts)
				}
			},
		},
		{
			name: "max_attempts lowers the limit",
			parts: func() []mpart {
				return append(validParts(), mpart{name: partMaxAttempts, data: []byte("2\n")})
			},
			check: func(t *testing.T, j job.Job, _ map[string]any) {
				if j.MaxAttempts != 2 {
					t.Errorf("max_attempts %d, want 2", j.MaxAttempts)
				}
			},
		},
		{
			name: "email lists its attachments",
			parts: func() []mpart {
				return withPart(validParts(), mpart{name: partDocument, ctype: "message/rfc822", data: []byte(email)})
			},
			check: func(t *testing.T, j job.Job, body map[string]any) {
				atts, _ := body["attachments"].([]any)
				if len(atts) != 1 {
					t.Fatalf("attachments = %v, want one", body["attachments"])
				}
				a, _ := atts[0].(map[string]any)
				if a["name"] != "ticket.pdf" || a["media_type"] != "application/pdf" || a["bytes"] == float64(0) {
					t.Errorf("attachment = %v", a)
				}
				if j.Representation != "text" || j.MediaType != "message/rfc822" {
					t.Errorf("job = %s %s", j.Representation, j.MediaType)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rg := newSubmitRig(t, submitRigOptions{})
			body := decodeJob(t, rg.submit(t, tt.parts(), nil))
			j, err := rg.db.GetJob(t.Context(), testApp, body["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, j, body)
		})
	}
}

// TestSubmitRefusals covers the contract §4 response table: every refusal answers its
// code with the quota headers, stores nothing, charges nothing and calls no model.
func TestSubmitRefusals(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{pool: true, cfg: func(c *config.Config) {
		c.Limits.DocumentBytes, c.Limits.PageBytes, c.Limits.Pages, c.Limits.RequestBytes = 4<<10, 2<<10, 3, 1<<20
	}})
	png := pngBytes(t)
	page := mpart{name: partPage, ctype: "image/png", data: png}
	valid, ct := multipartBody(t, validParts())
	type request struct {
		parts       []mpart
		body        []byte // raw body instead of parts
		contentType string
		mod         func(*http.Request)
	}
	tests := []struct {
		name   string
		req    request
		status int
		code   string
	}{
		{"not multipart", request{body: []byte(`{}`), contentType: "application/json"}, 400, codeBadRequest},
		{"no boundary", request{body: valid, contentType: "multipart/form-data"}, 400, codeBadRequest},
		{"truncated multipart", request{body: valid[:len(valid)-20], contentType: ct}, 400, codeBadRequest},
		{"document missing", request{parts: withoutPart(validParts(), partDocument)}, 400, codeBadRequest},
		{"document repeated", request{parts: append(validParts(), validParts()[0])}, 400, codeBadRequest},
		{"document without media type", request{parts: withPart(validParts(),
			mpart{name: partDocument, data: []byte(submitDocText)})}, 400, codeBadRequest},
		{"document empty", request{parts: withPart(validParts(),
			mpart{name: partDocument, ctype: "text/plain"})}, 400, codeBadRequest},
		{"extractor_instructions missing", request{parts: withoutPart(validParts(), partExtractorInstructions)}, 400, codeBadRequest},
		{"judge_instructions repeated", request{parts: append(validParts(), validParts()[2])}, 400, codeBadRequest},
		{"schema repeated", request{parts: append(validParts(), validParts()[3])}, 400, codeBadRequest},
		{"instructions as JSON", request{parts: withPart(validParts(),
			mpart{name: partJudgeInstructions, ctype: "application/json", data: []byte(`"x"`)})}, 400, codeBadRequest},
		{"instructions not UTF-8", request{parts: withPart(validParts(),
			mpart{name: partJudgeInstructions, ctype: "text/plain", data: []byte{0xff, 0xfe, 'x'}})}, 400, codeBadRequest},
		{"schema as text", request{parts: withPart(validParts(),
			mpart{name: partSchema, ctype: "text/plain", data: []byte(submitSchema)})}, 400, codeBadRequest},
		{"unknown part", request{parts: append(validParts(), mpart{name: "comment", data: []byte("hi")})}, 400, codeBadRequest},
		{"unnamed part", request{parts: append(validParts(), mpart{data: []byte("hi")})}, 400, codeBadRequest},
		{"retention unknown", request{parts: append(validParts(), mpart{name: partRetention, data: []byte("forever")})}, 400, codeBadRequest},
		{"retention repeated", request{parts: append(validParts(), mpart{name: partRetention, data: []byte("none")},
			mpart{name: partRetention, data: []byte("none")})}, 400, codeBadRequest},
		{"max_attempts zero", request{parts: append(validParts(), mpart{name: partMaxAttempts, data: []byte("0")})}, 400, codeBadRequest},
		{"max_attempts negative", request{parts: append(validParts(), mpart{name: partMaxAttempts, data: []byte("-1")})}, 400, codeBadRequest},
		{"max_attempts text", request{parts: append(validParts(), mpart{name: partMaxAttempts, data: []byte("three")})}, 400, codeBadRequest},
		{"max_attempts huge", request{parts: append(validParts(),
			mpart{name: partMaxAttempts, data: []byte(strings.Repeat("9", 40))})}, 400, codeBadRequest},
		{"Idempotency-Key too long", request{parts: validParts(), mod: idempotencyKey(strings.Repeat("k", 129))}, 400, codeBadRequest},
		{"page empty", request{parts: append(validParts(), mpart{name: partPage, ctype: "image/png"})}, 400, codeBadRequest},

		{"document over its limit", request{parts: withPart(validParts(), mpart{name: partDocument, ctype: "text/plain",
			data: bytes.Repeat([]byte("a"), 4<<10+1)})}, 413, codePayloadTooLarge},
		{"page over its limit", request{parts: append(validParts(), mpart{name: partPage, ctype: "image/png",
			data: append(png, make([]byte, 2<<10)...)})}, 413, codePayloadTooLarge},
		{"too many pages", request{parts: append(validParts(), page, page, page, page)}, 413, codePayloadTooLarge},
		{"instructions over 64 KiB", request{parts: withPart(validParts(), mpart{name: partExtractorInstructions,
			ctype: "text/plain", data: bytes.Repeat([]byte("a"), 64<<10+1)})}, 413, codePayloadTooLarge},
		{"schema over 256 KiB", request{parts: withPart(validParts(), mpart{name: partSchema,
			ctype: "application/schema+json", data: bytes.Repeat([]byte(" "), 256<<10+1)})}, 413, codePayloadTooLarge},
		{"request over its limit", request{body: append(bytes.Repeat([]byte("preamble\r\n"), 1<<17), valid...), contentType: ct}, 413, codePayloadTooLarge},

		{"media type not accepted", request{parts: withPart(validParts(),
			mpart{name: partDocument, ctype: "application/zip", data: []byte("PK\x03\x04")})}, 415, codeUnsupportedMediaType},
		{"PNG declared as PDF", request{parts: withPart(validParts(),
			mpart{name: partDocument, ctype: "application/pdf", data: png})}, 415, codeUnsupportedMediaType},
		{"page declared PNG is JPEG", request{parts: append(validParts(),
			mpart{name: partPage, ctype: "image/png", data: jpegBytes(t)})}, 415, codeUnsupportedMediaType},
		{"PDF without pages and no native reader", request{parts: withPart(validParts(),
			mpart{name: partDocument, ctype: "application/pdf", data: []byte(pdfBytes)})}, 415, codeUnsupportedDocument},

		{"schema not JSON", request{parts: withPart(validParts(),
			mpart{name: partSchema, ctype: "application/schema+json", data: []byte("not json SENTINEL")})}, 422, codeInvalidSchema},
		{"schema not a JSON Schema", request{parts: withPart(validParts(),
			mpart{name: partSchema, ctype: "application/schema+json", data: []byte(`{"type": 12}`)})}, 422, codeInvalidSchema},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, contentType := tt.req.body, tt.req.contentType
			if tt.req.parts != nil {
				body, contentType = multipartBody(t, tt.req.parts)
			}
			w := rg.post(body, contentType, tt.req.mod)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tt.status, w.Body.String())
			}
			if e := decodeError(t, w); e.Code != tt.code {
				t.Errorf("code = %q, want %q", e.Code, tt.code)
			}
			if strings.Contains(w.Body.String(), submitSentinel) {
				t.Errorf("content in the error body: %s", w.Body.String())
			}
			if w.Header().Get(headerQuotaRemaining) != "100" || w.Header().Get(headerQuotaReset) == "" {
				t.Errorf("quota headers = %q %q, want 100 and a reset", w.Header().Get(headerQuotaRemaining),
					w.Header().Get(headerQuotaReset))
			}
		})
	}
	if n := rg.blobJobs(t); n != 0 {
		t.Errorf("%d jobs have blobs after refusals, want none", n)
	}
	if n := rg.quotaUsed(t); n != 0 {
		t.Errorf("quota used %d after refusals, want 0", n)
	}
	if n := rg.providerCalls(); n != 0 {
		t.Errorf("provider calls after refusals = %d, want 0", n)
	}
	// The counter works: an accepted job calls the extractor and the judge once each.
	id := decodeJob(t, rg.submit(t, validParts(), nil))["id"].(string)
	if j := rg.waitTerminal(t, id); j.Status != job.StatusAccepted {
		t.Fatalf("status = %s, want accepted", j.Status)
	}
	if n := rg.providerCalls(); n != 2 {
		t.Errorf("provider calls = %d, want 2", n)
	}
}

// TestSubmitOversizedOverTCP sends a 130 MiB body to the default 128 MiB limit from a
// real TCP client and expects the 413 JSON body, not a reset connection.
func TestSubmitOversizedOverTCP(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{})
	srv := httptest.NewServer(rg.h)
	t.Cleanup(srv.Close)

	const pages, pageSize = 20, 130 << 20 / 20
	writeBody := func(w io.Writer) (string, error) {
		mw := multipart.NewWriter(w)
		if err := mw.SetBoundary("vde-test-boundary"); err != nil {
			return "", err
		}
		for _, p := range validParts() {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, p.name))
			h.Set("Content-Type", p.ctype)
			pw, err := mw.CreatePart(h)
			if err != nil {
				return "", err
			}
			if _, err := pw.Write(p.data); err != nil {
				return "", err
			}
		}
		chunk := make([]byte, 1<<20)
		copy(chunk, "\x89PNG\r\n\x1a\n")
		for range pages {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", `form-data; name="page"`)
			h.Set("Content-Type", "image/png")
			pw, err := mw.CreatePart(h)
			if err != nil {
				return "", err
			}
			for written := 0; written < pageSize; written += len(chunk) {
				if _, err := pw.Write(chunk[:min(len(chunk), pageSize-written)]); err != nil {
					return "", err
				}
			}
		}
		return mw.FormDataContentType(), mw.Close()
	}
	var counter countWriter
	contentType, err := writeBody(&counter)
	if err != nil {
		t.Fatal(err)
	}
	if counter.n <= 128<<20 {
		t.Fatalf("body is %d bytes, want over 128 MiB", counter.n)
	}

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	header := fmt.Sprintf("POST /v1/jobs HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n",
		srv.Listener.Addr(), rg.token, contentType, counter.n)
	var wg sync.WaitGroup
	wg.Go(func() {
		bw := bufio.NewWriterSize(conn, 1<<16)
		if _, err := bw.WriteString(header); err != nil {
			return
		}
		if _, err := writeBody(bw); err != nil {
			return // the server may close once it answered; the response is what counts
		}
		_ = bw.Flush()
	})
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("reading the response: %v (a reset instead of a 413?)", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the 413 body: %v", err)
	}
	_ = conn.Close()
	wg.Wait()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body %s", resp.StatusCode, raw)
	}
	var body errorBody
	if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code != codePayloadTooLarge {
		t.Errorf("body %s: want a payload_too_large error (%v)", raw, err)
	}
	if resp.Header.Get(headerQuotaRemaining) == "" {
		t.Error("413 without quota headers")
	}
	if rg.blobJobs(t) != 0 || rg.quotaUsed(t) != 0 {
		t.Error("an oversized request stored or charged something")
	}
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

func TestSubmitWithoutBlobStore(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t, false)
	token := testToken(t, s, auth.ScopeJobs)
	body, ct := multipartBody(t, validParts())
	w := serve(handlerOf(s), http.MethodPost, "/v1/jobs", func(r *http.Request) {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", ct)
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if w.Code != http.StatusServiceUnavailable || decodeError(t, w).Code != codeUnavailable {
		t.Errorf("status = %d, want 503 unavailable", w.Code)
	}
}

// failingBlobs fails the second Put, after one blob was written.
type failingBlobs struct {
	blob.Store
	puts    atomic.Int32
	deleted atomic.Int32
}

func (f *failingBlobs) Put(ctx context.Context, jobID, name string, r io.Reader) error {
	if f.puts.Add(1) == 2 {
		return errors.New("disk full")
	}
	return f.Store.Put(ctx, jobID, name, r)
}

func (f *failingBlobs) DeleteAll(ctx context.Context, jobID string) error {
	f.deleted.Add(1)
	return f.Store.DeleteAll(ctx, jobID)
}

// failingCreate fails CreateJob.
type failingCreate struct{ job.Store }

func (failingCreate) CreateJob(context.Context, *job.Job) error {
	return errors.New("database is locked")
}

func TestSubmitStorageFailure(t *testing.T) {
	t.Parallel()
	pdfWithPage := append(withPart(validParts(), mpart{name: partDocument, ctype: "application/pdf", data: []byte(pdfBytes)}),
		mpart{name: partPage, ctype: "image/png", data: pngBytes(t)})
	t.Run("blob write fails", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{})
		fb := &failingBlobs{Store: rg.s.deps.Blobs}
		rg.s.deps.Blobs = fb
		w := rg.submit(t, pdfWithPage, nil)
		if w.Code != http.StatusServiceUnavailable || decodeError(t, w).Code != codeUnavailable {
			t.Fatalf("status = %d, want 503 unavailable", w.Code)
		}
		if fb.deleted.Load() != 1 || rg.blobJobs(t) != 0 {
			t.Errorf("blobs not removed: DeleteAll calls %d, jobs with blobs %d", fb.deleted.Load(), rg.blobJobs(t))
		}
	})
	t.Run("row write fails", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{wrapStore: func(s job.Store) job.Store { return failingCreate{s} }})
		w := rg.submit(t, pdfWithPage, nil)
		if w.Code != http.StatusServiceUnavailable || decodeError(t, w).Code != codeUnavailable {
			t.Fatalf("status = %d, want 503 unavailable", w.Code)
		}
		if rg.blobJobs(t) != 0 {
			t.Error("blobs of the unstored job remain")
		}
	})
}

func TestSubmitIdempotency(t *testing.T) {
	t.Parallel()
	t.Run("replay returns the job as it is now and runs nothing", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{pool: true})
		first := decodeJob(t, rg.submit(t, validParts(), idempotencyKey("doc-42")))
		id := first["id"].(string)
		rg.waitTerminal(t, id)
		calls := rg.providerCalls()

		w := rg.submit(t, validParts(), idempotencyKey("doc-42")) // a new boundary, same parts
		again := decodeJob(t, w)
		if again["id"] != id || w.Header().Get(headerIdempotencyReplayed) != "true" {
			t.Errorf("replay = %v with Idempotency-Replayed %q, want %s and true", again["id"],
				w.Header().Get(headerIdempotencyReplayed), id)
		}
		if again["status"] != string(job.StatusAccepted) {
			t.Errorf("replayed status = %v, want the current accepted", again["status"])
		}
		if got := w.Header().Get(headerQuotaRemaining); got != "99" {
			t.Errorf("X-Quota-Remaining = %q, want 99: a replay is not charged", got)
		}
		time.Sleep(100 * time.Millisecond) // a wrongly created job would be leased by now
		if rg.providerCalls() != calls || rg.quotaUsed(t) != 1 || rg.blobJobs(t) != 1 {
			t.Errorf("after replay: calls %d (was %d), quota %d, blob jobs %d", rg.providerCalls(), calls,
				rg.quotaUsed(t), rg.blobJobs(t))
		}

		mismatch := withPart(validParts(), mpart{name: partJudgeInstructions, ctype: "text/markdown", data: []byte("other")})
		w = rg.submit(t, mismatch, idempotencyKey("doc-42"))
		if w.Code != http.StatusUnprocessableEntity || decodeError(t, w).Code != codeIdempotencyMismatch {
			t.Errorf("mismatch: status %d body %s, want 422 idempotency_mismatch", w.Code, w.Body.String())
		}
		if rg.quotaUsed(t) != 1 || rg.providerCalls() != calls {
			t.Error("a mismatch charged quota or ran a model")
		}
		// Keys are per application and per key: another key is a new job.
		if other := decodeJob(t, rg.submit(t, validParts(), idempotencyKey("doc-43"))); other["id"] == id {
			t.Error("another key replayed the first job")
		}
	})
	t.Run("replay of a job whose content is gone names it without content", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{})
		ctx := t.Context()
		id := decodeJob(t, rg.submit(t, validParts(), idempotencyKey("gone")))["id"].(string)
		if _, err := rg.db.LeaseNextQueued(ctx, "w", time.Now(), time.Now().Add(time.Minute), nil); err != nil {
			t.Fatal(err)
		}
		err := rg.db.Finish(ctx, id, "w", job.Outcome{Status: job.StatusAccepted, Attempts: 1,
			Result: json.RawMessage(`{"name":"x"}`), Feedback: json.RawMessage(`[]`)}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		check := func(when string) {
			t.Helper()
			w := rg.submit(t, validParts(), idempotencyKey("gone"))
			body := decodeJob(t, w)
			if w.Code != http.StatusAccepted || w.Header().Get(headerIdempotencyReplayed) != "true" || body["id"] != id {
				t.Errorf("%s: %d replayed %q id %v, want 202 true %s", when, w.Code, w.Header().Get(headerIdempotencyReplayed), body["id"], id)
			}
			deleted, _ := body["content_deleted_at"].(string)
			if body["status"] != string(job.StatusAccepted) || body["result"] != nil || deleted == "" ||
				fmt.Sprint(body["feedback"]) != "[]" {
				t.Errorf("%s: body %s, want status accepted, result null, feedback [], content_deleted_at set", when, w.Body.String())
			}
		}
		if w := rg.submit(t, validParts(), idempotencyKey("gone")); strings.Contains(w.Body.String(), "content_deleted_at") {
			t.Errorf("a replay of a readable job carries content_deleted_at: %s", w.Body.String())
		}
		rg.clock.Set(time.Now().Add(25 * time.Hour)) // past expires_at, not swept yet
		check("expired")
		rg.clock.Set(time.Time{})
		if err := rg.db.MarkContentDeleted(ctx, id, job.ContentAll, time.Now()); err != nil {
			t.Fatal(err)
		}
		check("content deleted")
		if rg.quotaUsed(t) != 1 {
			t.Errorf("quota %d, want 1: replays are not charged", rg.quotaUsed(t))
		}
	})
	t.Run("concurrent identical submissions create one job", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{})
		const n = 8
		var wg sync.WaitGroup
		results := make([]*httptest.ResponseRecorder, n)
		for i := range n {
			body, ct := multipartBody(t, validParts())
			wg.Go(func() { results[i] = rg.post(body, ct, idempotencyKey("race")) })
		}
		wg.Wait()
		ids, replayed := map[any]bool{}, 0
		for _, w := range results {
			ids[decodeJob(t, w)["id"]] = true
			if w.Header().Get(headerIdempotencyReplayed) == "true" {
				replayed++
			}
		}
		if len(ids) != 1 || replayed != n-1 {
			t.Errorf("ids %v, replayed %d; want one id and %d replays", ids, replayed, n-1)
		}
		if rg.quotaUsed(t) != 1 || rg.blobJobs(t) != 1 {
			t.Errorf("quota %d, blob jobs %d; want 1 and 1", rg.quotaUsed(t), rg.blobJobs(t))
		}
	})
	t.Run("insert race with another process replays", func(t *testing.T) {
		t.Parallel()
		var hide atomic.Bool
		rg := newSubmitRig(t, submitRigOptions{wrapStore: func(s job.Store) job.Store { return &racingStore{Store: s, hide: &hide} }})
		id := decodeJob(t, rg.submit(t, validParts(), idempotencyKey("k")))["id"]

		hide.Store(true) // the lookup misses the row, as if another process inserted it after it
		w := rg.submit(t, validParts(), idempotencyKey("k"))
		if body := decodeJob(t, w); body["id"] != id || w.Header().Get(headerIdempotencyReplayed) != "true" {
			t.Errorf("race replay = %v replayed %q, want %v true", body["id"], w.Header().Get(headerIdempotencyReplayed), id)
		}
		hide.Store(true)
		w = rg.submit(t, withoutPart(validParts(), partSchema), idempotencyKey("k"))
		if w.Code != http.StatusUnprocessableEntity || decodeError(t, w).Code != codeIdempotencyMismatch {
			t.Errorf("race mismatch: status %d, want 422 idempotency_mismatch", w.Code)
		}
		if rg.blobJobs(t) != 1 {
			t.Errorf("blob jobs %d, want 1: the losers' blobs are removed", rg.blobJobs(t))
		}
	})
	t.Run("body hash ignores boundary and filename, not part content or order of pages", func(t *testing.T) {
		t.Parallel()
		png, jpg := pngBytes(t), jpegBytes(t)
		hashOf := func(parts []mpart) string {
			body, ct := multipartBody(t, parts)
			r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
			r.Header.Set("Content-Type", ct)
			sub, _, ok := readSubmission(r, config.Limits{}, 1<<20)
			if !ok {
				t.Fatal("submission refused")
			}
			return sub.hash()
		}
		base := append(validParts(), mpart{name: partPage, ctype: "image/png", data: png},
			mpart{name: partPage, ctype: "image/jpeg", data: jpg})
		reordered := []mpart{base[3], base[4], base[1], base[5], base[0], base[2]} // pages keep their order
		renamed := withPart(base, mpart{name: partDocument, ctype: base[0].ctype, filename: "other.txt", data: base[0].data})
		swapped := append(validParts(), mpart{name: partPage, ctype: "image/jpeg", data: jpg},
			mpart{name: partPage, ctype: "image/png", data: png})
		h := hashOf(base)
		if hashOf(base) != h || hashOf(reordered) != h || hashOf(renamed) != h {
			t.Error("the hash depends on the boundary, part order or filename")
		}
		if hashOf(swapped) == h {
			t.Error("the hash ignores page order")
		}
		if hashOf(withPart(base, mpart{name: partSchema, ctype: "application/json", data: []byte(submitSchema)})) == h {
			t.Error("the hash ignores a part's media type")
		}
	})
}

// racingStore hides the next idempotency lookup when hide is set, as if another process
// inserted the row between this request's lookup and its insert.
type racingStore struct {
	job.Store
	hide *atomic.Bool
}

func (s *racingStore) FindByIdempotencyKey(ctx context.Context, app, key string) (job.Job, error) {
	if s.hide.CompareAndSwap(true, false) {
		return job.Job{}, job.ErrNotFound
	}
	return s.Store.FindByIdempotencyKey(ctx, app, key)
}

func TestJobToResponse(t *testing.T) {
	t.Parallel()
	confidence := 0.75
	created := time.Date(2026, 10, 16, 9, 12, 3, 500_000_000, time.FixedZone("CEST", 2*3600))
	j := &job.Job{
		ID: "j_1", Status: job.StatusRejected, Attempts: 3, ServiceVersion: "v",
		Result:          json.RawMessage(`{"name":"x"}`),
		Feedback:        json.RawMessage(`[{"pointer":"/name","message":"m","source":"judge"}]`),
		JudgeConfidence: &confidence,
		CreatedAt:       created, FinishedAt: created.Add(time.Minute), ExpiresAt: created.Add(24 * time.Hour),
	}
	raw, err := json.Marshal(jobToResponse(j))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"j_1","status":"rejected","result":{"name":"x"},"feedback":[{"pointer":"/name","message":"m","source":"judge"}],` +
		`"attempts":3,"service_version":"v","judge_confidence":0.75,"verification":"none","error":null,` +
		`"created_at":"2026-10-16T07:12:03Z","finished_at":"2026-10-16T07:13:03Z","expires_at":"2026-10-17T07:12:03Z"}`
	if string(raw) != want {
		t.Errorf("got  %s\nwant %s", raw, want)
	}
	failed := &job.Job{ID: "j_2", Status: job.StatusFailed, Error: json.RawMessage(`{"code":"timeout","message":"m"}`),
		Feedback: json.RawMessage(`null`)}
	raw, _ = json.Marshal(jobToResponse(failed))
	if !strings.Contains(string(raw), `"feedback":[]`) || !strings.Contains(string(raw), `"error":{"code":"timeout"`) ||
		!strings.Contains(string(raw), `"result":null`) || strings.Contains(string(raw), "attachments") {
		t.Errorf("failed job = %s", raw)
	}
	// A running job's saved feedback may hold a grounding repair's entries: under judge
	// policy always they never show (contract v1 sources only), under auto they do.
	saved := json.RawMessage(`[{"pointer":"/born","message":"value not found in the document","source":"grounding"}]`)
	for policy, want := range map[string]string{"always": `"feedback":[]`, "": `"feedback":[]`, "auto": `"source":"grounding"`} {
		running := &job.Job{ID: "j_3", Status: job.StatusRunning, Attempts: 1, JudgePolicy: policy, Feedback: saved}
		if raw, _ := json.Marshal(jobToResponse(running)); !strings.Contains(string(raw), want) {
			t.Errorf("policy %q: running job = %s, want %s", policy, raw, want)
		}
	}
}

// TestAttachmentsPersisted: an email's attachment list is stored with the job, so the
// submit answer, a poll and an idempotent replay list it alike.
func TestAttachmentsPersisted(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{})
	parts := withPart(validParts(), mpart{name: partDocument, ctype: "message/rfc822", data: []byte(email)})
	key := idempotencyKey("mail-1")
	created := decodeJob(t, rg.submit(t, parts, key))
	var polled map[string]any
	if w := rg.get("/v1/jobs/" + created["id"].(string)); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &polled) != nil {
		t.Fatalf("poll = %d %s", w.Code, w.Body)
	}
	replayed := rg.submit(t, parts, key)
	if replayed.Header().Get(headerIdempotencyReplayed) != "true" {
		t.Fatalf("second submission was not a replay: %d", replayed.Code)
	}
	for name, body := range map[string]map[string]any{"submit": created, "poll": polled, "replay": decodeJob(t, replayed)} {
		atts, _ := body["attachments"].([]any)
		if len(atts) != 1 || atts[0].(map[string]any)["name"] != "ticket.pdf" {
			t.Errorf("%s: attachments = %v, want ticket.pdf", name, body["attachments"])
		}
	}
	j, err := rg.db.GetJob(t.Context(), testApp, created["id"].(string))
	if err != nil || len(j.Attachments) != 1 || j.Attachments[0].MediaType != "application/pdf" {
		t.Errorf("stored attachments = %+v, %v", j.Attachments, err)
	}
}

// TestForbiddenCarriesQuotaHeaders: a scope refusal is an authenticated response, so it
// carries X-Quota-Remaining and X-Quota-Reset (contract §8).
func TestForbiddenCarriesQuotaHeaders(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{})
	rg.token = issueToken(t, rg.db, testApp, []string{auth.ScopeJobsRead}, time.Now().Add(time.Hour))
	w := rg.submit(t, validParts(), nil)
	if w.Code != http.StatusForbidden || w.Header().Get(headerQuotaRemaining) != "100" || w.Header().Get(headerQuotaReset) == "" {
		t.Errorf("403 = %d, quota headers %q %q; want 403 with both", w.Code,
			w.Header().Get(headerQuotaRemaining), w.Header().Get(headerQuotaReset))
	}
}

// judgeVersion is a rig's AppServiceVersion: one version per judge policy.
func judgeVersion(_ job.Application, p config.JudgePolicy) string { return "vde-0.4.0+" + p.String() }

// TestSubmitJudgePart: the judge part (ADR 0009) is accepted only when the application
// allows override, is validated, chooses the stored policy and service_version, and
// counts in the idempotency body hash.
func TestSubmitJudgePart(t *testing.T) {
	t.Parallel()
	judgePart := func(v string) mpart { return mpart{name: partJudge, ctype: "text/plain", data: []byte(v)} }

	t.Run("not allowed", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{appVersion: judgeVersion})
		for _, v := range []string{"always", "never", "bogus"} {
			w := rg.submit(t, append(validParts(), judgePart(v)), nil)
			if w.Code != http.StatusBadRequest || decodeError(t, w).Code != codeBadRequest ||
				!strings.Contains(w.Body.String(), "judge part") {
				t.Errorf("judge %s without allow_override: %d %s, want 400 naming the part", v, w.Code, w.Body)
			}
		}
		// Without the part the application's policy (here the role's) is stored.
		w := rg.submit(t, validParts(), nil)
		j, err := rg.db.GetJobAny(t.Context(), decodeJob(t, w)["id"].(string))
		if err != nil || j.JudgePolicy != "always" || j.ServiceVersion != "vde-0.4.0+always" ||
			w.Header().Get(headerServiceVersion) != j.ServiceVersion {
			t.Errorf("stored policy %q, version %q, header %q, %v", j.JudgePolicy, j.ServiceVersion,
				w.Header().Get(headerServiceVersion), err)
		}
		if n := rg.quotaUsed(t); n != 1 {
			t.Errorf("quota used %d, want 1: a refused part charges nothing", n)
		}
	})

	t.Run("application policy", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{appVersion: judgeVersion,
			app: func(a *job.Application) { a.JudgePolicy = "auto" }})
		j, _ := rg.db.GetJobAny(t.Context(), decodeJob(t, rg.submit(t, validParts(), nil))["id"].(string))
		if j.JudgePolicy != "auto" || j.ServiceVersion != "vde-0.4.0+auto" {
			t.Errorf("stored policy %q, version %q; want the application's auto", j.JudgePolicy, j.ServiceVersion)
		}
	})

	t.Run("allowed", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{appVersion: judgeVersion,
			app: func(a *job.Application) { a.JudgeAllowOverride = true }})
		for _, tt := range []struct {
			part, want string
			ctype      string
		}{
			{"never", "never", "text/plain"},
			{" auto\n", "auto", "text/plain; charset=utf-8"},
			{"always", "always", ""},
		} {
			p := judgePart(tt.part)
			p.ctype = tt.ctype
			w := rg.submit(t, append(validParts(), p), nil)
			if w.Code != http.StatusAccepted {
				t.Fatalf("judge %q: %d %s", tt.part, w.Code, w.Body)
			}
			body := decodeJob(t, w)
			j, err := rg.db.GetJobAny(t.Context(), body["id"].(string))
			want := "vde-0.4.0+" + tt.want
			if err != nil || j.JudgePolicy != tt.want || j.ServiceVersion != want || body["service_version"] != want ||
				w.Header().Get(headerServiceVersion) != want {
				t.Errorf("judge %q: stored %q, version %q, body %v, header %q; want %s", tt.part, j.JudgePolicy,
					j.ServiceVersion, body["service_version"], w.Header().Get(headerServiceVersion), want)
			}
			if body["verification"] != nil {
				t.Errorf("queued job verification = %v, want null", body["verification"])
			}
		}
		for name, p := range map[string]mpart{
			"unknown value": judgePart("sometimes"),
			"empty":         judgePart(""),
			"too long":      judgePart(strings.Repeat("a", maxJudgeBytes+1)),
			"media type":    {name: partJudge, ctype: "application/json", data: []byte(`"never"`)},
		} {
			w := rg.submit(t, append(validParts(), p), nil)
			if w.Code != http.StatusBadRequest || decodeError(t, w).Code != codeBadRequest {
				t.Errorf("%s: %d %s, want 400 bad_request", name, w.Code, w.Body)
			}
		}
		if w := rg.submit(t, append(validParts(), judgePart("never"), judgePart("auto")), nil); w.Code != http.StatusBadRequest {
			t.Errorf("repeated judge part: %d, want 400", w.Code)
		}
	})

	t.Run("idempotency", func(t *testing.T) {
		t.Parallel()
		rg := newSubmitRig(t, submitRigOptions{app: func(a *job.Application) { a.JudgeAllowOverride = true }})
		key := idempotencyKey("judge-1")
		first := rg.submit(t, append(validParts(), judgePart("never")), key)
		if first.Code != http.StatusAccepted {
			t.Fatalf("first: %d %s", first.Code, first.Body)
		}
		if w := rg.submit(t, append(validParts(), judgePart("never")), key); w.Header().Get(headerIdempotencyReplayed) != "true" {
			t.Errorf("same judge: %d, want a replay", w.Code)
		}
		for name, parts := range map[string][]mpart{
			"other judge": append(validParts(), judgePart("auto")),
			"no judge":    validParts(),
		} {
			w := rg.submit(t, parts, key)
			if w.Code != http.StatusUnprocessableEntity || decodeError(t, w).Code != codeIdempotencyMismatch {
				t.Errorf("%s: %d %s, want 422 idempotency_mismatch", name, w.Code, w.Body)
			}
		}
	})
}

// stubPDF is a document.PDFEngine whose PDFs have pages pages of text that grounds
// submitCandidate.
type stubPDF struct{ pages int }

func (e stubPDF) Open(context.Context, []byte) (document.PDFDoc, error) { return stubPDFDoc(e), nil }

type stubPDFDoc stubPDF

func (d stubPDFDoc) PageCount() int { return d.pages }
func (stubPDFDoc) Text(context.Context, int) (string, error) {
	return strings.Repeat("SENTINEL-PDF booking for Ada Lovelace. ", 8), nil
}
func (stubPDFDoc) Render(context.Context, int, int) ([]byte, error) {
	return nil, errors.New("not rendered")
}
func (stubPDFDoc) Close() {}

// TestSubmitPDFPreparedByService: with the PDF engine, a PDF without pages for groups
// that read no application/pdf is accepted and stored as its text layer (ADR 0010), and
// the worker runs it with the same selection; a PDF over documents.pdf.max_pages is
// refused with 415 naming the limit.
func TestSubmitPDFPreparedByService(t *testing.T) {
	t.Parallel()
	pdfParts := withPart(validParts(), mpart{name: partDocument, ctype: "application/pdf", filename: "booking.pdf",
		data: []byte("%PDF-1.7 SENTINEL-PDF")})

	rg := newSubmitRig(t, submitRigOptions{pool: true, pdf: stubPDF{pages: 2}})
	w := rg.submit(t, pdfParts, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	id, _ := decodeJob(t, w)["id"].(string)
	got := rg.waitTerminal(t, id)
	if got.Status != job.StatusAccepted || got.Representation != "text+text_layer" {
		t.Errorf("job %s, representation %q; want accepted, text+text_layer", got.Status, got.Representation)
	}

	long := newSubmitRig(t, submitRigOptions{pdf: stubPDF{pages: 21}})
	w = long.submit(t, pdfParts, nil)
	if w.Code != http.StatusUnsupportedMediaType || !strings.Contains(w.Body.String(), "unsupported_document") ||
		!strings.Contains(w.Body.String(), "20 pages") {
		t.Errorf("21 pages: status %d %s, want 415 naming the limit", w.Code, w.Body)
	}
}

// busyPDF is a document.PDFEngine with no instance free.
type busyPDF struct{}

func (busyPDF) Open(context.Context, []byte) (document.PDFDoc, error) {
	return nil, fmt.Errorf("%w: no instance free", pdf.ErrBusy)
}

// TestSubmitPDFEngineBusy: a PDF the service must prepare while every engine instance
// stays busy is answered 503 unavailable with Retry-After: 5, not 500: nothing is wrong
// with the document or the configuration, and no job is stored.
func TestSubmitPDFEngineBusy(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{pdf: busyPDF{}})
	w := rg.submit(t, withPart(validParts(), mpart{name: partDocument, ctype: "application/pdf", filename: "booking.pdf",
		data: []byte("%PDF-1.7 SENTINEL-PDF")}), nil)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" ||
		!strings.Contains(w.Body.String(), `"code":"unavailable"`) {
		t.Errorf("status %d, Retry-After %q: %s; want 503 unavailable, 5", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	if jobs, err := rg.db.ListJobs(t.Context(), job.JobFilter{}, 0); err != nil || len(jobs) != 0 {
		t.Errorf("jobs stored: %d, %v", len(jobs), err)
	}
}
