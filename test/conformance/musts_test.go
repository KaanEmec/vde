package conformance

// The MUSTs of contract sections 2 to 12 that the checklist items do not already cover,
// one test per MUST or group of closely related MUSTs. Test names start with the section:
// TestS04… checks section 4. docs/conformance/travelinbox-v1.md maps each MUST sentence to
// its test.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// ---- §2 Conventions ----

// TestS02RequestID: X-Request-Id echoes the client's header, else is a fresh id (every
// other test checks the echo on every response through checkResponse).
func TestS02RequestID(t *testing.T) {
	c := mainClient()
	r := c.do(t, http.MethodGet, "/v1/quota", env.token, nil, "", http.Header{"X-Request-Id": {"conformance-echo-1"}})
	if r.header.Get("X-Request-Id") != "conformance-echo-1" {
		t.Errorf("X-Request-Id %q, want the echo", r.header.Get("X-Request-Id"))
	}
	for _, sent := range []string{"", strings.Repeat("x", 300)} {
		r := c.raw(t, http.MethodGet, "/v1/jobs/j_unknown", env.token, nil, "", http.Header{"X-Request-Id": {sent}})
		if got := r.header.Get("X-Request-Id"); got == "" || got == sent {
			t.Errorf("sent %.10q…: X-Request-Id %q, want a fresh id", sent, got)
		}
		var e errorBody
		_ = json.Unmarshal(r.body, &e)
		if e.Error.RequestID != "" && e.Error.RequestID != r.header.Get("X-Request-Id") {
			t.Errorf("error.request_id %q differs from the header %q", e.Error.RequestID, r.header.Get("X-Request-Id"))
		}
	}
}

// TestS02TimestampsRFC3339UTC: job and quota timestamps are RFC 3339 in UTC and ordered.
func TestS02TimestampsRFC3339UTC(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	created := c.submitOK(t, env.token, standardParts(textDocument("s02-timestamps")), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	if final.FinishedAt == nil {
		t.Fatal("finished_at is null in a terminal state")
	}
	stamps := []string{final.CreatedAt, *final.FinishedAt, final.ExpiresAt, c.quota(t, env.token).Reset}
	for _, s := range stamps {
		if !isRFC3339UTC(s) {
			t.Errorf("%q is not RFC 3339 UTC", s)
		}
	}
	c0, _ := time.Parse(time.RFC3339, final.CreatedAt)
	f0, _ := time.Parse(time.RFC3339, *final.FinishedAt)
	e0, _ := time.Parse(time.RFC3339, final.ExpiresAt)
	if f0.Before(c0) || !e0.After(f0) {
		t.Errorf("created %s, finished %s, expires %s are out of order", final.CreatedAt, *final.FinishedAt, final.ExpiresAt)
	}
	if created.FinishedAt != nil {
		t.Errorf("finished_at %s on a queued job", *created.FinishedAt)
	}
}

// TestS02HTTPSRequiredOutsideLoopback: behind a trusted proxy (the deployment shape where
// requests reach the service from outside loopback), a request not marked as HTTPS is
// refused and one marked https is served. A direct non-loopback peer cannot be produced
// from a test on one machine; internal/api TestRequireHTTPS covers it.
func TestS02HTTPSRequiredOutsideLoopback(t *testing.T) {
	inst := newInstance(t, instanceOptions{name: "proxy", trustedProxy: true})
	c := inst.client()
	tok := inst.tokens[appDev]
	if r := c.raw(t, http.MethodGet, "/v1/quota", tok, nil, "", nil); r.status != http.StatusBadRequest || r.errorCode() != "bad_request" {
		t.Errorf("plain HTTP through the proxy: %d %s, want 400 bad_request", r.status, r.body)
	}
	if r := c.raw(t, http.MethodGet, "/v1/quota", tok, nil, "", http.Header{"X-Forwarded-Proto": {"http"}}); r.status != http.StatusBadRequest {
		t.Errorf("X-Forwarded-Proto http: %d, want 400", r.status)
	}
	c.header = http.Header{"X-Forwarded-Proto": {"https"}}
	if r := c.do(t, http.MethodGet, "/v1/quota", tok, nil, "", nil); r.status != http.StatusOK {
		t.Errorf("X-Forwarded-Proto https: %d %s, want 200", r.status, r.body)
	}
}

// TestS02ErrorMessagesQuoteNoContent: error messages, of refused requests and of failed
// jobs, never quote document contents, instructions, schema or extraction output.
func TestS02ErrorMessagesQuoteNoContent(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	const marker = "CONFMARK-ERRMSG-7f3a"
	doc := part{"document", marker + ".txt", "text/plain", []byte(marker + " document text")}
	withMarker := func(parts []part) []part {
		parts = replacePart(parts, "extractor_instructions", part{"extractor_instructions", "", "text/markdown", []byte(marker + " instructions")})
		return replacePart(parts, "judge_instructions", part{"judge_instructions", "", "text/markdown", []byte(marker + " judge")})
	}
	var replies []reply
	replies = append(replies,
		c.submit(t, env.token, replacePart(withMarker(standardParts(doc)), "schema",
			part{"schema", "", "application/schema+json", []byte(`{"type": "` + marker + `"}`)}), nil),
		c.submit(t, env.token, withMarker(standardParts(part{"document", marker, "application/x-" + strings.ToLower(marker), []byte(marker)})), nil),
		c.submit(t, env.token, append(withMarker(standardParts(doc)), textPart("retention", marker)), nil),
		c.submit(t, env.token, append(withMarker(standardParts(doc)), textPart("max_attempts", marker)), nil),
		c.get(t, env.token, marker),
	)
	for _, r := range replies {
		if r.status < 400 {
			t.Errorf("a malformed request was accepted: %d %s", r.status, r.body)
		}
		if bytes.Contains(r.body, []byte(marker)) {
			t.Errorf("error body quotes the request: %s", r.body)
		}
	}
	if env.remote {
		return
	}
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{
		marker: func(c llmCall) llmReply {
			if c.Role == roleExtractor {
				return llmReply{Text: candidate(marker + " candidate")}
			}
			return llmReply{Text: marker + " is not a verdict"}
		},
	}))
	created := c.submitOK(t, env.token, withMarker(standardParts(doc)), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	if final.Error == nil || strings.Contains(final.Error.Message, marker) {
		t.Errorf("failed job error %+v: want an error whose message quotes nothing", final.Error)
	}
}

// ---- §3 Authentication and applications ----

// TestS03TokenRefusals: a missing, malformed, unknown or expired token is refused with
// 401 before any provider call (revoked: TestItem02Unauthenticated).
func TestS03TokenRefusals(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	body, ct := encodeMultipart(t, standardParts(textDocument("s03-refusals")))
	before := 0
	if !env.remote {
		before = providerCalls()
	}
	cases := map[string]http.Header{
		"basic scheme":   {"Authorization": {"Basic dXNlcjpwYXNz"}},
		"empty bearer":   {"Authorization": {"Bearer "}},
		"two headers":    {"Authorization": {"Bearer " + env.token, "Bearer " + env.token}},
		"unknown token":  {"Authorization": {"Bearer vde_unknown_0000000000000000000000000000"}},
		"token in query": nil,
	}
	for name, hdr := range cases {
		path := "/v1/jobs"
		if name == "token in query" {
			path += "?access_token=" + env.token
		}
		r := c.do(t, http.MethodPost, path, "", body, ct, hdr)
		if r.status != http.StatusUnauthorized || r.errorCode() != "unauthorized" {
			t.Errorf("%s: %d %s, want 401 unauthorized", name, r.status, r.body)
		}
	}
	if !env.remote {
		tok, _, err := env.main.createToken(appDev, "jobs", "2s")
		if err != nil {
			t.Fatal(err)
		}
		if r := c.do(t, http.MethodGet, "/v1/quota", tok, nil, "", nil); r.status != http.StatusOK {
			t.Fatalf("fresh token: %d", r.status)
		}
		time.Sleep(2500 * time.Millisecond)
		if r := c.do(t, http.MethodPost, "/v1/jobs", tok, body, ct, nil); r.status != http.StatusUnauthorized {
			t.Errorf("expired token: %d %s, want 401", r.status, r.body)
		}
		settle()
		if after := providerCalls(); after != before {
			t.Errorf("refused requests made %d provider calls", after-before)
		}
	}
}

