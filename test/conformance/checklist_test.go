package conformance

// One test per item of the acceptance checklist of contract §14 (items 1 to 9; item 10,
// the TravelInbox evaluation, is job 02 of epic 09).

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestItem01TraceabilityComplete: "Every MUST in sections 2 to 12 holds". Each MUST has
// its own test (TestS02… to TestS12…); this test checks that the traceability table of
// docs/conformance/travelinbox-v1.md gives every row a test of this package that exists,
// or an explicit "not testable automatically, verified by: …".
func TestItem01TraceabilityComplete(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(env.root, "docs", "conformance", "travelinbox-v1.md"))
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	files, _ := filepath.Glob("*_test.go")
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				defined[fn.Name.Name] = true
			}
		}
	}
	rowRe := regexp.MustCompile(`^\|\s*(\d+\.\d+)\s*\|`)
	testRe := regexp.MustCompile(`\bTest[A-Za-z0-9_]+`)
	rows, referenced := 0, map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		m := rowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rows++
		cells := strings.Split(line, "|")
		evidence := cells[len(cells)-2]
		tests := testRe.FindAllString(evidence, -1)
		if len(cells) < 4 || strings.TrimSpace(cells[2]) == "" {
			t.Errorf("row %s has no MUST text", m[1])
		}
		manual := strings.Contains(evidence, "not testable automatically, verified by:")
		if len(tests) == 0 && !manual {
			t.Errorf("row %s has neither a test nor \"not testable automatically, verified by: …\"", m[1])
		}
		for _, name := range tests {
			referenced[name] = true
			if !defined[name] && !strings.Contains(evidence, "internal/") {
				t.Errorf("row %s names %s, which this package does not define", m[1], name)
			}
		}
	}
	for _, name := range testRe.FindAllString(string(data), -1) {
		referenced[name] = true // the SHOULD list and prose count as references too
	}
	if rows < 40 {
		t.Errorf("the table has %d MUST rows; sections 2 to 12 have more", rows)
	}
	for name := range defined {
		if (strings.HasPrefix(name, "TestS") || strings.HasPrefix(name, "TestItem")) && !referenced[name] && name != "TestItem01TraceabilityComplete" {
			t.Errorf("%s is not referenced by the traceability table", name)
		}
	}
}

// TestItem02Unauthenticated: a request without a token, with a revoked token and with
// another application's token gets 401, 401 and (for a job) 404, and the provider usage
// counter does not move; nothing is stored and no quota is charged (contract §3).
func TestItem02Unauthenticated(t *testing.T) {
	c := mainClient()
	note := "item02-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	parts := standardParts(textDocument(note))
	owned := c.submitOK(t, env.token, standardParts(textDocument(note+"-owned")), nil)
	c.waitTerminal(t, env.token, owned.ID)

	before := providerCalls()
	blobsBefore := blobCount(t)
	quotaBefore := c.quota(t, env.token).Remaining

	if r := c.submit(t, "", parts, nil); r.status != http.StatusUnauthorized || r.errorCode() != "unauthorized" {
		t.Errorf("no token: %d %s, want 401 unauthorized", r.status, r.body)
	}
	if r := c.get(t, "", owned.ID); r.status != http.StatusUnauthorized {
		t.Errorf("poll without a token: %d, want 401", r.status)
	}
	if !env.remote {
		inst := env.main
		tok, id, err := inst.createToken(appDev, "jobs", "1d")
		if err != nil {
			t.Fatal(err)
		}
		if r := c.do(t, http.MethodGet, "/v1/quota", tok, nil, "", nil); r.status != http.StatusOK {
			t.Fatalf("the new token does not work before revocation: %d", r.status)
		}
		if _, stderr, err := inst.cli("token", "revoke", "--config", inst.config, id); err != nil {
			t.Fatalf("vde token revoke: %v: %s", err, stderr)
		}
		if r := c.submit(t, tok, parts, nil); r.status != http.StatusUnauthorized || r.errorCode() != "unauthorized" {
			t.Errorf("revoked token: %d %s, want 401 unauthorized", r.status, r.body)
		}
		if r := c.get(t, tok, owned.ID); r.status != http.StatusUnauthorized {
			t.Errorf("poll with a revoked token: %d, want 401", r.status)
		}
	}
	if env.foreignToken != "" {
		r := c.get(t, env.foreignToken, owned.ID)
		if r.status != http.StatusNotFound || r.errorCode() != "not_found" {
			t.Errorf("another application's token: %d %s, want 404 not_found", r.status, r.body)
		}
	}
	if !env.remote {
		settle()
		if after := providerCalls(); after != before {
			t.Errorf("provider calls moved from %d to %d", before, after)
		}
		if after := blobCount(t); after != blobsBefore {
			t.Errorf("stored uploads moved from %d to %d", blobsBefore, after)
		}
	}
	if after := c.quota(t, env.token).Remaining; after != quotaBefore {
		t.Errorf("quota remaining moved from %d to %d", quotaBefore, after)
	}
}

