package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	spec "github.com/kaanemec/vde/api"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/job"
)

// jobFixture is a server whose store holds testApp and otherApp, with a blob store.
type jobFixture struct {
	s       *server
	h       http.Handler
	store   *auditStore
	blobs   *blob.FS
	blobDir string
	token   string // testApp, scope jobs
	logs    *bytes.Buffer
}

func newJobFixture(t *testing.T) *jobFixture {
	t.Helper()
	store := newTestStore(t, testApp, otherApp)
	dir := t.TempDir()
	blobs, err := blob.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	s := newServer(testDeps(t, Deps{
		Store:          store,
		ServiceVersion: testServiceVersion,
		Logger:         slog.New(slog.NewJSONHandler(&logs, nil)),
		Extractor:      fakeHealth(true),
		Judge:          fakeHealth(true),
		Blobs:          blobs,
	}))
	return &jobFixture{s: s, h: handlerOf(s), store: store, blobs: blobs, blobDir: dir,
		token: testToken(t, s, auth.ScopeJobs), logs: &logs}
}

// create stores a queued job of app with a document blob. created zero means now.
func (f *jobFixture) create(t *testing.T, app string, retention job.Retention, created time.Time) job.Job {
	t.Helper()
	j := job.Job{ApplicationID: app, ServiceVersion: testServiceVersion, Representation: "text",
		MediaType: "text/plain", Size: 11, Retention: retention, CreatedAt: created,
		ExtractorInstructions: "SENTINEL-extractor", JudgeInstructions: "SENTINEL-judge", Schema: `{"type":"object"}`}
	if err := f.store.CreateJob(context.Background(), &j); err != nil {
		t.Fatal(err)
	}
	if err := f.blobs.Put(context.Background(), j.ID, blob.Document, strings.NewReader("SENTINEL-doc")); err != nil {
		t.Fatal(err)
	}
	return j
}

// finish leases j (the only queued job) and writes o as its terminal state.
func (f *jobFixture) finish(t *testing.T, j job.Job, o job.Outcome) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	leased, err := f.store.LeaseNextQueued(ctx, "w", now, now.Add(time.Minute), nil)
	if err != nil || leased.ID != j.ID {
		t.Fatalf("lease = %s, %v; want %s", leased.ID, err, j.ID)
	}
	if err := f.store.Finish(ctx, j.ID, "w", o, now); err != nil {
		t.Fatal(err)
	}
}

func (f *jobFixture) do(method, id, token string) *httptest.ResponseRecorder {
	return serve(f.h, method, "/v1/jobs/"+id, func(r *http.Request) {
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	})
}

func (f *jobFixture) blobsLeft(t *testing.T, id string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(f.blobDir, id[len(id)-2:], id))
	return err == nil
}

var (
	acceptedResult = json.RawMessage(`{"schema_version":1,"reservations":[]}`)
	rejectedResult = json.RawMessage(`{"schema_version":1,"reservations":[{"booking_ref":"LX42Q2"}]}`)
	judgeFeedback  = json.RawMessage(`[{"pointer":"/reservations/0/booking_ref","message":"The source prints LX42QZ.","source":"judge"}]`)
	failedError    = json.RawMessage(`{"code":"provider_unavailable","message":"No endpoint of the judge group answered."}`)
)

func confidence(v float64) *float64 { return &v }

