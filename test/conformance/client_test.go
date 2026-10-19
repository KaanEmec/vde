package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// client talks to one service over HTTP and checks every response against the
// contract's rules for all responses (checkResponse).
type client struct {
	base   string
	header http.Header // added to every request (X-Forwarded-Proto for a proxy instance)
}

func mainClient() *client { return &client{base: env.base} }

func (inst *instance) client() *client { return &client{base: inst.base} }

// reply is one response, read whole.
type reply struct {
	status    int
	header    http.Header
	body      []byte
	requestID string // the X-Request-Id the request carried
}

var requestSeq atomic.Int64

var httpClient = &http.Client{Timeout: 2 * time.Minute}

// do sends one request with a fresh X-Request-Id (unless hdr sets one) and checks the
// response with checkResponse.
func (c *client) do(t *testing.T, method, path, token string, body []byte, contentType string, hdr http.Header) reply {
	t.Helper()
	r := c.raw(t, method, path, token, body, contentType, hdr)
	checkResponse(t, path, token, r)
	return r
}

// raw is do without the response checks.
func (c *client) raw(t *testing.T, method, path, token string, body []byte, contentType string, hdr http.Header) reply {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range c.header {
		req.Header[k] = vs
	}
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rid := req.Header.Get("X-Request-Id")
	if _, set := req.Header["X-Request-Id"]; !set {
		rid = fmt.Sprintf("conf-%d-%d", time.Now().UnixNano(), requestSeq.Add(1))
		req.Header.Set("X-Request-Id", rid)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the body: %v", method, path, err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: data, requestID: rid}
}

// errorBody is the error shape of contract §9.
type errorBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func (r reply) errorCode() string {
	var e errorBody
	_ = json.Unmarshal(r.body, &e)
	return e.Error.Code
}

// statusOfCode is the HTTP status of each error code of contract §9.
var statusOfCode = map[string]int{
	"bad_request": 400, "unauthorized": 401, "forbidden": 403, "not_found": 404,
	"payload_too_large": 413, "unsupported_media_type": 415, "unsupported_document": 415,
	"invalid_schema": 422, "idempotency_mismatch": 422, "rate_limited": 429, "quota_exhausted": 429,
	"internal": 500, "provider_unavailable": 502, "unavailable": 503, "timeout": 504,
}

// checkResponse applies the rules every response must follow:
//   - §2 X-Request-Id echoes the client's header;
//   - §11 every /v1/jobs response carries X-Service-Version;
//   - §8 every response to an authenticated request carries X-Quota-Remaining and
//     X-Quota-Reset (RFC 3339 UTC); a 403 is checked by TestS08QuotaHeadersOnForbidden;
//   - §9 every error has Content-Type application/json and the Error body, with a code
//     of the §9 table sent with its status;
//   - §4/§8 every 429 carries Retry-After;
//   - §5 a job body validates against the Job schema, with RFC 3339 UTC timestamps
//     and service_version equal to X-Service-Version; §8 a quota body against Quota.
func checkResponse(t *testing.T, path, token string, r reply) {
	t.Helper()
	where := fmt.Sprintf("%s → %d", path, r.status)
	if got := r.header.Get("X-Request-Id"); got != r.requestID {
		t.Errorf("%s: X-Request-Id %q, want the request's %q echoed", where, got, r.requestID)
	}
	isJobs := path == "/v1/jobs" || strings.HasPrefix(path, "/v1/jobs/")
	if isJobs && r.header.Get("X-Service-Version") == "" {
		t.Errorf("%s: no X-Service-Version", where)
	}
	if strings.HasPrefix(path, "/v1/") && token != "" && r.status != http.StatusUnauthorized && r.status != http.StatusForbidden {
		checkQuotaHeaders(t, where, r)
	}
	if r.status >= 400 {
		if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: error Content-Type %q, want application/json", where, ct)
		}
		if errs := validateBody(t, "Error", r.body); len(errs) != 0 {
			t.Errorf("%s: error body %s: %v", where, r.body, errs)
		}
		var e errorBody
		_ = json.Unmarshal(r.body, &e)
		if want, known := statusOfCode[e.Error.Code]; known && want != r.status {
			t.Errorf("%s: error code %s belongs to status %d", where, e.Error.Code, want)
		}
		if e.Error.RequestID != "" && e.Error.RequestID != r.requestID {
			t.Errorf("%s: error.request_id %q, want %q", where, e.Error.RequestID, r.requestID)
		}
	}
	if r.status == http.StatusTooManyRequests && !validRetryAfter(r.header.Get("Retry-After")) {
		t.Errorf("%s: Retry-After %q, want seconds or an HTTP date", where, r.header.Get("Retry-After"))
	}
	if isJobs && (r.status == http.StatusOK || r.status == http.StatusAccepted) {
		if errs := validateBody(t, "Job", r.body); len(errs) != 0 {
			t.Errorf("%s: job body %s: %v", where, r.body, errs)
		}
		var j map[string]any
		_ = json.Unmarshal(r.body, &j)
		for _, k := range []string{"created_at", "finished_at", "expires_at"} {
			if s, ok := j[k].(string); ok && !isRFC3339UTC(s) {
				t.Errorf("%s: %s %q is not RFC 3339 UTC", where, k, s)
			}
		}
		if v, _ := j["service_version"].(string); v == "" || v != r.header.Get("X-Service-Version") {
			t.Errorf("%s: service_version %q, X-Service-Version %q", where, v, r.header.Get("X-Service-Version"))
		}
	}
	if path == "/v1/quota" && r.status == http.StatusOK {
		if errs := validateBody(t, "Quota", r.body); len(errs) != 0 {
			t.Errorf("%s: quota body %s: %v", where, r.body, errs)
		}
	}
}

