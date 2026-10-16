package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The TravelInbox API document, FromTravelApp/extraction-service-api.yaml, read with
// yaml.v3 and checked by a small validator that covers the keywords that document uses:
// $ref, type (one or a list), required, properties, enum, const, items, minimum,
// maxItems, minLength, maxLength, pattern, oneOf and format date-time.

var spec struct {
	once    sync.Once
	schemas map[string]any
	err     error
}

func specSchemas(t *testing.T) map[string]any {
	t.Helper()
	spec.once.Do(func() {
		data, err := os.ReadFile(filepath.Join(env.root, "FromTravelApp", "extraction-service-api.yaml"))
		if err != nil {
			spec.err = err
			return
		}
		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			spec.err = err
			return
		}
		components, _ := doc["components"].(map[string]any)
		spec.schemas, _ = components["schemas"].(map[string]any)
		if len(spec.schemas) == 0 {
			spec.err = fmt.Errorf("no components.schemas in the API document")
		}
	})
	if spec.err != nil {
		t.Fatalf("reading the TravelInbox API document: %v", spec.err)
	}
	return spec.schemas
}

// validateBody checks a JSON body against components.schemas[name] and returns the
// violations.
func validateBody(t *testing.T, name string, body []byte) []string {
	t.Helper()
	schemas := specSchemas(t)
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{"body is not JSON: " + err.Error()}
	}
	s, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("the API document has no schema %s", name)
	}
	return checkSchema(schemas, "", s, v)
}

func checkSchema(schemas map[string]any, path string, s map[string]any, v any) []string {
	if ref, ok := s["$ref"].(string); ok {
		target, ok := schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		if !ok {
			return []string{path + ": unresolvable $ref " + ref}
		}
		return checkSchema(schemas, path, target, v)
	}
	at := path
	if at == "" {
		at = "(root)"
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, at+": "+fmt.Sprintf(format, args...)) }

	if alts, ok := s["oneOf"].([]any); ok {
		matched := 0
		for _, a := range alts {
			if as, ok := a.(map[string]any); ok && len(checkSchema(schemas, path, as, v)) == 0 {
				matched++
			}
		}
		if matched != 1 {
			fail("matches %d of the oneOf alternatives, want exactly 1", matched)
		}
	}
	if typ, ok := s["type"]; ok {
		var types []string
		switch tv := typ.(type) {
		case string:
			types = []string{tv}
		case []any:
			for _, x := range tv {
				types = append(types, fmt.Sprint(x))
			}
		}
		if !slices.ContainsFunc(types, func(want string) bool { return hasType(v, want) }) {
			fail("%s is not of type %v", describe(v), types)
			return errs
		}
	}
	if c, ok := s["const"]; ok && fmt.Sprint(c) != fmt.Sprint(v) {
		fail("%s is not the constant %v", describe(v), c)
	}
	if enum, ok := s["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) }) {
		fail("%s is not one of %v", describe(v), enum)
	}
	switch val := v.(type) {
	case map[string]any:
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, present := val[fmt.Sprint(r)]; !present {
					fail("required field %q is missing", r)
				}
			}
		}
		if props, ok := s["properties"].(map[string]any); ok {
			for name, ps := range props {
				sub, ok := val[name]
				if psm, isMap := ps.(map[string]any); ok && isMap {
					errs = append(errs, checkSchema(schemas, path+"/"+name, psm, sub)...)
				}
			}
		}
	case []any:
		if maxItems, ok := s["maxItems"].(int); ok && len(val) > maxItems {
			fail("has %d items, more than %d", len(val), maxItems)
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, item := range val {
				errs = append(errs, checkSchema(schemas, fmt.Sprintf("%s/%d", path, i), items, item)...)
			}
		}
	case string:
		if n, ok := s["minLength"].(int); ok && len([]rune(val)) < n {
			fail("is shorter than %d", n)
		}
		if n, ok := s["maxLength"].(int); ok && len([]rune(val)) > n {
			fail("is longer than %d", n)
		}
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(val) {
			fail("%q does not match %s", val, p)
		}
		if s["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339, val); err != nil {
				fail("%q is not an RFC 3339 date-time", val)
			}
		}
	case json.Number:
		if m, ok := s["minimum"].(int); ok {
			if f, err := val.Float64(); err != nil || f < float64(m) {
				fail("%s is below the minimum %d", val, m)
			}
		}
	}
	return errs
}