// TestS03ScopeForbidden: a valid token used outside its scope gets 403 (a jobs:read
// token may poll and read the quota, not submit).
func TestS03ScopeForbidden(t *testing.T) {
	requireLocal(t, "creates a token with the CLI")
	c := mainClient()
	loadInputs(t)
	tok, _, err := env.main.createToken(appDev, "jobs:read", "1d")
	if err != nil {
		t.Fatal(err)
	}
	before := providerCalls()
	if r := c.submit(t, tok, standardParts(textDocument("s03-scope")), nil); r.status != http.StatusForbidden || r.errorCode() != "forbidden" {
		t.Errorf("submit with jobs:read: %d %s, want 403 forbidden", r.status, r.body)
	}
	if r := c.do(t, http.MethodGet, "/v1/quota", tok, nil, "", nil); r.status != http.StatusOK {
		t.Errorf("quota with jobs:read: %d, want 200", r.status)
	}
	settle()
	if providerCalls() != before {
		t.Error("a forbidden submission reached the provider")
	}
}

// TestS03TokenStoredHashed: the service stores only a hash of each token: the token
// does not appear in any file of the data directory after it was created and used.
func TestS03TokenStoredHashed(t *testing.T) {
	requireLocal(t, "reads the data directory")
	tok, _, err := env.main.createToken(appDev, "jobs", "1d")
	if err != nil {
		t.Fatal(err)
	}
	mainClient().quota(t, tok)
	for _, hit := range findInDir(t, env.main.dataDir, tok) {
		t.Errorf("the plain token is stored in %s", hit)
	}
}

// TestS03TokenLifecycle: tokens carry an expiry, several are active at once for rotation,
// and revocation takes effect on the next request.
func TestS03TokenLifecycle(t *testing.T) {
	requireLocal(t, "uses the token CLI")
	c := mainClient()
	if _, _, err := env.main.cli("token", "create", "--config", env.main.config, "--app", appDev, "--scope", "jobs"); err == nil {
		t.Error("vde token create without --expires succeeded")
	}
	oldTok, oldID, err := env.main.createToken(appDev, "jobs", "30d")
	if err != nil {
		t.Fatal(err)
	}
	newTok, _, err := env.main.createToken(appDev, "jobs", "60d")
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{oldTok, newTok} {
		if r := c.do(t, http.MethodGet, "/v1/quota", tok, nil, "", nil); r.status != http.StatusOK {
			t.Errorf("an active token: %d, want 200", r.status)
		}
	}
	if _, stderr, err := env.main.cli("token", "revoke", "--config", env.main.config, oldID); err != nil {
		t.Fatalf("revoke: %v %s", err, stderr)
	}
	if r := c.do(t, http.MethodGet, "/v1/quota", oldTok, nil, "", nil); r.status != http.StatusUnauthorized {
		t.Errorf("revoked token, next request: %d, want 401", r.status)
	}
	if r := c.do(t, http.MethodGet, "/v1/quota", newTok, nil, "", nil); r.status != http.StatusOK {
		t.Errorf("the rotated-in token after the revocation: %d, want 200", r.status)
	}
}

// TestS03ForeignJobIs404: another application's token gets 404, not 403, the same answer
// as for a job that does not exist.
func TestS03ForeignJobIs404(t *testing.T) {
	if env.foreignToken == "" {
		t.Skip("VDE_CONFORMANCE_FOREIGN_TOKEN is not set")
	}
	c := mainClient()
	loadInputs(t)
	created := c.submitOK(t, env.token, standardParts(textDocument("s03-foreign")), nil)
	c.waitTerminal(t, env.token, created.ID)
	foreign := c.get(t, env.foreignToken, created.ID)
	unknown := c.get(t, env.foreignToken, "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB")
	if foreign.status != http.StatusNotFound || unknown.status != http.StatusNotFound {
		t.Fatalf("foreign %d, unknown %d, want 404 and 404", foreign.status, unknown.status)
	}
	var a, b errorBody
	_ = json.Unmarshal(foreign.body, &a)
	_ = json.Unmarshal(unknown.body, &b)
	if a.Error.Code != b.Error.Code || a.Error.Message != b.Error.Message {
		t.Errorf("a foreign job (%s) is distinguishable from a missing one (%s)", foreign.body, unknown.body)
	}
	if r := c.get(t, env.token, created.ID); r.status != http.StatusOK {
		t.Errorf("the owner's read after a foreign read: %d", r.status)
	}
}

// ---- §4 Submitting a job ----

// TestS04SubmitResponse: 202 with Location /v1/jobs/{id} and the job resource with
// status queued (submitOK checks it on every submission of the suite).
func TestS04SubmitResponse(t *testing.T) {
	loadInputs(t)
	j := mainClient().submitOK(t, env.token, standardParts(textDocument("s04-submit")), nil)
	if j.Attempts != 0 || j.Result != nil && string(j.Result) != "null" || j.Error != nil {
		t.Errorf("a queued job: %s", j.raw)
	}
}

// TestS04MalformedRequests400: malformed multipart, a required part missing or repeated,
// an unreadable retention or max_attempts answer 400 bad_request, before any model call.
func TestS04MalformedRequests400(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	good := standardParts(textDocument("s04-malformed"))
	cases := map[string][]part{
		"no document":               replacePart(good, "document", part{}),
		"no extractor_instructions": replacePart(good, "extractor_instructions", part{}),
		"no judge_instructions":     replacePart(good, "judge_instructions", part{}),
		"two documents":             append(slices.Clone(good), textDocument("again")),
		"two schemas":               append(slices.Clone(good), part{"schema", "", "application/schema+json", inputs.schema}),
		"two judge_instructions":    append(slices.Clone(good), part{"judge_instructions", "", "text/plain", []byte("x")}),
		"retention forever":         append(slices.Clone(good), textPart("retention", "forever")),
		"two retentions":            append(slices.Clone(good), textPart("retention", "none"), textPart("retention", "none")),
		"max_attempts zero":         append(slices.Clone(good), textPart("max_attempts", "0")),
		"max_attempts negative":     append(slices.Clone(good), textPart("max_attempts", "-1")),
		"max_attempts word":         append(slices.Clone(good), textPart("max_attempts", "three")),
	}
	before := 0
	if !env.remote {
		before = providerCalls()
	}
	for name, parts := range cases {
		if r := c.submit(t, env.token, parts, nil); r.status != http.StatusBadRequest || r.errorCode() != "bad_request" {
			t.Errorf("%s: %d %s, want 400 bad_request", name, r.status, r.body)
		}
	}
	body, ct := encodeMultipart(t, good)
	for name, req := range map[string]struct {
		body []byte
		ct   string
	}{
		"truncated multipart": {body[:len(body)/2], ct},
		"not multipart":       {[]byte(`{"document": "x"}`), "application/json"},
		"no boundary":         {body, "multipart/form-data"},
	} {
		if r := c.do(t, http.MethodPost, "/v1/jobs", env.token, req.body, req.ct, nil); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, r.status, r.body)
		}
	}
	if !env.remote {
		settle()
		if after := providerCalls(); after != before {
			t.Errorf("malformed requests made %d provider calls", after-before)
		}
	}
}

// TestS04MediaTypes: the five MUST media types are accepted (TestItem03DocumentKinds
// runs each to a terminal state); any other answers 415 unsupported_media_type.
func TestS04MediaTypes(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	for _, ct := range []string{"application/zip", "application/msword", "video/mp4", "application/octet-stream"} {
		r := c.submit(t, env.token, standardParts(part{"document", "doc.bin", ct, []byte("PK\x03\x04 not a document")}), nil)
		if r.status != http.StatusUnsupportedMediaType || r.errorCode() != "unsupported_media_type" {
			t.Errorf("%s: %d %s, want 415 unsupported_media_type", ct, r.status, r.body)
		}
	}
	for _, d := range []part{
		sampleDocument(t, "booking.txt", "text/plain"),
		sampleDocument(t, "booking.html", "text/html"),
		sampleDocument(t, "booking.pdf", "application/pdf"),
		sampleDocument(t, "booking.png", "image/png"),
		sampleDocument(t, "receipt.jpg", "image/jpeg"),
	} {
		if env.remote {
			break // TestItem03DocumentKinds runs them against a deployment
		}
		c.submitOK(t, env.token, standardParts(d), nil)
	}
}

const (
	mib = 1 << 20
	kib = 1 << 10
)

// padded returns data padded with pad to exactly n bytes.
func padded(data []byte, pad byte, n int) []byte {
	out := make([]byte, n)
	copy(out, data)
	for i := len(data); i < n; i++ {
		out[i] = pad
	}
	return out
}