// TestItem03DocumentKinds: a text document, a PDF with pages, a JPEG and an HTML email
// body (plus a PNG and a whole .eml, which the service accepts as a superset) each reach a
// terminal state within the time limit with service_version and attempts set. Locally the
// judge must also see exactly the representation the extractor saw (contract §4).
func TestItem03DocumentKinds(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	for _, tc := range []struct {
		name  string
		doc   part
		pages []part
		text  string // a phrase of the document both roles must read, for text kinds
	}{
		{"text", sampleDocument(t, "booking.txt", "text/plain; charset=utf-8"), nil, "HV-48213"},
		{"pdf with page", sampleDocument(t, "booking.pdf", "application/pdf"), []part{pagePart(t)}, ""},
		{"jpeg", sampleDocument(t, "receipt.jpg", "image/jpeg"), nil, ""},
		{"png", sampleDocument(t, "booking.png", "image/png"), nil, ""},
		{"html email body", sampleDocument(t, "booking.html", "text/html; charset=utf-8"), nil, "HV-48213"},
		{"eml", sampleDocument(t, "booking.eml", "message/rfc822"), nil, "HV-48213"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := 0
			if !env.remote {
				start = env.llm.count(env.main.name)
			}
			created := c.submitOK(t, env.token, standardParts(tc.doc, tc.pages...), nil)
			begun := time.Now()
			final := c.waitTerminal(t, env.token, created.ID)
			if final.ServiceVersion == "" || final.Attempts < 1 || final.FinishedAt == nil {
				t.Errorf("terminal %s: service_version %q attempts %d finished_at %v",
					final.Status, final.ServiceVersion, final.Attempts, final.FinishedAt)
			}
			if time.Since(begun) > 5*time.Minute {
				t.Errorf("took %s, over the default 5 minute limit", time.Since(begun))
			}
			if env.remote {
				return
			}
			if final.Status != "accepted" {
				t.Errorf("status %s, want accepted with the default script", final.Status)
			}
			calls := env.llm.calls(env.main.name, start)
			ex, ju := byRole(calls, roleExtractor), byRole(calls, roleJudge)
			if len(ex) != 1 || len(ju) != 1 {
				t.Fatalf("%d extractor and %d judge calls, want 1 and 1", len(ex), len(ju))
			}
			if !slices.Equal(ex[0].Blobs, ju[0].Blobs) {
				t.Errorf("the judge read %d binary parts, the extractor %d, or different bytes", len(ju[0].Blobs), len(ex[0].Blobs))
			}
			if tc.text == "" {
				if len(ex[0].Blobs) == 0 {
					t.Error("a PDF or image document reached the models without any file or image part")
				}
				return
			}
			var shared bool
			for _, e := range ex[0].Texts {
				if strings.Contains(e, tc.text) && slices.Contains(ju[0].Texts, e) {
					shared = true
				}
			}
			if !shared {
				t.Error("the judge did not read the same document text part as the extractor")
			}
		})
	}
}