func TestGetJobBodies(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	jobSchema := loadJobSchema(t)
	tests := []struct {
		name    string
		outcome *job.Outcome // nil leaves the job queued
		want    map[string]string
	}{
		{name: "accepted", outcome: &job.Outcome{Status: job.StatusAccepted, Attempts: 2, Result: acceptedResult,
			Feedback: json.RawMessage(`[]`), JudgeConfidence: confidence(0.9)}, want: map[string]string{
			"status": `"accepted"`, "result": string(acceptedResult), "feedback": `[]`, "attempts": `2`,
			"error": `null`, "judge_confidence": `0.9`, "verification": `"judge"`,
		}},
		{name: "rejected", outcome: &job.Outcome{Status: job.StatusRejected, Attempts: 3, Result: rejectedResult,
			Feedback: judgeFeedback}, want: map[string]string{
			"status": `"rejected"`, "result": string(rejectedResult), "feedback": string(judgeFeedback),
			"attempts": `3`, "error": `null`, "verification": `"none"`,
		}},
		{name: "failed", outcome: &job.Outcome{Status: job.StatusFailed, Attempts: 1, Error: failedError},
			want: map[string]string{
				"status": `"failed"`, "result": `null`, "feedback": `[]`, "attempts": `1`, "error": string(failedError),
				"verification": `"none"`,
			}},
		{name: "extracted", outcome: &job.Outcome{Status: job.StatusExtracted, Attempts: 1, Result: acceptedResult,
			Feedback: json.RawMessage(`[]`)}, want: map[string]string{
			"status": `"extracted"`, "result": string(acceptedResult), "verification": `"none"`, "error": `null`,
		}},
		{name: "grounded", outcome: &job.Outcome{Status: job.StatusAccepted, Attempts: 1, Result: acceptedResult,
			Feedback: json.RawMessage(`[]`), Verification: job.VerificationGrounded}, want: map[string]string{
			"status": `"accepted"`, "verification": `"grounded"`,
		}},
		{name: "queued", want: map[string]string{
			"status": `"queued"`, "result": `null`, "feedback": `[]`, "attempts": `0`, "error": `null`,
			"finished_at": `null`, "judge_confidence": `null`, "verification": `null`,
		}},
	}
	for _, tt := range tests { // sequential: finish leases the only queued job, so queued is last
		j := f.create(t, testApp, job.RetentionDefault, time.Time{})
		if tt.outcome != nil {
			f.finish(t, j, *tt.outcome)
		}
		resp := f.do(http.MethodGet, j.ID, f.token)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body %s", tt.name, resp.Code, resp.Body.Bytes())
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		for k, v := range tt.want {
			if got := string(body[k]); got != v {
				t.Errorf("%s: %s = %s, want %s", tt.name, k, got, v)
			}
		}
		if string(body["id"]) != `"`+j.ID+`"` || string(body["service_version"]) != `"`+testServiceVersion+`"` {
			t.Errorf("%s: id/service_version = %s/%s", tt.name, body["id"], body["service_version"])
		}
		for _, k := range []string{"created_at", "expires_at"} {
			var ts string
			if err := json.Unmarshal(body[k], &ts); err != nil || !rfc3339Seconds(ts) {
				t.Errorf("%s: %s = %s, want RFC 3339 UTC whole seconds", tt.name, k, body[k])
			}
		}
		if tt.outcome != nil {
			var ts string
			if err := json.Unmarshal(body["finished_at"], &ts); err != nil || !rfc3339Seconds(ts) {
				t.Errorf("%s: finished_at = %s, want a timestamp", tt.name, body["finished_at"])
			}
		}
		if _, ok := body["attachments"]; ok {
			t.Errorf("%s: attachments present for a text document", tt.name)
		}
		if resp.Header().Get(headerServiceVersion) != testServiceVersion {
			t.Errorf("%s: X-Service-Version = %q", tt.name, resp.Header().Get(headerServiceVersion))
		}
		checkJobSchema(t, jobSchema, resp.Body.Bytes())
		if bytes.Contains(resp.Body.Bytes(), []byte("SENTINEL")) {
			t.Errorf("%s: body carries the job's inputs: %s", tt.name, resp.Body.Bytes())
		}
	}
	if strings.Contains(f.logs.String(), "SENTINEL") || strings.Contains(f.logs.String(), "LX42") {
		t.Errorf("logs carry content: %s", f.logs)
	}
}

func rfc3339Seconds(s string) bool {
	ts, err := time.Parse(time.RFC3339, s)
	return err == nil && strings.HasSuffix(s, "Z") && ts.Nanosecond() == 0 && len(s) == len("2026-10-16T09:12:03Z")
}