func checkQuotaHeaders(t *testing.T, where string, r reply) {
	t.Helper()
	rem := r.header.Get("X-Quota-Remaining")
	if n, err := strconv.Atoi(rem); err != nil || n < 0 {
		t.Errorf("%s: X-Quota-Remaining %q, want a non-negative integer", where, rem)
	}
	if reset := r.header.Get("X-Quota-Reset"); !isRFC3339UTC(reset) {
		t.Errorf("%s: X-Quota-Reset %q, want RFC 3339 UTC", where, reset)
	}
}

func isRFC3339UTC(s string) bool {
	ts, err := time.Parse(time.RFC3339, s)
	return err == nil && strings.HasSuffix(s, "Z") && ts.Location() == time.UTC
}

func validRetryAfter(v string) bool {
	if n, err := strconv.Atoi(v); err == nil {
		return n >= 0
	}
	_, err := http.ParseTime(v)
	return err == nil
}

// ---- multipart requests ----

// part is one multipart part.
type part struct {
	name, filename, contentType string
	data                        []byte
}

var inputs struct {
	once                          sync.Once
	extractor, judge, schema      []byte
	txt, html, eml, pdf, png, jpg []byte
	err                           error
}

func loadInputs(t *testing.T) {
	t.Helper()
	if err := load(); err != nil {
		t.Fatal(err)
	}
}

// load reads the TravelInbox inputs and the samples once.
func load() error {
	inputs.once.Do(func() {
		read := func(rel ...string) []byte {
			data, err := os.ReadFile(filepath.Join(append([]string{env.root}, rel...)...))
			if err != nil && inputs.err == nil {
				inputs.err = err
			}
			return data
		}
		inputs.extractor = read("testdata", "travelinbox", "corpus", "extractor.md")
		inputs.judge = read("testdata", "travelinbox", "corpus", "judge.md")
		inputs.schema = read("FromTravelApp", "extraction-schema.json")
		inputs.txt = read("examples", "samples", "booking.txt")
		inputs.html = read("examples", "samples", "booking.html")
		inputs.eml = read("examples", "samples", "booking.eml")
		inputs.pdf = read("examples", "samples", "booking.pdf")
		inputs.png = read("examples", "samples", "booking.png")
		inputs.jpg = read("examples", "samples", "receipt.jpg")
	})
	return inputs.err
}

// textDocument is a plain-text document that carries note, so the fake provider's
// scripts and the call record can tell the test's job from others.
func textDocument(note string) part {
	return part{"document", "booking.txt", "text/plain; charset=utf-8",
		[]byte("Reference: " + note + "\nHarbour View Hotel, booking HV-48213, 20-23 February 2026.\n")}
}

func sampleDocument(t *testing.T, name, contentType string) part {
	t.Helper()
	loadInputs(t)
	data := map[string][]byte{
		"booking.txt": inputs.txt, "booking.html": inputs.html, "booking.eml": inputs.eml,
		"booking.pdf": inputs.pdf, "booking.png": inputs.png, "receipt.jpg": inputs.jpg,
	}[name]
	return part{"document", name, contentType, data}
}

func pagePart(t *testing.T) part {
	t.Helper()
	loadInputs(t)
	return part{"page", "page-1.png", "image/png", inputs.png}
}

// standardParts is TravelInbox's submission (contract §4, §15) for doc and pages:
// its extractor and judge instructions and its schema.
func standardParts(doc part, pages ...part) []part {
	if err := load(); err != nil {
		panic(err) // the repository's own test inputs are missing
	}
	parts := append([]part{doc}, pages...)
	return append(parts,
		part{"extractor_instructions", "extractor.md", "text/markdown", inputs.extractor},
		part{"judge_instructions", "judge.md", "text/markdown", inputs.judge},
		part{"schema", "extraction-schema.json", "application/schema+json", inputs.schema},
	)
}

func textPart(name, value string) part {
	return part{name, "", "text/plain", []byte(value)}
}

// replacePart returns parts with the part named name replaced by p (or removed when p
// has no name).
func replacePart(parts []part, name string, p part) []part {
	var out []part
	for _, q := range parts {
		switch {
		case q.name != name:
			out = append(out, q)
		case p.name != "":
			out = append(out, p)
		}
	}
	return out
}

