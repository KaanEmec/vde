package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"unicode/utf8"

	"github.com/kaanemec/vde/internal/config"
)

// fuzzBoundary is the multipart boundary of every fuzz input; the fuzzer mutates the
// body around it.
const fuzzBoundary = "vde-fuzz-boundary"

// fuzzLimits are small, so the inputs reach every limit.
var fuzzLimits = config.Limits{DocumentBytes: 4 << 10, PageBytes: 1 << 10, Pages: 3}

const fuzzRequestLimit = 64 << 10

// fuzzPart is one part of a seed body: the Content-Disposition value as written (so
// seeds can carry malformed ones), a Content-Type and the bytes.
type fuzzPart struct{ disposition, ctype, data string }

func fuzzBody(f *testing.F, parts ...fuzzPart) []byte {
	f.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if err := mw.SetBoundary(fuzzBoundary); err != nil {
		f.Fatal(err)
	}
	for _, p := range parts {
		h := textproto.MIMEHeader{"Content-Disposition": {p.disposition}}
		if p.ctype != "" {
			h.Set("Content-Type", p.ctype)
		}
		w, err := mw.CreatePart(h)
		if err != nil {
			f.Fatal(err)
		}
		_, _ = w.Write([]byte(p.data))
	}
	if err := mw.Close(); err != nil {
		f.Fatal(err)
	}
	return b.Bytes()
}

// FuzzReadSubmission drives the multipart reader of POST /v1/jobs with arbitrary bodies:
// it never panics, refuses only with 400 or 413, and what it accepts has the required
// parts within their limits.
func FuzzReadSubmission(f *testing.F) {
	doc := fuzzPart{`form-data; name="document"; filename="a.txt"`, "text/plain", "Booking ABC123"}
	ext := fuzzPart{`form-data; name="extractor_instructions"`, "text/plain", "Extract."}
	judge := fuzzPart{`form-data; name="judge_instructions"`, "text/markdown", "Check."}
	page := fuzzPart{`form-data; name="page"`, "image/png", "\x89PNG\r\n\x1a\n"}
	for _, seed := range [][]fuzzPart{
		{doc, ext, judge},
		{doc, ext, judge, page, page,
			{`form-data; name="schema"`, "application/schema+json", `{"type":"object"}`},
			{`form-data; name="retention"`, "", "none"},
			{`form-data; name="max_attempts"`, "", "3"},
			{`form-data; name="judge"`, "text/plain", "auto"}},
		{doc, ext, judge, page, page, page, page},                 // one page too many
		{doc, doc, ext, judge},                                    // repeated part
		{ext, judge},                                              // missing document
		{{`form-data; name="document"`, "", "x"}, ext, judge},     // document without a type
		{doc, {`form-data; name="other"`, "text/plain", "x"}},     // unknown part
		{doc, {`attachment; name="document"`, "text/plain", "x"}}, // not form-data
		{doc, {`form-data; name="extractor_instructions"`, "text/plain", "\xff\xfe"}, judge},
		{doc, {`form-data; name="schema"`, "text/html", "{}"}, ext, judge},
		// A nested multipart body is only bytes of the document part.
		{{`form-data; name="document"`, "multipart/mixed; boundary=inner",
			"--inner\r\nContent-Type: text/plain\r\n\r\nx\r\n--inner--\r\n"}, ext, judge},
		// Header injection attempts in the disposition.
		{{"form-data; name=\"document\"\r\nX-Injected: 1", "text/plain", "x"}, ext, judge},
		{{`form-data; name="document"; filename="../../etc/passwd"`, "text/plain", "x"}, ext, judge},
	} {
		f.Add(fuzzBody(f, seed...))
	}
	f.Add([]byte("--" + fuzzBoundary + "\r\nContent-Disposition: form-data; name=\"document\"\r\n\r\nunterminated"))
	f.Add([]byte{})

	maxBytes := map[string]int{
		partDocument: int(fuzzLimits.DocumentBytes), partExtractorInstructions: maxInstructionsBytes,
		partJudgeInstructions: maxInstructionsBytes, partSchema: maxSchemaBytes, partRetention: maxOptionBytes,
		partMaxAttempts: maxOptionBytes, partJudge: maxJudgeBytes,
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+fuzzBoundary)
		r.Body = &limitedBody{ReadCloser: http.MaxBytesReader(httptest.NewRecorder(), r.Body, fuzzRequestLimit)}
		sub, e, ok := readSubmission(r, fuzzLimits, fuzzRequestLimit)
		if !ok {
			if e.status != http.StatusBadRequest && e.status != http.StatusRequestEntityTooLarge {
				t.Fatalf("refused with %d %s, want 400 or 413", e.status, e.code)
			}
			return
		}
		if sub.document == nil || sub.extractorInstructions == nil || sub.judgeInstructions == nil {
			t.Fatal("accepted without a required part")
		}
		if len(sub.pages) > fuzzLimits.Pages {
			t.Fatalf("accepted %d pages, limit %d", len(sub.pages), fuzzLimits.Pages)
		}
		for _, p := range sub.pages {
			if int64(len(p.data)) > fuzzLimits.PageBytes {
				t.Fatalf("accepted a page of %d bytes", len(p.data))
			}
		}
		for name, p := range map[string]*part{
			partDocument: sub.document, partExtractorInstructions: sub.extractorInstructions,
			partJudgeInstructions: sub.judgeInstructions, partSchema: sub.schema, partRetention: sub.retention,
			partMaxAttempts: sub.maxAttempts, partJudge: sub.judge,
		} {
			if p != nil && len(p.data) > maxBytes[name] {
				t.Fatalf("accepted %s of %d bytes, limit %d", name, len(p.data), maxBytes[name])
			}
		}
		if !utf8.Valid(sub.extractorInstructions.data) || !utf8.Valid(sub.judgeInstructions.data) {
			t.Fatal("accepted instructions that are not UTF-8")
		}
		if h := sub.hash(); len(h) != 64 {
			t.Fatalf("body hash has %d characters", len(h))
		}
	})
}