func TestGetJobNotFound(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	foreign := f.create(t, otherApp, job.RetentionDefault, time.Time{})
	expired := f.create(t, testApp, job.RetentionDefault, time.Now().Add(-25*time.Hour)) // testApp keeps 24h
	readOnly := issueToken(t, f.store, testApp, []string{auth.ScopeJobsRead}, time.Now().Add(time.Hour))
	tests := []struct{ name, method, id, token string }{
		{"unknown", http.MethodGet, "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB", f.token},
		{"foreign", http.MethodGet, foreign.ID, f.token},
		{"foreign with a read-only token", http.MethodGet, foreign.ID, readOnly},
		{"expired before the sweep", http.MethodGet, expired.ID, f.token},
		{"delete unknown", http.MethodDelete, "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB", f.token},
		{"delete foreign", http.MethodDelete, foreign.ID, f.token},
	}
	var bodies []string
	for _, tt := range tests {
		resp := f.do(tt.method, tt.id, tt.token)
		if resp.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", tt.name, resp.Code)
			continue
		}
		var e errorBody
		if err := json.Unmarshal(resp.Body.Bytes(), &e); err != nil || e.Error.Code != codeNotFound {
			t.Errorf("%s: body %s, want not_found", tt.name, resp.Body.Bytes())
		}
		bodies = append(bodies, e.Error.Message)
	}
	for _, b := range bodies {
		if b != msgJobNotFound {
			t.Errorf("message %q: unknown and foreign jobs must read alike", b)
		}
	}
	if _, err := f.store.GetJobAny(context.Background(), foreign.ID); err != nil {
		t.Errorf("foreign job after a refused DELETE: %v, want it kept", err)
	}
	if !f.blobsLeft(t, foreign.ID) {
		t.Error("foreign job's blobs deleted by a refused DELETE")
	}
	if code := f.do(http.MethodGet, foreign.ID, "").Code; code != http.StatusUnauthorized {
		t.Errorf("without a token: %d, want 401", code)
	}
}

// TestGetJobRetentionNone is contract §10 and §14 item 8 at the handler: the first read
// of a terminal retention: none job returns the whole body and deletes the content; the
// next read is 404; a read in progress deletes nothing.
func TestGetJobRetentionNone(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	ctx := context.Background()
	j := f.create(t, testApp, job.RetentionNone, time.Time{})
	if resp := f.do(http.MethodGet, j.ID, f.token); resp.Code != http.StatusOK {
		t.Fatalf("queued read = %d, want 200", resp.Code)
	}
	if got, _ := f.store.GetJobAny(ctx, j.ID); !got.ContentDeletedAt.IsZero() {
		t.Fatal("a read in progress deleted the content")
	}
	f.finish(t, j, job.Outcome{Status: job.StatusRejected, Attempts: 3, Result: rejectedResult, Feedback: judgeFeedback})

	first := f.do(http.MethodGet, j.ID, f.token)
	if first.Code != http.StatusOK || !bytes.Contains(first.Body.Bytes(), []byte("LX42Q2")) || !bytes.Contains(first.Body.Bytes(), []byte("LX42QZ")) {
		t.Fatalf("first terminal read = %d %s, want the whole body", first.Code, first.Body.Bytes())
	}
	if second := f.do(http.MethodGet, j.ID, f.token); second.Code != http.StatusNotFound {
		t.Errorf("second read = %d, want 404", second.Code)
	}
	row, err := f.store.GetJobAny(ctx, j.ID)
	if err != nil {
		t.Fatalf("row: %v (it stays for audit until expires_at)", err)
	}
	if row.ContentDeletedAt.IsZero() || row.Result != nil || row.Feedback != nil || row.Error != nil ||
		row.ExtractorInstructions != "" || row.JudgeInstructions != "" || row.Schema != "" {
		t.Errorf("row after the first read keeps content: %+v", row)
	}
	if f.blobsLeft(t, j.ID) {
		t.Error("blob directory left after the first read")
	}
	if e := f.store.last(); e.Kind != "job.deleted" || e.JobID != j.ID || !strings.Contains(string(e.Details), `"first_read"`) {
		t.Errorf("last audit event = %+v, want job.deleted first_read", e)
	}
	if code := f.do(http.MethodDelete, j.ID, f.token).Code; code != http.StatusNotFound {
		t.Errorf("DELETE after the first read = %d, want 404", code)
	}
}