func encodeMultipart(t *testing.T, parts []part) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		disp := `form-data; name="` + p.name + `"`
		if p.filename != "" {
			disp += `; filename="` + p.filename + `"`
		}
		h.Set("Content-Disposition", disp)
		if p.contentType != "" {
			h.Set("Content-Type", p.contentType)
		}
		pw, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(p.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// ---- jobs ----

// jobBody is the job resource of contract §5.
type jobBody struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result"`
	Feedback       []feedback      `json:"feedback"`
	Attempts       int             `json:"attempts"`
	ServiceVersion string          `json:"service_version"`
	Error          *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt        string  `json:"created_at"`
	FinishedAt       *string `json:"finished_at"`
	ExpiresAt        string  `json:"expires_at"`
	ContentDeletedAt *string `json:"content_deleted_at"` // vde superset: set only in a replay of a content-deleted job

	raw    []byte
	header http.Header
}

type feedback struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
	Source  string `json:"source"`
}

func (j jobBody) terminal() bool {
	return j.Status != "queued" && j.Status != "running"
}

func (j jobBody) errorCode() string {
	if j.Error == nil {
		return ""
	}
	return j.Error.Code
}

func parseJob(t *testing.T, r reply) jobBody {
	t.Helper()
	var j jobBody
	if err := json.Unmarshal(r.body, &j); err != nil {
		t.Fatalf("job body %s: %v", r.body, err)
	}
	j.raw, j.header = r.body, r.header
	return j
}

// submit posts parts to /v1/jobs.
func (c *client) submit(t *testing.T, token string, parts []part, hdr http.Header) reply {
	t.Helper()
	body, ct := encodeMultipart(t, parts)
	return c.do(t, http.MethodPost, "/v1/jobs", token, body, ct, hdr)
}

// submitOK submits and requires 202 with Location and a queued job (contract §4). The
// job is polled to a terminal state when the test ends, if the test did not.
func (c *client) submitOK(t *testing.T, token string, parts []part, hdr http.Header) jobBody {
	t.Helper()
	r := c.submit(t, token, parts, hdr)
	if r.status != http.StatusAccepted {
		t.Fatalf("POST /v1/jobs = %d %s, want 202", r.status, r.body)
	}
	j := parseJob(t, r)
	if j.ID == "" || j.Status != "queued" {
		t.Errorf("202 body: id %q status %q, want an id and queued", j.ID, j.Status)
	}
	if loc := r.header.Get("Location"); loc != "/v1/jobs/"+j.ID {
		t.Errorf("Location %q, want /v1/jobs/%s", loc, j.ID)
	}
	t.Cleanup(func() { c.drain(token, j.ID) })
	return j
}

// pollInterval and pollLimit: fast against the fake provider, contract-like against a
// deployment (contract §6: a job ends within at most 8 minutes).
func pollTiming() (time.Duration, time.Duration) {
	if env.remote {
		return 2 * time.Second, 9 * time.Minute
	}
	return 20 * time.Millisecond, 60 * time.Second
}

// get polls the job once.
func (c *client) get(t *testing.T, token, id string) reply {
	t.Helper()
	return c.do(t, http.MethodGet, "/v1/jobs/"+id, token, nil, "", nil)
}

// waitTerminal polls until the job is terminal and returns that (first terminal) read.
func (c *client) waitTerminal(t *testing.T, token, id string) jobBody {
	t.Helper()
	interval, limit := pollTiming()
	deadline := time.Now().Add(limit)
	for {
		r := c.get(t, token, id)
		if r.status != http.StatusOK {
			t.Fatalf("GET /v1/jobs/%s = %d %s", id, r.status, r.body)
		}
		j := parseJob(t, r)
		if j.terminal() {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still %s after %s", id, j.Status, limit)
		}
		time.Sleep(interval)
	}
}

// drain polls a job until it is terminal or gone, so that no job of one test runs
// into the next. Failures are not reported: the test itself checks what it needs.
func (c *client) drain(token, id string) {
	interval, limit := pollTiming()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(interval) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.base+"/v1/jobs/"+id, nil)
		if err != nil {
			return
		}
		for k, vs := range c.header {
			req.Header[k] = vs
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return
		}
		var j jobBody
		err = json.NewDecoder(resp.Body).Decode(&j)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil || j.terminal() {
			return
		}
	}
}

// quota reads GET /v1/quota.
type quotaBody struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     string `json:"reset"`
	Window    string `json:"window"`
}

func (c *client) quota(t *testing.T, token string) quotaBody {
	t.Helper()
	r := c.do(t, http.MethodGet, "/v1/quota", token, nil, "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /v1/quota = %d %s", r.status, r.body)
	}
	var q quotaBody
	if err := json.Unmarshal(r.body, &q); err != nil {
		t.Fatal(err)
	}
	if h := r.header.Get("X-Quota-Remaining"); h != strconv.Itoa(q.Remaining) {
		t.Errorf("X-Quota-Remaining %q, body remaining %d", h, q.Remaining)
	}
	if h := r.header.Get("X-Quota-Reset"); h != q.Reset {
		t.Errorf("X-Quota-Reset %q, body reset %q", h, q.Reset)
	}
	return q
}

// settle waits long enough for a wrongly queued job to have reached the fake provider.
func settle() { time.Sleep(400 * time.Millisecond) }

// sameJSON reports whether a and b are the same JSON value.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}