// TestS04LimitsAllowed: a request at every limit the service MUST allow is accepted: a
// 25 MiB document, 20 pages of which 10 are 10 MiB, 64 KiB instructions, a 256 KiB
// schema, about 125.7 MiB in all (the whole-body limit is 128 MiB).
func TestS04LimitsAllowed(t *testing.T) {
	requireLocal(t, "a 25 MiB job against a deployment costs real provider calls")
	c := mainClient()
	loadInputs(t)
	doc := part{"document", "big.txt", "text/plain", bytes.Repeat([]byte("Harbour View Hotel HV-48213. "), 25*mib/29+1)[:25*mib]}
	parts := []part{doc}
	for i := range 20 {
		data := inputs.png
		if i < 10 {
			data = padded(inputs.png, 0, 10*mib)
		}
		parts = append(parts, part{"page", "p.png", "image/png", data})
	}
	parts = append(parts,
		part{"extractor_instructions", "", "text/markdown", padded(inputs.extractor, '\n', 64*kib)},
		part{"judge_instructions", "", "text/markdown", padded(inputs.judge, '\n', 64*kib)},
		part{"schema", "", "application/schema+json", padded(inputs.schema, ' ', 256*kib)},
	)
	created := c.submitOK(t, env.token, parts, nil)
	if final := c.waitTerminal(t, env.token, created.ID); final.Status != "accepted" {
		t.Errorf("a job at the limits ended %s: %v", final.Status, final.Error)
	}
}

// TestS04LimitsExceeded413: over any limit the answer is 413 payload_too_large, and the
// client sees the status after sending its whole request rather than a reset connection.
func TestS04LimitsExceeded413(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	base := standardParts(textDocument("s04-limits"))
	pages := func(n int) []part {
		var ps []part
		for range n {
			ps = append(ps, part{"page", "p.png", "image/png", inputs.png})
		}
		return ps
	}
	cases := map[string][]part{
		"document 25 MiB + 1": replacePart(base, "document", part{"document", "big.txt", "text/plain", bytes.Repeat([]byte("a"), 25*mib+1)}),
		"page 10 MiB + 1":     append(slices.Clone(base), part{"page", "p.png", "image/png", padded(inputs.png, 0, 10*mib+1)}),
		"21 pages":            append(slices.Clone(base), pages(21)...),
		"extractor_instructions 64 KiB + 1": replacePart(base, "extractor_instructions",
			part{"extractor_instructions", "", "text/markdown", padded(inputs.extractor, '\n', 64*kib+1)}),
		"judge_instructions 64 KiB + 1": replacePart(base, "judge_instructions",
			part{"judge_instructions", "", "text/markdown", padded(inputs.judge, '\n', 64*kib+1)}),
		"schema 256 KiB + 1": replacePart(base, "schema",
			part{"schema", "", "application/schema+json", padded(inputs.schema, ' ', 256*kib+1)}),
	}
	if !env.remote {
		var big []part // 13 pages of 10 MiB: every part within its limit, the body over 128 MiB
		for range 13 {
			big = append(big, part{"page", "p.png", "image/png", padded(inputs.png, 0, 10*mib)})
		}
		cases["request body over 128 MiB"] = append(slices.Clone(base), big...)
	}
	before := 0
	if !env.remote {
		before = providerCalls()
	}
	for name, parts := range cases {
		body, ct := encodeMultipart(t, parts)
		req, err := http.NewRequest(http.MethodPost, c.base+"/v1/jobs", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+env.token)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("X-Request-Id", "conf-413")
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Errorf("%s: the client saw %v instead of a status", name, err)
			continue
		}
		data, _ := readAllClose(resp)
		r := reply{status: resp.StatusCode, header: resp.Header, body: data, requestID: "conf-413"}
		checkResponse(t, "/v1/jobs", env.token, r)
		if r.status != http.StatusRequestEntityTooLarge || r.errorCode() != "payload_too_large" {
			t.Errorf("%s: %d %s, want 413 payload_too_large", name, r.status, r.body)
		}
	}
	if !env.remote {
		settle()
		if after := providerCalls(); after != before {
			t.Errorf("oversized requests made %d provider calls", after-before)
		}
	}
}

// TestS04RepresentationNativeOrPages: the service decides the representation from its
// configuration: a PDF goes to the models natively when the extractor's endpoints accept
// PDF, as the page images otherwise; with neither readable the submission is refused 415
// unsupported_document. Both roles read the same bytes. The instance without a PDF reader
// runs documents.pdf.mode native, the setting that keeps 4.11 exactly: under the default
// auto the service would read the PDF's text layer itself (ADR 0010).
func TestS04RepresentationNativeOrPages(t *testing.T) {
	requireLocal(t, "configures what the models accept")
	loadInputs(t)
	pdf := sampleDocument(t, "booking.pdf", "application/pdf")
	pdfData := base64.StdEncoding.EncodeToString(inputs.pdf)
	pngData := base64.StdEncoding.EncodeToString(inputs.png)

	start := providerCalls()
	created := mainClient().submitOK(t, env.token, standardParts(pdf, pagePart(t)), nil)
	mainClient().waitTerminal(t, env.token, created.ID)
	for _, call := range env.llm.calls(env.main.name, start) {
		if len(call.Blobs) != 1 || !strings.HasSuffix(call.Blobs[0], pdfData) {
			t.Errorf("%s with PDF accepted: %d binary parts, want the PDF itself", call.Role, len(call.Blobs))
		}
	}

	inst := newInstance(t, instanceOptions{name: "no-pdf", accepts: []string{"image/png", "image/jpeg", "text/plain", "text/html"}, pdfMode: "native"})
	c := inst.client()
	created = c.submitOK(t, inst.tokens[appDev], standardParts(pdf, pagePart(t)), nil)
	if final := c.waitTerminal(t, inst.tokens[appDev], created.ID); final.Status != "accepted" {
		t.Errorf("PDF with a page, PDF not accepted: %s", final.raw)
	}
	calls := env.llm.calls(inst.name, 0)
	if len(calls) != 2 {
		t.Fatalf("%d calls, want an extractor and a judge call", len(calls))
	}
	for _, call := range calls {
		if len(call.Blobs) != 1 || !strings.HasSuffix(call.Blobs[0], pngData) {
			t.Errorf("%s: %d binary parts, want the page image", call.Role, len(call.Blobs))
		}
	}
	r := c.submit(t, inst.tokens[appDev], standardParts(pdf), nil)
	if r.status != http.StatusUnsupportedMediaType || r.errorCode() != "unsupported_document" {
		t.Errorf("PDF without pages, PDF not accepted: %d %s, want 415 unsupported_document", r.status, r.body)
	}
}

// TestS04UnsupportedDocument: a media type the service accepts but no configured
// endpoint reads, without pages, is refused at submit with 415 unsupported_document.
func TestS04UnsupportedDocument(t *testing.T) {
	requireLocal(t, "depends on the configured models")
	webp := padded([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), 0, 64)
	before := providerCalls()
	r := mainClient().submit(t, env.token, standardParts(part{"document", "photo.webp", "image/webp", webp}), nil)
	if r.status != http.StatusUnsupportedMediaType || r.errorCode() != "unsupported_document" {
		t.Errorf("webp with no model reading it: %d %s, want 415 unsupported_document", r.status, r.body)
	}
	settle()
	if providerCalls() != before {
		t.Error("an unsupported document reached the provider")
	}
}

// TestS04IdempotencyMismatch: the same Idempotency-Key with a different body is 422
// idempotency_mismatch and runs nothing.
func TestS04IdempotencyMismatch(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	key := "conformance-mismatch-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hdr := http.Header{"Idempotency-Key": {key}}
	created := c.submitOK(t, env.token, standardParts(textDocument(key)), hdr)
	c.waitTerminal(t, env.token, created.ID)
	before := 0
	if !env.remote {
		before = providerCalls()
	}
	for name, parts := range map[string][]part{
		"other document":  standardParts(textDocument(key + "-changed")),
		"added retention": append(standardParts(textDocument(key)), textPart("retention", "none")),
	} {
		if r := c.submit(t, env.token, parts, hdr); r.status != http.StatusUnprocessableEntity || r.errorCode() != "idempotency_mismatch" {
			t.Errorf("%s: %d %s, want 422 idempotency_mismatch", name, r.status, r.body)
		}
	}
	if !env.remote {
		settle()
		if providerCalls() != before {
			t.Error("a mismatched replay reached the provider")
		}
	}
}