// TestItem04SchemaViolation: a candidate that violates the schema is sent back to the
// extractor with source "schema" feedback, without a judge call; after the attempt
// limit the job is rejected with the invalid candidate kept in result.
func TestItem04SchemaViolation(t *testing.T) {
	requireLocal(t, "scripts the extractor")
	c := mainClient()
	note := "item04-schema-violation"
	invalid := func(n int) string {
		return `{"schema_version": 2, "reservations": [], "notes": "` + note + ` attempt ` + strconv.Itoa(n) + `"}`
	}
	var attempt int
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{
		note: func(c llmCall) llmReply {
			if c.Role == roleJudge {
				return llmReply{Text: accept}
			}
			attempt++
			return llmReply{Text: invalid(attempt)}
		},
	}))
	start := env.llm.count(env.main.name)
	created := c.submitOK(t, env.token, standardParts(textDocument(note)), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	calls := env.llm.calls(env.main.name, start)

	if final.Status != "rejected" || final.Attempts != 3 || final.Error != nil {
		t.Errorf("status %s attempts %d error %v, want rejected after 3 attempts without error", final.Status, final.Attempts, final.Error)
	}
	if !sameJSON(final.Result, []byte(invalid(3))) {
		t.Errorf("result %s, want the last invalid candidate %s kept as produced", final.Result, invalid(3))
	}
	if n := len(byRole(calls, roleJudge)); n != 0 {
		t.Errorf("%d judge calls, want none", n)
	}
	ex := byRole(calls, roleExtractor)
	if len(ex) != 3 {
		t.Fatalf("%d extractor calls, want 3", len(ex))
	}
	if len(final.Feedback) == 0 {
		t.Fatal("no feedback on the rejected candidate")
	}
	for _, f := range final.Feedback {
		if f.Source != "schema" || f.Message == "" {
			t.Errorf("feedback %+v, want source schema with a message", f)
		}
	}
	if !slices.ContainsFunc(final.Feedback, func(f feedback) bool { return f.Pointer == "/schema_version" }) {
		t.Errorf("feedback %+v has no entry pointing at /schema_version", final.Feedback)
	}
	// The retry carries the previous candidate and its schema feedback.
	for i := 1; i < 3; i++ {
		if !ex[i].contains(note+" attempt "+strconv.Itoa(i)) || !ex[i].contains(final.Feedback[0].Message) {
			t.Errorf("extractor call %d lacks the previous candidate or the schema feedback", i+1)
		}
	}
}

// TestItem05JudgeGroupUnreachable: with both judge endpoints unreachable the job ends
// failed with provider_unavailable, never accepted; the judge's failover never crosses
// into the extractor group; /healthz reports 503 once the judge group is marked down.
func TestItem05JudgeGroupUnreachable(t *testing.T) {
	inst := newInstance(t, instanceOptions{name: "judge-down", unreachable: []string{"judge", "judge-fallback"}})
	c := inst.client()
	loadInputs(t)
	created := c.submitOK(t, inst.tokens[appDev], standardParts(textDocument("item05")), nil)
	final := c.waitTerminal(t, inst.tokens[appDev], created.ID)
	if final.Status != "failed" || final.errorCode() != "provider_unavailable" {
		t.Errorf("status %s error %v, want failed with provider_unavailable", final.Status, final.Error)
	}
	if final.Attempts != 1 || !sameJSON(final.Result, []byte(candidate("conformance default candidate"))) {
		t.Errorf("attempts %d result %s, want 1 and the candidate kept", final.Attempts, final.Result)
	}
	calls := env.llm.calls(inst.name, 0)
	if len(calls) != 1 || calls[0].Role != roleExtractor {
		t.Errorf("the fake provider saw %d calls, want only the one extractor call (no judge call crossing into the extractor group)", len(calls))
	}
	r := c.raw(t, http.MethodGet, "/healthz", "", nil, "", nil)
	if r.status != http.StatusServiceUnavailable {
		t.Errorf("/healthz with every judge endpoint down = %d %s, want 503", r.status, r.body)
	}
}

