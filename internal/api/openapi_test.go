package api

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	spec "github.com/kaanemec/vde/api"
)

// travelInboxDoc is the consumer's document ours must be a superset of (ADR 0005).
const travelInboxDoc = "../../FromTravelApp/extraction-service-api.yaml"

// relaxedRequired lists the required fields we deliberately make optional, each a
// superset of the consumer's request (open question 6: schema-less mode).
var relaxedRequired = map[string][]string{"SubmitJob": {"schema"}}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type openAPIDoc struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Required   []string       `yaml:"required"`
			Properties map[string]any `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadDoc(t *testing.T, data []byte) (openAPIDoc, any) {
	t.Helper()
	var doc openAPIDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	return doc, tree
}

// docPath maps a mux path to the document's: /v1 is the servers' base path, and root
// routes are documented with their own servers.
func docPath(path string) (string, bool) {
	if p, ok := strings.CutPrefix(path, "/v1"); ok {
		return p, false
	}
	return path, true
}

func operations(item map[string]any) []string {
	var ops []string
	for k := range item {
		if slices.Contains(httpMethods, k) {
			ops = append(ops, k)
		}
	}
	return ops
}

func responses(item map[string]any, method string) map[string]any {
	op, _ := item[method].(map[string]any)
	r, _ := op["responses"].(map[string]any)
	return r
}

func TestOpenAPIMatchesRoutes(t *testing.T) {
	t.Parallel()
	doc, _ := loadDoc(t, spec.OpenAPI)
	documented := map[string]bool{}
	for path, item := range doc.Paths {
		for _, m := range operations(item) {
			documented[strings.ToUpper(m)+" "+path] = true
		}
	}
	for _, rt := range newServer(Deps{}).routes() {
		method, path, _ := strings.Cut(rt.pattern, " ")
		p, root := docPath(path)
		key := method + " " + p
		if !documented[key] {
			t.Errorf("route %q is not in api/openapi.yaml as %s", rt.pattern, key)
			continue
		}
		delete(documented, key)
		op, _ := doc.Paths[p][strings.ToLower(method)].(map[string]any)
		if _, ok := op["servers"]; root && !ok {
			t.Errorf("root route %q: %s needs its own servers (not under /v1)", rt.pattern, key)
		}
	}
	for key := range documented {
		t.Errorf("api/openapi.yaml documents %s, which no route serves", key)
	}
}

func TestOpenAPIDescribesErrorCodes(t *testing.T) {
	t.Parallel()
	_, tree := loadDoc(t, spec.OpenAPI)
	var descriptions strings.Builder
	var walk func(any)
	walk = func(n any) {
		switch n := n.(type) {
		case map[string]any:
			for k, v := range n {
				if s, ok := v.(string); ok && k == "description" {
					descriptions.WriteString(s + "\n")
				}
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(tree)
	all := descriptions.String()
	codes := []string{reasonExtractorDown, reasonJudgeDown}
	for code := range codeStatus {
		codes = append(codes, code)
	}
	for _, code := range codes {
		if !regexp.MustCompile(`\b` + code + `\b`).MatchString(all) {
			t.Errorf("code %q appears in no description of api/openapi.yaml", code)
		}
	}
}

// TestOpenAPISupersetOfTravelInbox checks every path, method, response code and required
// field of TravelInbox's document is in ours (job 03 acceptance).
func TestOpenAPISupersetOfTravelInbox(t *testing.T) {
	t.Parallel()
	theirsData, err := os.ReadFile(travelInboxDoc)
	if err != nil {
		t.Fatal(err)
	}
	theirs, _ := loadDoc(t, theirsData)
	ours, _ := loadDoc(t, spec.OpenAPI)
	if len(theirs.Paths) == 0 || len(theirs.Components.Schemas) == 0 {
		t.Fatal("TravelInbox document parsed empty")
	}
	for path, item := range theirs.Paths {
		ourItem, ok := ours.Paths[path]
		if !ok {
			t.Errorf("path %s missing", path)
			continue
		}
		for _, m := range operations(item) {
			if _, ok := ourItem[m]; !ok {
				t.Errorf("%s %s missing", strings.ToUpper(m), path)
				continue
			}
			ourResponses := responses(ourItem, m)
			for code := range responses(item, m) {
				if _, ok := ourResponses[code]; !ok {
					t.Errorf("%s %s: response %s missing", strings.ToUpper(m), path, code)
				}
			}
		}
	}
	for name, schema := range theirs.Components.Schemas {
		our, ok := ours.Components.Schemas[name]
		if !ok {
			t.Errorf("schema %s missing", name)
			continue
		}
		for _, field := range schema.Required {
			if _, ok := our.Properties[field]; !ok {
				t.Errorf("schema %s: required field %s missing", name, field)
			}
			if !slices.Contains(our.Required, field) && !slices.Contains(relaxedRequired[name], field) {
				t.Errorf("schema %s: field %s is no longer required", name, field)
			}
		}
	}
}