// TestS04IdempotencyReplayAfterFirstRead: a replay of a retention: none job whose
// terminal state was read still names the same job and runs nothing. Its body is the
// content-deleted form (docs/api/guide.md, docs/conformance/travelinbox-v1-review.md):
// status kept, result null, feedback empty and content_deleted_at set, which tells the
// client the result was delivered and must not be re-submitted for.
func TestS04IdempotencyReplayAfterFirstRead(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	key := "conformance-replay-none-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hdr := http.Header{"Idempotency-Key": {key}}
	parts := append(standardParts(textDocument(key)), textPart("retention", "none"))
	created := c.submitOK(t, env.token, parts, hdr)
	final := c.waitTerminal(t, env.token, created.ID) // the first terminal read deletes the content
	before := 0
	if !env.remote {
		before = providerCalls()
	}
	r := c.submit(t, env.token, parts, hdr)
	if r.status != http.StatusAccepted {
		t.Fatalf("replay after the first read: %d %s, want 202", r.status, r.body)
	}
	replayed := parseJob(t, r)
	if replayed.ID != created.ID || r.header.Get("Idempotency-Replayed") != "true" {
		t.Errorf("replay: id %s (want %s), Idempotency-Replayed %q", replayed.ID, created.ID, r.header.Get("Idempotency-Replayed"))
	}
	if replayed.Status != final.Status {
		t.Errorf("replayed status %q, want the terminal %q", replayed.Status, final.Status)
	}
	if replayed.ContentDeletedAt == nil || *replayed.ContentDeletedAt == "" {
		t.Errorf("replay of a content-deleted job without content_deleted_at: %s", r.body)
	}
	if len(replayed.Result) != 0 && string(replayed.Result) != "null" || len(replayed.Feedback) != 0 {
		t.Errorf("replay of a content-deleted job carries content: %s", r.body)
	}
	if !env.remote {
		settle()
		if providerCalls() != before {
			t.Error("the replay reached the provider")
		}
	}
}

// TestS04IdempotencyWindowOutlivesShortRetention: a repeat within 24 hours is the same
// job even when the operator keeps retention: default jobs for less than 24 hours.
func TestS04IdempotencyWindowOutlivesShortRetention(t *testing.T) {
	inst := newInstance(t, instanceOptions{name: "short-retention", retention: "2s", sweep: "1s"})
	c := inst.client()
	loadInputs(t)
	tok := inst.tokens[appDev]
	key := "conformance-window-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hdr := http.Header{"Idempotency-Key": {key}}
	parts := standardParts(textDocument(key))
	created := c.submitOK(t, tok, parts, hdr)
	c.waitTerminal(t, tok, created.ID)
	time.Sleep(4 * time.Second) // past the retention and at least one sweep
	before := env.llm.count(inst.name)
	r := c.submit(t, tok, parts, hdr)
	if r.status == http.StatusAccepted && r.header.Get("Idempotency-Replayed") != "true" {
		j := parseJob(t, r)
		t.Cleanup(func() { c.drain(tok, j.ID) })
	}
	if r.status != http.StatusAccepted || r.header.Get("Idempotency-Replayed") != "true" || parseJob(t, r).ID != created.ID {
		t.Errorf("repeat within 24 hours: %d, Idempotency-Replayed %q: %s", r.status, r.header.Get("Idempotency-Replayed"), r.body)
	}
	if j := parseJob(t, r); j.ContentDeletedAt == nil {
		t.Errorf("replay past a 2 s retention without content_deleted_at: %s", r.body)
	}
	settle()
	if n := env.llm.count(inst.name) - before; n != 0 {
		t.Errorf("the repeat made %d provider calls", n)
	}
	if r := c.get(t, tok, created.ID); r.status != http.StatusNotFound {
		t.Errorf("poll past the retention: %d, want 404", r.status)
	}
}

// TestS04IdempotencyKeyScopedPerApplication: the same key from another application is
// a new job of that application.
func TestS04IdempotencyKeyScopedPerApplication(t *testing.T) {
	if env.foreignToken == "" {
		t.Skip("VDE_CONFORMANCE_FOREIGN_TOKEN is not set")
	}
	c := mainClient()
	loadInputs(t)
	key := "conformance-scoped-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hdr := http.Header{"Idempotency-Key": {key}}
	parts := standardParts(textDocument(key))
	a := c.submitOK(t, env.token, parts, hdr)
	r := c.submit(t, env.foreignToken, parts, hdr)
	if r.status != http.StatusAccepted || r.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the other application's first use of the key: %d, Idempotency-Replayed %q", r.status, r.header.Get("Idempotency-Replayed"))
	}
	b := parseJob(t, r)
	t.Cleanup(func() { c.drain(env.foreignToken, b.ID) })
	if a.ID == b.ID {
		t.Error("two applications share a job through one key")
	}
}

// ---- §5 The job resource ----

// TestS05TerminalStatusStable: a terminal status never changes; repeated reads of a
// retention: default job return the same job.
func TestS05TerminalStatusStable(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	created := c.submitOK(t, env.token, standardParts(textDocument("s05-stable")), nil)
	first := c.waitTerminal(t, env.token, created.ID)
	for range 3 {
		time.Sleep(50 * time.Millisecond)
		again := parseJob(t, c.get(t, env.token, created.ID))
		if again.Status != first.Status || !sameJSON(again.raw, first.raw) {
			t.Errorf("terminal job changed from %s to %s", first.raw, again.raw)
		}
	}
}

// TestS05NotFound: a job that does not exist is 404 not_found (foreign:
// TestS03ForeignJobIs404, expired by retention: TestItem08RetentionNone).
func TestS05NotFound(t *testing.T) {
	c := mainClient()
	for _, id := range []string{"j_01JA3V9Q6ZK7M4W8X2R5T1N0HB", "nope", "..%2F..%2Fquota", strings.Repeat("j", 200)} {
		if r := c.get(t, env.token, id); r.status != http.StatusNotFound || r.errorCode() != "not_found" {
			t.Errorf("GET /v1/jobs/%.20s: %d %s, want 404 not_found", id, r.status, r.body)
		}
	}
}

// TestS05PollingNotCharged: polls and quota reads do not count against the quota.
func TestS05PollingNotCharged(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	created := c.submitOK(t, env.token, standardParts(textDocument("s05-polling")), nil)
	c.waitTerminal(t, env.token, created.ID)
	before := c.quota(t, env.token)
	for range 10 {
		c.get(t, env.token, created.ID)
		c.get(t, env.token, "j_unknown")
	}
	if after := c.quota(t, env.token); after.Remaining != before.Remaining {
		t.Errorf("polls changed the quota remaining from %d to %d", before.Remaining, after.Remaining)
	}
}

// TestS05AcceptedOnlyOnJudgeAccept: accepted means the judge accepted a schema-valid
// result without error; a judge that rejects, answers garbage or rejects without a
// reason never produces accepted. The accepted result is the candidate as produced.
func TestS05AcceptedOnlyOnJudgeAccept(t *testing.T) {
	requireLocal(t, "scripts the judge")
	c := mainClient()
	loadInputs(t)
	judge := map[string]string{
		"s05-judge-accepts":          accept,
		"s05-judge-rejects":          reject("/notes", "The document does not say this."),
		"s05-judge-garbage":          "Looks fine to me!",
		"s05-judge-reject-no-reason": `{"verdict": "reject", "feedback": {}}`,
		"s05-judge-accept-typo":      `{"verdict": "accepted", "feedback": {}}`,
	}
	per := map[string]func(llmCall) llmReply{}
	for note, answer := range judge {
		per[note] = func(c llmCall) llmReply {
			if c.Role == roleExtractor {
				return llmReply{Text: candidate(note)}
			}
			return llmReply{Text: answer}
		}
	}
	env.llm.setScript(t, env.main.name, scriptPerNote(per))
	for note := range judge {
		created := c.submitOK(t, env.token, append(standardParts(textDocument(note)), textPart("max_attempts", "1")), nil)
		final := c.waitTerminal(t, env.token, created.ID)
		if note == "s05-judge-accepts" {
			if final.Status != "accepted" || final.Error != nil || len(final.Feedback) != 0 || !sameJSON(final.Result, []byte(candidate(note))) {
				t.Errorf("%s: %s", note, final.raw)
			}
			continue
		}
		if final.Status == "accepted" {
			t.Errorf("%s: accepted without an accept verdict: %s", note, final.raw)
		}
		if !sameJSON(final.Result, []byte(candidate(note))) {
			t.Errorf("%s: result %s, want the candidate kept", note, final.Result)
		}
	}
}

// ---- §6 The extraction loop ----