// TestItem06PrimaryExtractorUnreachable: with the primary extractor endpoint
// unreachable, jobs complete through the fallback endpoint and the result is the same
// as through the primary.
func TestItem06PrimaryExtractorUnreachable(t *testing.T) {
	inst := newInstance(t, instanceOptions{name: "primary-down", unreachable: []string{"primary"}})
	c := inst.client()
	loadInputs(t)
	for i := range 2 {
		created := c.submitOK(t, inst.tokens[appDev], standardParts(textDocument("item06-"+strconv.Itoa(i))), nil)
		final := c.waitTerminal(t, inst.tokens[appDev], created.ID)
		if final.Status != "accepted" || final.Attempts != 1 || len(final.Feedback) != 0 || final.Error != nil ||
			!sameJSON(final.Result, []byte(candidate("conformance default candidate"))) {
			t.Errorf("job %d: %s", i, final.raw)
		}
	}
	calls := env.llm.calls(inst.name, 0)
	for _, call := range calls {
		if call.Endpoint == "primary" || call.Endpoint == "judge-fallback" {
			t.Errorf("a call reached %s", call.Endpoint)
		}
	}
	if n := len(byRole(calls, roleExtractor)); n != 2 {
		t.Errorf("%d extractor calls reached the fallback, want 2", n)
	}
}

// TestItem07QuotaSpent: a spent quota answers 429 with X-Quota-Remaining: 0,
// X-Quota-Reset and Retry-After; polls and quota reads still work and charge nothing.
func TestItem07QuotaSpent(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	if env.remote {
		// Against a deployment only the poll half: polls and quota reads charge nothing.
		created := c.submitOK(t, env.token, standardParts(textDocument("item07-remote")), nil)
		before := c.quota(t, env.token)
		for range 3 {
			c.get(t, env.token, created.ID)
		}
		if after := c.quota(t, env.token); after.Remaining != before.Remaining {
			t.Errorf("polls changed the quota from %d to %d", before.Remaining, after.Remaining)
		}
		return
	}
	token := env.main.tokens[appQuota]
	var jobs []string
	var spent reply
	for range quotaLimit + 1 {
		r := c.submit(t, token, standardParts(textDocument("item07")), nil)
		if r.status == http.StatusAccepted {
			jobs = append(jobs, parseJob(t, r).ID)
			continue
		}
		spent = r
		break
	}
	if spent.status != http.StatusTooManyRequests || spent.errorCode() != "quota_exhausted" {
		t.Fatalf("submission %d after a quota of %d: %d %s, want 429 quota_exhausted", len(jobs)+1, quotaLimit, spent.status, spent.body)
	}
	if spent.header.Get("X-Quota-Remaining") != "0" {
		t.Errorf("X-Quota-Remaining %q, want 0", spent.header.Get("X-Quota-Remaining"))
	}
	reset, err := time.Parse(time.RFC3339, spent.header.Get("X-Quota-Reset"))
	if err != nil || !reset.After(time.Now()) {
		t.Errorf("X-Quota-Reset %q, want a future RFC 3339 time", spent.header.Get("X-Quota-Reset"))
	}
	if spent.header.Get("Retry-After") == "" {
		t.Error("no Retry-After on the 429")
	}
	for _, id := range jobs {
		final := c.waitTerminal(t, token, id) // polls work
		if final.Status != "accepted" {
			t.Errorf("job %s: %s", id, final.Status)
		}
		if r := c.get(t, token, id); r.status != http.StatusOK || r.header.Get("X-Quota-Remaining") != "0" {
			t.Errorf("poll after the quota is spent: %d, X-Quota-Remaining %q", r.status, r.header.Get("X-Quota-Remaining"))
		}
	}
	q := c.quota(t, token)
	if q.Remaining != 0 || q.Limit != quotaLimit || q.Reset != spent.header.Get("X-Quota-Reset") || q.Window != "P1D" {
		t.Errorf("GET /v1/quota = %+v, want remaining 0 of %d resetting at %s", q, quotaLimit, spent.header.Get("X-Quota-Reset"))
	}
}