// TestGetJobRetentionNoneConcurrent: of concurrent first reads exactly one gets the
// body; the others get 404, never a half-deleted job.
func TestGetJobRetentionNoneConcurrent(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	j := f.create(t, testApp, job.RetentionNone, time.Time{})
	f.finish(t, j, job.Outcome{Status: job.StatusAccepted, Attempts: 1, Result: acceptedResult})
	const readers = 8
	codes := make(chan int, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			resp := f.do(http.MethodGet, j.ID, f.token)
			if resp.Code == http.StatusOK && !bytes.Contains(resp.Body.Bytes(), []byte(`"reservations":[]`)) {
				t.Errorf("a 200 without the result: %s", resp.Body.Bytes())
			}
			codes <- resp.Code
		})
	}
	wg.Wait()
	close(codes)
	var ok, notFound int
	for c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusNotFound:
			notFound++
		}
	}
	if ok != 1 || notFound != readers-1 {
		t.Errorf("got %d 200 and %d 404, want exactly one 200", ok, notFound)
	}
}

func TestDeleteJob(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	ctx := context.Background()
	j := f.create(t, testApp, job.RetentionDefault, time.Time{})
	f.finish(t, j, job.Outcome{Status: job.StatusAccepted, Attempts: 1, Result: acceptedResult})
	readOnly := issueToken(t, f.store, testApp, []string{auth.ScopeJobsRead}, time.Now().Add(time.Hour))
	if code := f.do(http.MethodDelete, j.ID, readOnly).Code; code != http.StatusForbidden {
		t.Errorf("DELETE with jobs:read = %d, want 403", code)
	}
	resp := f.do(http.MethodDelete, j.ID, f.token)
	if resp.Code != http.StatusNoContent || len(resp.Body.Bytes()) != 0 {
		t.Fatalf("DELETE = %d %q, want 204 and no body", resp.Code, resp.Body.Bytes())
	}
	if resp.Header().Get(headerServiceVersion) != testServiceVersion {
		t.Errorf("X-Service-Version = %q", resp.Header().Get(headerServiceVersion))
	}
	if _, err := f.store.GetJobAny(ctx, j.ID); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("row after DELETE: %v, want gone", err)
	}
	if f.blobsLeft(t, j.ID) {
		t.Error("blobs left after DELETE")
	}
	if e := f.store.last(); e.Kind != "job.deleted" || e.JobID != j.ID || !strings.Contains(string(e.Details), `"request"`) {
		t.Errorf("last audit event = %+v, want job.deleted request", e)
	}
	if code := f.do(http.MethodGet, j.ID, f.token).Code; code != http.StatusNotFound {
		t.Errorf("GET after DELETE = %d, want 404", code)
	}
	if code := f.do(http.MethodDelete, j.ID, f.token).Code; code != http.StatusNotFound {
		t.Errorf("second DELETE = %d, want 404", code)
	}
}

// jobSchema is the part of the OpenAPI Job schema the checks below use.
type jobSchema struct {
	required   []string
	properties map[string]map[string]any
	schemas    map[string]map[string]any
}