// TestS06RefusalsMakeNoModelCall: a request refused for its content or its quota makes
// no model call.
func TestS06RefusalsMakeNoModelCall(t *testing.T) {
	requireLocal(t, "reads the provider usage counter")
	c := mainClient()
	loadInputs(t)
	quotaTok := env.main.tokens[appQuota]
	for range quotaLimit + 1 { // spend the quota application's quota, if not spent yet
		r := c.submit(t, quotaTok, standardParts(textDocument("s06-spend")), nil)
		if r.status != http.StatusAccepted {
			break
		}
		c.waitTerminal(t, quotaTok, parseJob(t, r).ID)
	}
	before := providerCalls()
	refused := []reply{
		c.submit(t, env.token, replacePart(standardParts(textDocument("s06-refused")), "schema",
			part{"schema", "", "application/schema+json", []byte(`{"type": 42}`)}), nil),
		c.submit(t, env.token, replacePart(standardParts(textDocument("s06-refused")), "schema",
			part{"schema", "", "application/schema+json", []byte(`not json`)}), nil),
		c.submit(t, env.token, standardParts(part{"document", "x.zip", "application/zip", []byte("PK")}), nil),
		c.submit(t, quotaTok, standardParts(textDocument("s06-refused")), nil),
	}
	for _, r := range refused {
		if r.status < 400 {
			t.Errorf("expected a refusal, got %d %s", r.status, r.body)
		}
	}
	if refused[0].errorCode() != "invalid_schema" || refused[1].errorCode() != "invalid_schema" || refused[3].errorCode() != "quota_exhausted" {
		t.Errorf("codes %s %s %s, want invalid_schema twice and quota_exhausted",
			refused[0].errorCode(), refused[1].errorCode(), refused[3].errorCode())
	}
	settle()
	if after := providerCalls(); after != before {
		t.Errorf("refused requests made %d provider calls", after-before)
	}
}

// promptOrder checks that the needles appear in the call's user content in order, each
// verbatim and complete.
func promptOrder(t *testing.T, what string, call llmCall, needles ...string) {
	t.Helper()
	content := strings.Join(call.Texts, "\n")
	at := -1
	for i, n := range needles {
		idx := strings.Index(content, n)
		if idx < 0 {
			t.Errorf("%s: input %d (%.40q…) is not in the prompt verbatim", what, i+1, n)
			continue
		}
		if idx < at {
			t.Errorf("%s: input %d (%.40q…) comes before the previous one", what, i+1, n)
		}
		at = idx
	}
}

// TestS06ExtractorPromptCarriesInputsVerbatim: the extractor prompt has the service's
// framing first, then the caller's extractor_instructions verbatim and complete, the
// schema, the document and, on a retry, the previous candidate and the feedback on it.
func TestS06ExtractorPromptCarriesInputsVerbatim(t *testing.T) {
	requireLocal(t, "reads the prompts the provider received")
	c := mainClient()
	loadInputs(t)
	note := "s06-extractor-prompt"
	doc := textDocument(note)
	first := candidate(note + " first")
	const why = "The booking reference is HV-48213, not \"HV-48231\"; see line 2."
	var judged int
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{note: func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			if c.contains(why) {
				return llmReply{Text: candidate(note + " second")}
			}
			return llmReply{Text: first}
		}
		judged++
		if judged == 1 {
			return llmReply{Text: reject("/notes", why)}
		}
		return llmReply{Text: accept}
	}}))
	start := providerCalls()
	created := c.submitOK(t, env.token, standardParts(doc), nil)
	if final := c.waitTerminal(t, env.token, created.ID); final.Status != "accepted" || final.Attempts != 2 {
		t.Fatalf("job: %s, want accepted on attempt 2", final.raw)
	}
	ex := byRole(env.llm.calls(env.main.name, start), roleExtractor)
	if len(ex) != 2 {
		t.Fatalf("%d extractor calls, want 2", len(ex))
	}
	for i, call := range ex {
		if strings.TrimSpace(call.System) == "" || strings.Contains(call.System, string(inputs.extractor)) {
			t.Errorf("call %d: the framing is missing from the system prompt or mixed with the caller's instructions", i+1)
		}
		promptOrder(t, "extractor call "+strconv.Itoa(i+1), call, string(inputs.extractor), string(inputs.schema), string(doc.data))
		if call.contains(string(inputs.judge)) {
			t.Errorf("call %d carries the judge instructions", i+1)
		}
	}
	promptOrder(t, "extractor retry", ex[1], string(doc.data), first, why)
}

// TestS06JudgePromptCarriesInputsVerbatim: the judge prompt has the judge framing, the
// caller's judge_instructions verbatim, the schema, the same document and the candidate.
func TestS06JudgePromptCarriesInputsVerbatim(t *testing.T) {
	requireLocal(t, "reads the prompts the provider received")
	c := mainClient()
	loadInputs(t)
	doc := textDocument("s06-judge-prompt")
	start := providerCalls()
	created := c.submitOK(t, env.token, standardParts(doc), nil)
	c.waitTerminal(t, env.token, created.ID)
	ju := byRole(env.llm.calls(env.main.name, start), roleJudge)
	if len(ju) != 1 {
		t.Fatalf("%d judge calls, want 1", len(ju))
	}
	if strings.TrimSpace(ju[0].System) == "" {
		t.Error("no judge framing")
	}
	promptOrder(t, "judge call", ju[0], string(inputs.judge), string(inputs.schema), string(doc.data),
		candidate("conformance default candidate"))
	if ju[0].contains(string(inputs.extractor)) {
		t.Error("the judge prompt carries the extractor instructions")
	}
}

// TestS06NoJSONRepair: an answer that is not JSON after removing code fences and prose
// is not repaired: it counts as an attempt with schema feedback, never reaches the
// judge, and with no parseable attempt the job fails with extractor_failed and result null.
func TestS06NoJSONRepair(t *testing.T) {
	requireLocal(t, "scripts the extractor")
	c := mainClient()
	loadInputs(t)
	note := "s06-no-repair"
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{note: func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			return llmReply{Text: `{"schema_version": 1, "reservations": [],}`} // a trailing comma
		}
		return llmReply{Text: accept}
	}}))
	start := providerCalls()
	created := c.submitOK(t, env.token, append(standardParts(textDocument(note)), textPart("max_attempts", "2")), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	if final.Status != "failed" || final.errorCode() != "extractor_failed" || final.Attempts != 2 ||
		(len(final.Result) != 0 && string(final.Result) != "null") {
		t.Errorf("job: %s, want failed extractor_failed after 2 attempts with result null", final.raw)
	}
	if len(final.Feedback) == 0 || final.Feedback[0].Source != "schema" {
		t.Errorf("feedback %+v, want schema feedback", final.Feedback)
	}
	if n := len(byRole(env.llm.calls(env.main.name, start), roleJudge)); n != 0 {
		t.Errorf("%d judge calls on unparseable answers", n)
	}
}

// TestS06JudgeUnparseableTwice: an unparseable verdict is asked for once more in the same
// attempt; failing twice, the job is failed with judge_failed, never accepted, with the
// candidate kept.
func TestS06JudgeUnparseableTwice(t *testing.T) {
	requireLocal(t, "scripts the judge")
	c := mainClient()
	loadInputs(t)
	note := "s06-judge-garbage"
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{note: func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			return llmReply{Text: candidate(note)}
		}
		return llmReply{Text: "I would accept this."}
	}}))
	start := providerCalls()
	created := c.submitOK(t, env.token, standardParts(textDocument(note)), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	calls := env.llm.calls(env.main.name, start)
	if final.Status != "failed" || final.errorCode() != "judge_failed" || final.Attempts != 1 ||
		!sameJSON(final.Result, []byte(candidate(note))) {
		t.Errorf("job: %s, want failed judge_failed on attempt 1 with the candidate", final.raw)
	}
	if ex, ju := len(byRole(calls, roleExtractor)), len(byRole(calls, roleJudge)); ex != 1 || ju != 2 {
		t.Errorf("%d extractor and %d judge calls, want 1 and 2", ex, ju)
	}
}

// TestS06RejectUntilAttemptLimit: reject goes back to the extractor with the judge's
// feedback until the attempt limit (default 3); the last rejection ends the job rejected
// with the last candidate and the last feedback.
func TestS06RejectUntilAttemptLimit(t *testing.T) {
	requireLocal(t, "scripts both roles")
	c := mainClient()
	loadInputs(t)
	note := "s06-reject-loop"
	var ex, ju int
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{note: func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			ex++
			return llmReply{Text: candidate(note + " candidate " + strconv.Itoa(ex))}
		}
		ju++
		return llmReply{Text: reject("/notes", "wrong "+strconv.Itoa(ju))}
	}}))
	start := providerCalls()
	created := c.submitOK(t, env.token, standardParts(textDocument(note)), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	if final.Status != "rejected" || final.Attempts != 3 || final.Error != nil ||
		!sameJSON(final.Result, []byte(candidate(note+" candidate 3"))) {
		t.Errorf("job: %s, want rejected after 3 attempts with candidate 3", final.raw)
	}
	if len(final.Feedback) != 1 || final.Feedback[0] != (feedback{"/notes", "wrong 3", "judge"}) {
		t.Errorf("feedback %+v, want the last judge feedback", final.Feedback)
	}
	calls := byRole(env.llm.calls(env.main.name, start), roleExtractor)
	for i := 1; i < len(calls); i++ {
		if !calls[i].contains(note+" candidate "+strconv.Itoa(i)) || !calls[i].contains("wrong "+strconv.Itoa(i)) {
			t.Errorf("extractor call %d lacks the previous candidate or the judge's feedback", i+1)
		}
	}
}

