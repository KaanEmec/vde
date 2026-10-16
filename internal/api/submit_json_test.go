package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// jsonFrom is the JSON body equivalent to multipart parts.
func jsonFrom(parts []mpart) map[string]any {
	body := map[string]any{}
	var pages []any
	for _, p := range parts {
		switch p.name {
		case partDocument:
			body["document"] = map[string]any{"media_type": p.ctype, "data_base64": b64(string(p.data)), "filename": p.filename}
		case partPage:
			pages = append(pages, map[string]any{"media_type": p.ctype, "data_base64": b64(string(p.data))})
		case partSchema:
			body["schema"] = json.RawMessage(p.data)
		case partMaxAttempts:
			body["max_attempts"] = json.RawMessage(p.data)
		default:
			body[p.name] = string(p.data)
		}
	}
	if pages != nil {
		body["pages"] = pages
	}
	return body
}

func (rg *submitRig) submitJSON(t *testing.T, target string, body any, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	data, ok := body.([]byte) // sent as is
	if !ok {
		var err error
		if data, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	return rg.postTo(target, data, "application/json; charset=utf-8", mod)
}

// The scenarios of the multipart path give the same jobs through the JSON path: same
// representation, media type, pages and outcome.
func TestJSONSubmissionMatchesMultipart(t *testing.T) {
	t.Parallel()
	png := mpart{name: partPage, ctype: "image/png", data: pngBytes(t)}
	scenarios := []struct {
		name  string
		parts []mpart
	}{
		{"text", validParts()},
		{"html", withPart(validParts(), mpart{name: partDocument, ctype: "text/html; charset=utf-8", data: []byte("<p>" + submitDocText + "</p>")})},
		{"pdf with pages", append(withPart(validParts(), mpart{name: partDocument, ctype: "application/pdf", data: []byte(pdfBytes)}), png, png)},
		{"png image", withPart(validParts(), mpart{name: partDocument, ctype: "image/png", data: pngBytes(t)})},
		{"schema-less, options", append(withoutPart(validParts(), partSchema),
			mpart{name: partRetention, ctype: "text/plain", data: []byte("none")},
			mpart{name: partMaxAttempts, ctype: "text/plain", data: []byte("2")})},
	}
	rg := newSubmitRig(t, submitRigOptions{pool: true})
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			viaParts := decodeJob(t, rg.submit(t, sc.parts, nil))
			viaJSON := decodeJob(t, rg.submitJSON(t, "/v1/jobs", jsonFrom(sc.parts), nil))
			a := rg.waitTerminal(t, viaParts["id"].(string))
			b := rg.waitTerminal(t, viaJSON["id"].(string))
			if a.Status != b.Status || a.Representation != b.Representation || a.MediaType != b.MediaType ||
				a.PageCount != b.PageCount || a.Size != b.Size || a.MaxAttempts != b.MaxAttempts || a.Retention != b.Retention {
				t.Errorf("multipart job %+v\nJSON job      %+v", a, b)
			}
			if a.Status != job.StatusAccepted {
				t.Errorf("status = %s, want accepted", a.Status)
			}
		})
	}
}

func TestJSONSubmissionThroughExtractAndIdempotency(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{pool: true, notify: true})
	body := jsonFrom(validParts())
	j := syncBody(t, rg.submitJSON(t, "/v1/extract", body, idempotencyKey("k-1")), http.StatusOK)
	if j.Status != job.StatusAccepted || string(j.Result) != submitCandidate {
		t.Errorf("job = %+v", j)
	}
	w := rg.submitJSON(t, "/v1/jobs", body, idempotencyKey("k-1"))
	if w.Code != http.StatusAccepted || w.Header().Get(headerIdempotencyReplayed) != "true" {
		t.Errorf("replay = %d %v", w.Code, w.Header())
	}
	body["judge_instructions"] = "different"
	if w := rg.submitJSON(t, "/v1/jobs", body, idempotencyKey("k-1")); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("same key, other body = %d, want 422", w.Code)
	}
	if rg.quotaUsed(t) != 1 {
		t.Errorf("quota used = %d, want 1", rg.quotaUsed(t))
	}
}

func TestJSONSubmissionRefusals(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{cfg: func(c *config.Config) {
		c.Limits.DocumentBytes, c.Limits.Pages = 64, 1
	}})
	edit := func(f func(m map[string]any)) any {
		m := jsonFrom(validParts())
		f(m)
		return m
	}
	doc := func(m map[string]any) map[string]any { return m["document"].(map[string]any) }
	tests := []struct {
		name   string
		body   any
		status int
		code   string
	}{
		{"malformed", []byte(`{"document":`), 400, codeBadRequest},
		{"trailing data", []byte(`{} {}`), 400, codeBadRequest},
		{"unknown field", edit(func(m map[string]any) { m["documents"] = 1 }), 400, codeBadRequest},
		{"no document", edit(func(m map[string]any) { delete(m, "document") }), 400, codeBadRequest},
		{"no media type", edit(func(m map[string]any) { delete(doc(m), "media_type") }), 400, codeBadRequest},
		{"data and url", edit(func(m map[string]any) { doc(m)["url"] = "https://example.com/a" }), 400, codeBadRequest},
		{"neither data nor url", edit(func(m map[string]any) { delete(doc(m), "data_base64") }), 400, codeBadRequest},
		{"bad base64", edit(func(m map[string]any) { doc(m)["data_base64"] = "!!!" }), 400, codeBadRequest},
		{"document too large", edit(func(m map[string]any) { doc(m)["data_base64"] = b64(strings.Repeat("a", 65)) }), 413, codePayloadTooLarge},
		{"too many pages", edit(func(m map[string]any) {
			p := map[string]any{"media_type": "image/png", "data_base64": b64(string(pngBytes(t)))}
			m["pages"] = []any{p, p}
		}), 413, codePayloadTooLarge},
		{"instructions too large", edit(func(m map[string]any) { m["judge_instructions"] = strings.Repeat("a", maxInstructionsBytes+1) }), 413, codePayloadTooLarge},
		{"no instructions", edit(func(m map[string]any) { delete(m, "extractor_instructions") }), 400, codeBadRequest},
		{"schema not an object", edit(func(m map[string]any) { m["schema"] = true }), 422, codeInvalidSchema},
		{"invalid schema", edit(func(m map[string]any) { m["schema"] = map[string]any{"type": 12} }), 422, codeInvalidSchema},
		{"max_attempts as a string", edit(func(m map[string]any) { m["max_attempts"] = "3" }), 400, codeBadRequest},
		{"max_attempts zero", edit(func(m map[string]any) { m["max_attempts"] = 0 }), 400, codeBadRequest},
		{"bad retention", edit(func(m map[string]any) { m["retention"] = "forever" }), 400, codeBadRequest},
		{"media type refused", edit(func(m map[string]any) { doc(m)["media_type"] = "application/zip" }), 415, codeUnsupportedMediaType},
		{"url without fetch", edit(func(m map[string]any) {
			delete(doc(m), "data_base64")
			doc(m)["url"] = "https://example.com/a.txt"
		}), 400, codeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := rg.submitJSON(t, "/v1/jobs", tt.body, nil)
			if w.Code != tt.status || decodeError(t, w).Code != tt.code {
				t.Errorf("= %d %s, want %d %s", w.Code, w.Body.String(), tt.status, tt.code)
			}
		})
	}
	if rg.quotaUsed(t) != 0 || rg.blobJobs(t) != 0 {
		t.Errorf("quota used %d, blob jobs %d; want nothing stored", rg.quotaUsed(t), rg.blobJobs(t))
	}
}