func loadJobSchema(t *testing.T) jobSchema {
	t.Helper()
	var doc struct {
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(spec.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	js := doc.Components.Schemas["Job"]
	if js == nil {
		t.Fatal("no Job schema")
	}
	s := jobSchema{properties: map[string]map[string]any{}, schemas: doc.Components.Schemas}
	for _, r := range js["required"].([]any) {
		s.required = append(s.required, r.(string))
	}
	for k, v := range js["properties"].(map[string]any) {
		s.properties[k] = v.(map[string]any)
	}
	return s
}

// checkJobSchema validates body against the Job schema: required fields, no unknown
// fields, and each value's JSON type (type lists, $ref, oneOf, enum, array items'
// required fields, number bounds).
func checkJobSchema(t *testing.T, s jobSchema, body []byte) {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.required {
		if _, ok := obj[r]; !ok {
			t.Errorf("Job: required field %q missing", r)
		}
	}
	for k, v := range obj {
		prop, ok := s.properties[k]
		if !ok {
			t.Errorf("Job: field %q not in the schema", k)
			continue
		}
		if err := s.check(prop, v); err != "" {
			t.Errorf("Job.%s = %v: %s", k, v, err)
		}
	}
}

func (s jobSchema) check(schema map[string]any, v any) string {
	if ref, ok := schema["$ref"].(string); ok {
		return s.check(s.schemas[strings.TrimPrefix(ref, "#/components/schemas/")], v)
	}
	if alts, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, a := range alts {
			if s.check(a.(map[string]any), v) == "" {
				matches++
			}
		}
		if matches != 1 {
			return "matches " + string(rune('0'+matches)) + " of oneOf"
		}
		return ""
	}
	var types []string
	switch ty := schema["type"].(type) {
	case string:
		types = []string{ty}
	case []any:
		for _, x := range ty {
			types = append(types, x.(string))
		}
	}
	integerAsNumber := jsonType(v) == "integer" && slices.Contains(types, "number")
	if len(types) > 0 && !slices.Contains(types, jsonType(v)) && !integerAsNumber {
		return "type " + jsonType(v) + ", want " + strings.Join(types, " or ")
	}
	if enum, ok := schema["enum"].([]any); ok && !slices.Contains(enum, v) {
		return "not in enum"
	}
	if n, ok := v.(float64); ok {
		if lo, ok := schema["minimum"].(int); ok && n < float64(lo) {
			return "below minimum"
		}
		if hi, ok := schema["maximum"].(int); ok && n > float64(hi) {
			return "above maximum"
		}
	}
	if obj, ok := v.(map[string]any); ok {
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, ok := obj[r.(string)]; !ok {
					return "missing required " + r.(string)
				}
			}
		}
	}
	if arr, ok := v.([]any); ok {
		if items, ok := schema["items"].(map[string]any); ok {
			for _, it := range arr {
				if err := s.check(items, it); err != "" {
					return "item: " + err
				}
			}
		}
	}
	return ""
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// TestJobResponseMatchesOpenAPI is the OpenAPI job's deferred check: the handler's
// struct produces exactly the Job schema's fields.
func TestJobResponseMatchesOpenAPI(t *testing.T) {
	t.Parallel()
	s := loadJobSchema(t)
	var tags []string
	rt := reflect.TypeFor[jobResponse]()
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		tags = append(tags, name)
	}
	var props []string
	for k := range s.properties {
		props = append(props, k)
	}
	slices.Sort(tags)
	slices.Sort(props)
	if !slices.Equal(tags, props) {
		t.Errorf("jobResponse json fields %v, Job schema properties %v", tags, props)
	}
}

// TestGetJobRunningVerification: a running job's verification is null.
func TestGetJobRunningVerification(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	j := f.create(t, testApp, job.RetentionDefault, time.Time{})
	now := time.Now()
	if _, err := f.store.LeaseNextQueued(context.Background(), "w", now, now.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	resp := f.do(http.MethodGet, j.ID, f.token)
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil || string(body["status"]) != `"running"` {
		t.Fatalf("running job = %s, %v", resp.Body, err)
	}
	if v, ok := body["verification"]; !ok || string(v) != "null" {
		t.Errorf("running verification = %s (present %t), want null", v, ok)
	}
}

// TestGetJobRetentionNoneExtracted: an extracted job is terminal like the others: its
// first read under retention: none deletes its content.
func TestGetJobRetentionNoneExtracted(t *testing.T) {
	t.Parallel()
	f := newJobFixture(t)
	j := f.create(t, testApp, job.RetentionNone, time.Time{})
	f.finish(t, j, job.Outcome{Status: job.StatusExtracted, Attempts: 1, Result: acceptedResult, Feedback: json.RawMessage(`[]`)})
	first := f.do(http.MethodGet, j.ID, f.token)
	if first.Code != http.StatusOK || !bytes.Contains(first.Body.Bytes(), []byte(`"extracted"`)) {
		t.Fatalf("first read = %d %s", first.Code, first.Body)
	}
	if second := f.do(http.MethodGet, j.ID, f.token); second.Code != http.StatusNotFound {
		t.Errorf("second read = %d, want 404", second.Code)
	}
	if row, err := f.store.GetJobAny(context.Background(), j.ID); err != nil || row.ContentDeletedAt.IsZero() || row.Result != nil {
		t.Errorf("row after the first read = %+v, %v", row, err)
	}
	if f.blobsLeft(t, j.ID) {
		t.Error("blob directory left after the first read")
	}
}