// TestItem08RetentionNone: with retention none the document and pages are gone at the
// terminal state, before any read, and the job is 404 after its terminal state has been
// read once; expires_at reports the 24-hour bound.
func TestItem08RetentionNone(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	pdf := sampleDocument(t, "booking.pdf", "application/pdf")
	none := c.submitOK(t, env.token, append(standardParts(pdf, pagePart(t)), textPart("retention", "none")), nil)
	created, _ := time.Parse(time.RFC3339, none.CreatedAt)
	expires, _ := time.Parse(time.RFC3339, none.ExpiresAt)
	if expires.Sub(created) > 24*time.Hour || !expires.After(created) {
		t.Errorf("expires_at %s for created_at %s, want within 24 hours", none.ExpiresAt, none.CreatedAt)
	}
	if !env.remote {
		// A retention: default twin keeps its blobs: the path checked below is the right one.
		kept := c.submitOK(t, env.token, standardParts(pdf, pagePart(t)), nil)
		c.waitTerminal(t, env.token, kept.ID)
		if _, err := os.Stat(env.main.blobDir(kept.ID)); err != nil {
			t.Fatalf("a retention: default job has no blob directory at %s: %v", env.main.blobDir(kept.ID), err)
		}
		// The none job: wait for the blobs to go without reading the job.
		deadline := time.Now().Add(30 * time.Second)
		for {
			if _, err := os.Stat(env.main.blobDir(none.ID)); os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the document and pages of a retention: none job survive 30 s without a terminal read")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	first := c.waitTerminal(t, env.token, none.ID)
	if !env.remote && (first.Status != "accepted" || len(first.Result) == 0 || string(first.Result) == "null") {
		t.Errorf("first terminal read: %s, want the accepted result", first.raw)
	}
	r := c.get(t, env.token, none.ID)
	if r.status != http.StatusNotFound || r.errorCode() != "not_found" {
		t.Errorf("second read after the terminal state was read: %d %s, want 404 not_found", r.status, r.body)
	}
}

// TestItem09Idempotency: a repeated submission with the same Idempotency-Key returns
// the same job with Idempotency-Replayed: true and runs no model call.
func TestItem09Idempotency(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	key := "conformance-item09-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hdr := http.Header{"Idempotency-Key": {key}}
	parts := standardParts(textDocument(key))
	created := c.submitOK(t, env.token, parts, hdr)
	final := c.waitTerminal(t, env.token, created.ID)

	before := 0
	if !env.remote {
		before = providerCalls()
	}
	quotaBefore := c.quota(t, env.token).Remaining
	for range 2 {
		r := c.submit(t, env.token, parts, hdr)
		if r.status != http.StatusAccepted && r.status != http.StatusOK {
			t.Fatalf("replay: %d %s, want 202 or 200", r.status, r.body)
		}
		replayed := parseJob(t, r)
		if replayed.ID != created.ID || r.header.Get("Idempotency-Replayed") != "true" {
			t.Errorf("replay: id %s (want %s), Idempotency-Replayed %q", replayed.ID, created.ID, r.header.Get("Idempotency-Replayed"))
		}
		if replayed.Attempts != final.Attempts {
			t.Errorf("replay shows %d attempts, the job had %d", replayed.Attempts, final.Attempts)
		}
	}
	if !env.remote {
		settle()
		if after := providerCalls(); after != before {
			t.Errorf("the replays made %d provider calls", after-before)
		}
	}
	if after := c.quota(t, env.token).Remaining; after != quotaBefore {
		t.Logf("note: the replays changed the quota remaining from %d to %d", quotaBefore, after)
	}
}

// providerCalls is the main instance's provider usage counter: every call the fake
// provider received from it.
func providerCalls() int {
	if env.remote {
		return 0 // a deployment's provider usage cannot be observed from outside
	}
	return env.llm.count(env.main.name)
}

// blobCount counts the job directories of the main instance's blob store.
func blobCount(t *testing.T) int {
	t.Helper()
	if env.remote {
		return 0
	}
	n := 0
	shards, _ := os.ReadDir(filepath.Join(env.main.dataDir, "blobs"))
	for _, s := range shards {
		jobs, _ := os.ReadDir(filepath.Join(env.main.dataDir, "blobs", s.Name()))
		n += len(jobs)
	}
	return n
}