func hasType(v any, want string) bool {
	switch want {
	case "null":
		return v == nil
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		return ok && !strings.ContainsAny(n.String(), ".eE")
	}
	return false
}

func describe(v any) string {
	if v == nil {
		return "null"
	}
	b, _ := json.Marshal(v)
	if len(b) > 60 {
		b = append(b[:60], "..."...)
	}
	return string(b)
}

// TestJobBodyMatchesOpenAPI checks that the job resource, in every state the suite can
// produce, validates against the Job schema of FromTravelApp/extraction-service-api.yaml
// (required fields and their types), and that the validator itself refuses bodies that
// break the schema, so the per-response checks of the other tests are not vacuous.
func TestJobBodyMatchesOpenAPI(t *testing.T) {
	t.Run("validator refuses broken bodies", func(t *testing.T) {
		good := `{"id":"j_1","status":"queued","result":null,"feedback":[],"attempts":0,"service_version":"v",` +
			`"error":null,"created_at":"2026-10-16T09:12:03Z","finished_at":null,"expires_at":"2026-10-17T09:12:03Z"}`
		if errs := validateBody(t, "Job", []byte(good)); len(errs) != 0 {
			t.Fatalf("the contract's own example fails validation: %v", errs)
		}
		for name, broken := range map[string]string{
			"error missing":      strings.Replace(good, `"error":null,`, ``, 1),
			"attempts string":    strings.Replace(good, `"attempts":0`, `"attempts":"0"`, 1),
			"attempts float":     strings.Replace(good, `"attempts":0`, `"attempts":0.5`, 1),
			"unknown status":     strings.Replace(good, `"queued"`, `"done"`, 1),
			"feedback null":      strings.Replace(good, `"feedback":[]`, `"feedback":null`, 1),
			"result array":       strings.Replace(good, `"result":null`, `"result":[]`, 1),
			"error without code": strings.Replace(good, `"error":null`, `"error":{"message":"m"}`, 1),
			"bad timestamp":      strings.Replace(good, `"2026-10-16T09:12:03Z"`, `"16 Oct 2026"`, 1),
			"feedback entry":     strings.Replace(good, `"feedback":[]`, `"feedback":[{"pointer":""}]`, 1),
		} {
			if errs := validateBody(t, "Job", []byte(broken)); len(errs) == 0 {
				t.Errorf("%s: the validator accepts %s", name, broken)
			}
		}
	})

	c := mainClient()
	if !env.remote {
		// Every terminal state: accepted (default script), rejected, failed.
		script := scriptPerNote(map[string]func(c llmCall) llmReply{
			"openapi-rejected": func(c llmCall) llmReply {
				if c.Role == roleExtractor {
					return llmReply{Text: candidate("openapi-rejected")}
				}
				return llmReply{Text: reject("/notes", "not in the document")}
			},
			"openapi-failed": func(c llmCall) llmReply {
				if c.Role == roleExtractor {
					return llmReply{Text: candidate("openapi-failed")}
				}
				return llmReply{Text: "no verdict here"}
			},
		})
		env.llm.setScript(t, env.main.name, script)
	}
	for _, note := range []string{"openapi-accepted", "openapi-rejected", "openapi-failed"} {
		if env.remote && note != "openapi-accepted" {
			continue
		}
		created := c.submitOK(t, env.token, standardParts(textDocument(note)), nil)
		if errs := validateBody(t, "Job", created.raw); len(errs) != 0 {
			t.Errorf("202 body: %v", errs)
		}
		final := c.waitTerminal(t, env.token, created.ID)
		if errs := validateBody(t, "Job", final.raw); len(errs) != 0 {
			t.Errorf("%s terminal body: %v", final.Status, errs)
		}
		if !env.remote && !strings.HasSuffix(note, final.Status) {
			t.Errorf("%s: status %s", note, final.Status)
		}
	}
}

// scriptPerNote dispatches on the note carried by the document text (textDocument) or,
// for judge calls, by the candidate; calls matching no note get the default script.
func scriptPerNote(per map[string]func(c llmCall) llmReply) func(c llmCall) llmReply {
	return func(c llmCall) llmReply {
		for note, fn := range per {
			if c.contains(note) {
				return fn(c)
			}
		}
		return defaultScript(c)
	}
}