// TestS06AttemptLimit: max_attempts can only lower the application's limit; a higher
// value is clamped, not refused.
func TestS06AttemptLimit(t *testing.T) {
	requireLocal(t, "scripts the judge")
	c := mainClient()
	loadInputs(t)
	note := "s06-attempt-limit"
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{note: func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			return llmReply{Text: candidate(note)}
		}
		return llmReply{Text: reject("", "no")}
	}}))
	for _, tc := range []struct {
		value string
		want  int
	}{{"1", 1}, {"2", 2}, {"99", 3}, {"", 3}} {
		parts := standardParts(textDocument(note))
		if tc.value != "" {
			parts = append(parts, textPart("max_attempts", tc.value))
		}
		created := c.submitOK(t, env.token, parts, nil)
		if final := c.waitTerminal(t, env.token, created.ID); final.Attempts != tc.want || final.Status != "rejected" {
			t.Errorf("max_attempts %q: %s after %d attempts, want rejected after %d", tc.value, final.Status, final.Attempts, tc.want)
		}
	}
}

// TestS06JobTimeout: a job reaches a terminal state within the configured time limit;
// on expiry it is failed with timeout and keeps the candidate (and any feedback on it).
func TestS06JobTimeout(t *testing.T) {
	inst := newInstance(t, instanceOptions{name: "timeout", jobTimeout: "3s", failoverTime: "2s", callTimeout: "2s"})
	env.llm.setScript(t, inst.name, func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			return llmReply{Text: candidate("s06-timeout")}
		}
		return llmReply{Text: reject("/notes", "slow no"), Delay: 1200 * time.Millisecond}
	})
	loadInputs(t)
	c := inst.client()
	created := c.submitOK(t, inst.tokens[appDev], standardParts(textDocument("s06-timeout")), nil)
	final := c.waitTerminal(t, inst.tokens[appDev], created.ID)
	// The time limit cuts the third judge call: result is the third candidate, kept, and
	// feedback is the feedback on it, which the judge had not given yet (contract §5:
	// feedback is the final feedback on result).
	if final.Status != "failed" || final.errorCode() != "timeout" || !sameJSON(final.Result, []byte(candidate("s06-timeout"))) {
		t.Errorf("job: %s, want failed timeout with the candidate kept", final.raw)
	}
	for _, f := range final.Feedback {
		if f.Source != "judge" || f.Message != "slow no" {
			t.Errorf("feedback %+v is not the judge's", f)
		}
	}
	c0, _ := time.Parse(time.RFC3339, final.CreatedAt)
	if final.FinishedAt != nil {
		if f0, _ := time.Parse(time.RFC3339, *final.FinishedAt); f0.Sub(c0) > 5*time.Second {
			t.Errorf("finished %s after creation, over the 3 s limit", f0.Sub(c0))
		}
	}
}

// ---- §7 The judge verdict ----

// TestS07VerdictFormat: the judge framing asks for {"verdict", "feedback"} with accept or
// reject; a verdict other than accept or reject is unparseable (asked twice, then
// judge_failed).
func TestS07VerdictFormat(t *testing.T) {
	requireLocal(t, "scripts the judge")
	c := mainClient()
	loadInputs(t)
	note := "s07-verdict"
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{note: func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			return llmReply{Text: candidate(note)}
		}
		return llmReply{Text: `{"verdict": "maybe", "feedback": {"/notes": "unsure"}}`}
	}}))
	start := providerCalls()
	created := c.submitOK(t, env.token, standardParts(textDocument(note)), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	if final.Status != "failed" || final.errorCode() != "judge_failed" {
		t.Errorf("verdict maybe: %s, want failed judge_failed", final.raw)
	}
	ju := byRole(env.llm.calls(env.main.name, start), roleJudge)
	if len(ju) != 2 {
		t.Errorf("%d judge calls, want 2", len(ju))
	}
	if len(ju) > 0 {
		for _, word := range []string{`"verdict"`, `"feedback"`, `"accept"`, `"reject"`} {
			if !strings.Contains(ju[0].System, word) {
				t.Errorf("the judge framing does not mention %s", word)
			}
		}
	}
}

// TestS07FeedbackOrderAndMessagesUnedited: the judge's feedback map becomes the feedback
// array in the order the judge gave, with source judge, and messages are not edited
// (2,500 characters are below any allowed cap).
func TestS07FeedbackOrderAndMessagesUnedited(t *testing.T) {
	requireLocal(t, "scripts the judge")
	c := mainClient()
	loadInputs(t)
	note := "s07-feedback"
	long := strings.Repeat("Größe «quoted» \"text\"  with  spaces,\ttabs and a newline\n", 50)[:2500]
	pairs := []string{
		"/schema_version", long,
		"/reservations/-", "A reservation for the return flight is missing.",
		"", "The candidate as a whole: see page 1.",
		"/notes", "Zürich ≠ Zurich",
	}
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{note: func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			return llmReply{Text: candidate(note)}
		}
		return llmReply{Text: reject(pairs...)}
	}}))
	created := c.submitOK(t, env.token, append(standardParts(textDocument(note)), textPart("max_attempts", "1")), nil)
	final := c.waitTerminal(t, env.token, created.ID)
	if final.Status != "rejected" || len(final.Feedback) != len(pairs)/2 {
		t.Fatalf("job: %s, want rejected with %d feedback entries", final.raw, len(pairs)/2)
	}
	for i, f := range final.Feedback {
		want := feedback{pairs[2*i], pairs[2*i+1], "judge"}
		if f != want {
			t.Errorf("feedback %d = %+.80v, want %+.80v", i, f, want)
		}
	}
}

// ---- §8 Quota and rate limits ----

// TestS08RateLimited: exceeding the rate limit answers 429 rate_limited with
// X-Quota-Remaining above zero and Retry-After.
func TestS08RateLimited(t *testing.T) {
	requireLocal(t, "needs an application with a tiny rate limit")
	c := mainClient()
	tok := env.main.tokens[appRate]
	var r reply
	for range rateLimit + 3 {
		r = c.do(t, http.MethodGet, "/v1/quota", tok, nil, "", nil)
		if r.status == http.StatusTooManyRequests {
			break
		}
	}
	if r.status != http.StatusTooManyRequests || r.errorCode() != "rate_limited" {
		t.Fatalf("after %d requests per minute: %d %s, want 429 rate_limited", rateLimit+3, r.status, r.body)
	}
	if n, err := strconv.Atoi(r.header.Get("X-Quota-Remaining")); err != nil || n <= 0 {
		t.Errorf("X-Quota-Remaining %q on a rate limit, want above zero", r.header.Get("X-Quota-Remaining"))
	}
	if r.header.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

// TestS08QuotaHeadersOnEveryAuthenticatedResponse: X-Quota-Remaining and X-Quota-Reset
// on every response to an authenticated request, 2xx and errors alike.
func TestS08QuotaHeadersOnEveryAuthenticatedResponse(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	good := standardParts(textDocument("s08-headers"))
	replies := map[string]reply{
		"200 quota":  c.do(t, http.MethodGet, "/v1/quota", env.token, nil, "", nil),
		"404 job":    c.get(t, env.token, "j_unknown"),
		"400 submit": c.submit(t, env.token, replacePart(good, "document", part{}), nil),
		"415 submit": c.submit(t, env.token, replacePart(good, "document", part{"document", "x", "application/zip", []byte("PK")}), nil),
		"422 submit": c.submit(t, env.token, replacePart(good, "schema", part{"schema", "", "application/schema+json", []byte(`{"type": 1}`)}), nil),
		"413 submit": c.submit(t, env.token, replacePart(good, "schema", part{"schema", "", "application/schema+json", padded(inputs.schema, ' ', 256*kib+1)}), nil),
		"202 submit": c.submit(t, env.token, good, nil),
	}
	if r := replies["202 submit"]; r.status == http.StatusAccepted {
		j := parseJob(t, r)
		t.Cleanup(func() { c.drain(env.token, j.ID) })
		replies["200 poll"] = c.get(t, env.token, j.ID)
	}
	if !env.remote {
		quotaTok := env.main.tokens[appQuota]
		for range quotaLimit + 1 {
			r := c.submit(t, quotaTok, good, nil)
			if r.status != http.StatusAccepted {
				replies["429 quota"] = r
				break
			}
			j := parseJob(t, r)
			t.Cleanup(func() { c.drain(quotaTok, j.ID) })
		}
	}
	for name, r := range replies {
		if r.header.Get("X-Quota-Remaining") == "" || r.header.Get("X-Quota-Reset") == "" {
			t.Errorf("%s (status %d): X-Quota-Remaining %q, X-Quota-Reset %q", name, r.status,
				r.header.Get("X-Quota-Remaining"), r.header.Get("X-Quota-Reset"))
		}
	}
}

// TestS08QuotaHeadersOnForbidden: a 403 answers an authenticated request (the token is
// valid, its scope is not), so it carries the quota headers too.
func TestS08QuotaHeadersOnForbidden(t *testing.T) {
	requireLocal(t, "creates a jobs:read token")
	loadInputs(t)
	tok, _, err := env.main.createToken(appDev, "jobs:read", "1d")
	if err != nil {
		t.Fatal(err)
	}
	r := mainClient().submit(t, tok, standardParts(textDocument("s08-forbidden")), nil)
	if r.status != http.StatusForbidden {
		t.Fatalf("submit with jobs:read: %d, want 403", r.status)
	}
	checkQuotaHeaders(t, "403", r)
}

// TestS08QuotaRead: GET /v1/quota returns the numbers of the headers without changing
// them; a submission counts one.
func TestS08QuotaRead(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	a := c.quota(t, env.token)
	b := c.quota(t, env.token)
	if a != b {
		t.Errorf("two quota reads differ: %+v, %+v", a, b)
	}
	if a.Limit < a.Remaining || a.Window == "" {
		t.Errorf("quota %+v", a)
	}
	if env.remote {
		return
	}
	if a.Limit != 100000 || a.Window != "P1D" {
		t.Errorf("quota %+v, want the configured limit 100000 per P1D", a)
	}
	r := c.submit(t, env.token, standardParts(textDocument("s08-quota-read")), nil)
	if r.status != http.StatusAccepted {
		t.Fatalf("submit: %d", r.status)
	}
	j := parseJob(t, r)
	t.Cleanup(func() { c.drain(env.token, j.ID) })
	if r.header.Get("X-Quota-Remaining") != strconv.Itoa(a.Remaining-1) {
		t.Errorf("X-Quota-Remaining after a submission %q, want %d", r.header.Get("X-Quota-Remaining"), a.Remaining-1)
	}
	if after := c.quota(t, env.token); after.Remaining != a.Remaining-1 {
		t.Errorf("remaining %d after one submission, want %d", after.Remaining, a.Remaining-1)
	}
}

// ---- §9 Errors ----

// TestS09ErrorCodes: each HTTP error of the §9 table answers with its code, the Error
// body and Content-Type application/json (checkResponse checks shape and status-code
// pairing on every error of the suite).
func TestS09ErrorCodes(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	good := standardParts(textDocument("s09-codes"))
	key := "conformance-s09-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	first := c.submitOK(t, env.token, good, http.Header{"Idempotency-Key": {key}})
	c.waitTerminal(t, env.token, first.ID)
	type check struct {
		r      reply
		status int
		code   string
	}
	checks := map[string]check{
		"bad_request":            {c.submit(t, env.token, replacePart(good, "judge_instructions", part{}), nil), 400, "bad_request"},
		"unauthorized":           {c.submit(t, "", good, nil), 401, "unauthorized"},
		"not_found":              {c.get(t, env.token, "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB"), 404, "not_found"},
		"payload_too_large":      {c.submit(t, env.token, append(slices.Clone(good), part{"page", "p.png", "image/png", padded(inputs.png, 0, 10*mib+1)}), nil), 413, "payload_too_large"},
		"unsupported_media_type": {c.submit(t, env.token, replacePart(good, "document", part{"document", "x", "application/zip", []byte("PK")}), nil), 415, "unsupported_media_type"},
		"invalid_schema":         {c.submit(t, env.token, replacePart(good, "schema", part{"schema", "", "application/schema+json", []byte(`{"type": "nonsense"}`)}), nil), 422, "invalid_schema"},
		"idempotency_mismatch":   {c.submit(t, env.token, standardParts(textDocument("s09-other")), http.Header{"Idempotency-Key": {key}}), 422, "idempotency_mismatch"},
	}
	if !env.remote {
		tok, _, err := env.main.createToken(appDev, "jobs:read", "1d")
		if err != nil {
			t.Fatal(err)
		}
		checks["forbidden"] = check{c.submit(t, tok, good, nil), 403, "forbidden"}
		webp := padded([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), 0, 64)
		checks["unsupported_document"] = check{c.submit(t, env.token, replacePart(good, "document", part{"document", "x.webp", "image/webp", webp}), nil), 415, "unsupported_document"}
		var limited reply
		for range rateLimit + 3 {
			if limited = c.do(t, http.MethodGet, "/v1/quota", env.main.tokens[appRate], nil, "", nil); limited.status == 429 {
				break
			}
		}
		checks["rate_limited"] = check{limited, 429, "rate_limited"}
		var spent reply
		for range quotaLimit + 1 {
			spent = c.submit(t, env.main.tokens[appQuota], good, nil)
			if spent.status != http.StatusAccepted {
				break
			}
			j := parseJob(t, spent)
			t.Cleanup(func() { c.drain(env.main.tokens[appQuota], j.ID) })
		}
		checks["quota_exhausted"] = check{spent, 429, "quota_exhausted"}
	}
	for name, ch := range checks {
		if ch.r.status != ch.status || ch.r.errorCode() != ch.code {
			t.Errorf("%s: %d %s, want %d %s", name, ch.r.status, ch.r.body, ch.status, ch.code)
		}
	}
}

// ---- §10 Retention, privacy and logging ----

// findInDir returns the files under dir containing needle.
func findInDir(t *testing.T, dir, needle string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(needle)) {
			rel, _ := filepath.Rel(dir, path)
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

// markedJob runs a retention: none job whose every content carries a marker: document,
// instructions, schema, candidate and judge feedback. It returns the markers.
func markedJob(t *testing.T, prefix string, retention bool) (jobBody, []string) {
	t.Helper()
	c := mainClient()
	loadInputs(t)
	m := func(what string) string { return "CONFMARK-" + prefix + "-" + what }
	markers := []string{m("DOC"), m("EXTRACTOR"), m("JUDGE"), m("SCHEMA"), m("RESULT"), m("FEEDBACK")}
	var schema map[string]any
	if err := json.Unmarshal(inputs.schema, &schema); err != nil {
		t.Fatal(err)
	}
	schema["$comment"] = m("SCHEMA")
	schemaJSON, _ := json.Marshal(schema)
	parts := []part{
		{"document", "doc.txt", "text/plain", []byte("Booking " + m("DOC") + " at Harbour View Hotel.")},
		{"extractor_instructions", "", "text/markdown", append(slices.Clone(inputs.extractor), "\n"+m("EXTRACTOR")...)},
		{"judge_instructions", "", "text/markdown", append(slices.Clone(inputs.judge), "\n"+m("JUDGE")...)},
		{"schema", "", "application/schema+json", schemaJSON},
		textPart("max_attempts", "1"),
	}
	if retention {
		parts = append(parts, textPart("retention", "none"))
	}
	env.llm.setScript(t, env.main.name, scriptPerNote(map[string]func(llmCall) llmReply{m("DOC"): func(c llmCall) llmReply {
		if c.Role == roleExtractor {
			return llmReply{Text: candidate(m("RESULT"))}
		}
		return llmReply{Text: reject("/notes", m("FEEDBACK")+" is not in the document")}
	}}))
	created := c.submitOK(t, env.token, parts, nil)
	final := c.waitTerminal(t, env.token, created.ID)
	if final.Status != "rejected" || !strings.Contains(string(final.Result), m("RESULT")) {
		t.Fatalf("marked job: %s, want rejected with the marked result", final.raw)
	}
	return final, markers
}

// TestS10NoContentSurvivesFirstRead: after the first terminal read of a retention: none
// job, nothing of its content survives in the service's database or blob store.
func TestS10NoContentSurvivesFirstRead(t *testing.T) {
	requireLocal(t, "reads the data directory")
	final, markers := markedJob(t, "SURVIVE", true)
	if r := mainClient().get(t, env.token, final.ID); r.status != http.StatusNotFound {
		t.Fatalf("second read: %d, want 404", r.status)
	}
	var found []string
	for _, mk := range markers {
		for _, hit := range findInDir(t, env.main.dataDir, mk) {
			found = append(found, mk+" in "+hit)
		}
	}
	if len(found) > 0 {
		t.Errorf("content of a retention: none job survives its first terminal read in the data directory: %s", strings.Join(found, "; "))
	}
}

// TestS10OperatorForcesRetentionNone: an application the operator forced to none gets
// retention none whatever the request says.
func TestS10OperatorForcesRetentionNone(t *testing.T) {
	requireLocal(t, "needs an application with retention forced to none")
	c := mainClient()
	loadInputs(t)
	tok := env.main.tokens[appForced]
	for _, parts := range [][]part{
		standardParts(textDocument("s10-forced-absent")),
		append(standardParts(textDocument("s10-forced-default")), textPart("retention", "default")),
	} {
		created := c.submitOK(t, tok, parts, nil)
		c.waitTerminal(t, tok, created.ID)
		if r := c.get(t, tok, created.ID); r.status != http.StatusNotFound {
			t.Errorf("second read of a job of a forced-none application: %d, want 404", r.status)
		}
		if _, err := os.Stat(env.main.blobDir(created.ID)); !os.IsNotExist(err) {
			t.Errorf("blobs of a forced-none job survive: %v", err)
		}
	}
}

// TestS10DefaultRetention: a retention: default job (or one without the part) is kept
// for the application's configured retention, readable after its terminal read.
func TestS10DefaultRetention(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	for _, parts := range [][]part{
		standardParts(textDocument("s10-default-absent")),
		append(standardParts(textDocument("s10-default-explicit")), textPart("retention", "default")),
	} {
		created := c.submitOK(t, env.token, parts, nil)
		final := c.waitTerminal(t, env.token, created.ID)
		if r := c.get(t, env.token, created.ID); r.status != http.StatusOK {
			t.Errorf("second read of a retention: default job: %d, want 200", r.status)
		}
		if env.remote {
			continue
		}
		c0, _ := time.Parse(time.RFC3339, final.CreatedAt)
		e0, _ := time.Parse(time.RFC3339, final.ExpiresAt)
		f0 := c0
		if final.FinishedAt != nil {
			f0, _ = time.Parse(time.RFC3339, *final.FinishedAt)
		}
		if e0.Before(c0.Add(devRetention-time.Second)) || e0.After(f0.Add(devRetention+time.Second)) {
			t.Errorf("expires_at %s for created_at %s, want the configured retention %s", final.ExpiresAt, final.CreatedAt, devRetention)
		}
	}
}

// TestS10NoContentInLogsOrMetrics: document bytes, instructions, schema, candidates and
// feedback never appear in the logs (at debug level, the most verbose, with the content
// debug switch at its default) or in the metrics.
func TestS10NoContentInLogsOrMetrics(t *testing.T) {
	requireLocal(t, "reads the service's logs")
	_, markers := markedJob(t, "LOGS", false)
	metrics := mainClient().raw(t, http.MethodGet, "/metrics", "", nil, "", nil)
	if metrics.status != http.StatusOK {
		t.Errorf("/metrics from loopback: %d", metrics.status)
	}
	logs := env.main.logs.String()
	if !strings.Contains(logs, `"level":"INFO"`) {
		t.Error("no log lines from the service: the scan below would prove little")
	}
	for _, mk := range markers {
		if strings.Contains(logs, mk) {
			t.Errorf("the log carries %s", mk)
		}
		if bytes.Contains(metrics.body, []byte(mk)) {
			t.Errorf("the metrics carry %s", mk)
		}
	}
}

// TestS10RequestCannotNameEndpoint: a request can never name an endpoint or a model.
func TestS10RequestCannotNameEndpoint(t *testing.T) {
	c := mainClient()
	loadInputs(t)
	before := 0
	if !env.remote {
		before = providerCalls()
	}
	for _, name := range []string{"model", "endpoint", "provider", "base_url"} {
		parts := append(standardParts(textDocument("s10-name")), textPart(name, "https://attacker.example/v1"))
		if r := c.submit(t, env.token, parts, nil); r.status != http.StatusBadRequest {
			t.Errorf("a %s part: %d %s, want 400", name, r.status, r.body)
		}
	}
	if !env.remote {
		settle()
		if providerCalls() != before {
			t.Error("a request naming an endpoint reached a provider")
		}
	}
}

// ---- §11 Versioning ----

// TestS11ServiceVersionTracksConfiguration: two results with the same service_version
// come from the same configuration: jobs of one configuration share it, and a change of
// the attempt limit or of what the models accept changes it.
func TestS11ServiceVersionTracksConfiguration(t *testing.T) {
	requireLocal(t, "starts services with other configurations")
	loadInputs(t)
	run := func(c *client, token, note string) string {
		created := c.submitOK(t, token, standardParts(textDocument(note)), nil)
		return c.waitTerminal(t, token, created.ID).ServiceVersion
	}
	v1, v2 := run(mainClient(), env.token, "s11-a"), run(mainClient(), env.token, "s11-b")
	if v1 == "" || v1 != v2 {
		t.Errorf("one configuration, two versions: %q %q", v1, v2)
	}
	for _, o := range []instanceOptions{
		{name: "attempts-2", maxAttempts: 2},
		{name: "accepts-text", accepts: []string{"text/plain", "text/html"}},
	} {
		inst := newInstance(t, o)
		if v := run(inst.client(), inst.tokens[appDev], "s11-"+o.name); v == v1 {
			t.Errorf("%s: service_version %q unchanged", o.name, v)
		}
	}
}

// ---- §12 Operations ----

// TestS12Healthz: GET /healthz answers 200 {"status":"ok"} without authentication when
// the service can accept jobs (503 when a role's endpoints are all marked down:
// TestItem05JudgeGroupUnreachable).
func TestS12Healthz(t *testing.T) {
	r := mainClient().raw(t, http.MethodGet, "/healthz", "", nil, "", nil)
	var body struct{ Status string }
	if err := json.Unmarshal(r.body, &body); err != nil || r.status != http.StatusOK || body.Status != "ok" {
		t.Errorf("GET /healthz = %d %s, want 200 {\"status\":\"ok\"}", r.status, r.body)
	}
}

// TestS12ConfigCheck: vde config check validates without network calls: groups reference
// defined endpoints, every endpoint-model pair declares its media types, both roles have
// a group, the job time limit is at most 8 minutes.
func TestS12ConfigCheck(t *testing.T) {
	requireLocal(t, "runs the CLI")
	good, err := os.ReadFile(env.main.config)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	check := func(name, text string) error {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := runCLI("", nil, "config", "check", "--config", path)
		return err
	}
	before := providerCalls()
	if err := check("good", string(good)); err != nil {
		t.Errorf("the suite's own configuration fails the check: %v", err)
	}
	for name, edit := range map[string][2]string{
		"undefined endpoint": {"- {endpoint: fallback, model: extractor-fallback}", "- {endpoint: nowhere, model: extractor-fallback}"},
		"no media types": {"extractor-primary: {accepts: [" + strings.Join(defaultAccepts, ", ") + "]}",
			"extractor-primary: {accepts: []}"},
		"no judge role":   {"  judge: {group: review}", ""},
		"timeout over 8m": {"job_timeout: 5m", "job_timeout: 9m"},
	} {
		text := strings.Replace(string(good), edit[0], edit[1], 1)
		if text == string(good) {
			t.Fatalf("%s: the edit does not apply", name)
		}
		if err := check(strings.ReplaceAll(name, " ", "-"), text); err == nil {
			t.Errorf("%s: vde config check passes", name)
		}
	}
	if providerCalls() != before {
		t.Error("vde config check called a provider endpoint")
	}
}

// TestS12DevConfigDocumented: a minimal development configuration is documented (one
// provider, one model for both roles, one application, a token command) and valid with
// a single key in an environment variable.
func TestS12DevConfigDocumented(t *testing.T) {
	requireLocal(t, "runs the CLI")
	path := filepath.Join(env.root, "examples", "compose", "vde.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Endpoints map[string]struct {
			APIKeyEnv string                    `yaml:"api_key_env"`
			Models    map[string]map[string]any `yaml:"models"`
		} `yaml:"endpoints"`
		Roles        map[string]struct{ Group string } `yaml:"roles"`
		Applications []map[string]any                  `yaml:"applications"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var keyEnv string
	for _, e := range cfg.Endpoints {
		keyEnv = e.APIKeyEnv
		if len(e.Models) != 1 {
			t.Errorf("%d models, want one", len(e.Models))
		}
	}
	if len(cfg.Endpoints) != 1 || cfg.Roles["extractor"].Group != cfg.Roles["judge"].Group || len(cfg.Applications) != 1 || keyEnv == "" {
		t.Errorf("examples/compose/vde.yaml is not one provider, one model for both roles, one application, one key")
	}
	if _, stderr, err := runCLI(t.TempDir(), []string{keyEnv + "=conformance-dummy-key"}, "config", "check", "--config", path); err != nil {
		t.Errorf("vde config check on the development configuration: %v: %s", err, stderr)
	}
	readme, err := os.ReadFile(filepath.Join(env.root, "examples", "compose", "README.md"))
	if err != nil || !bytes.Contains(readme, []byte("vde token create")) {
		t.Error("the development walk-through does not show how to create a token")
	}
}

func readAllClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}
