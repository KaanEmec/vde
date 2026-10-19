package api

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	spec "github.com/kaanemec/vde/api"
	"github.com/kaanemec/vde/internal/document"
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

func TestOpenAPIMatchesRoutes(t *testing.T) {
	t.Parallel()
	doc, _ := loadDoc(t, spec.OpenAPI)
	documented := map[string]bool{}
	for path, item := range doc.Paths {
		for _, m := range operations(item) {
			documented[strings.ToUpper(m)+" "+path] = true
		}
	}
	for _, rt := range newServer(testDeps(t, Deps{})).routes() {
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

// TestAdminOpenAPIMatchesRoutes checks every admin route against api/admin-openapi.yaml,
// whose server base path is /admin/v1, and back.
func TestAdminOpenAPIMatchesRoutes(t *testing.T) {
	t.Parallel()
	doc, _ := loadDoc(t, spec.AdminOpenAPI)
	documented := map[string]bool{}
	for path, item := range doc.Paths {
		for _, m := range operations(item) {
			documented[strings.ToUpper(m)+" "+path] = true
		}
	}
	routes := newServerWith(testDeps(t, Deps{}), true).adminRoutes()
	if len(routes) == 0 {
		t.Fatal("no admin routes")
	}
	for _, rt := range routes {
		method, path, _ := strings.Cut(rt.pattern, " ")
		p, ok := strings.CutPrefix(path, "/admin/v1")
		if !ok {
			t.Errorf("admin route %q is not under /admin/v1", rt.pattern)
			continue
		}
		key := method + " " + p
		if !documented[key] {
			t.Errorf("admin route %q is not in api/admin-openapi.yaml as %s", rt.pattern, key)
		}
		delete(documented, key)
	}
	for key := range documented {
		t.Errorf("api/admin-openapi.yaml documents %s, which no admin route serves", key)
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

// TestOpenAPISupersetOfTravelInbox checks TravelInbox's document against ours with the
// snapshot's checker: every path, method, response, schema, field, header, enum value,
// error code and required field of theirs is in ours, SubmitJob.schema relaxed.
func TestOpenAPISupersetOfTravelInbox(t *testing.T) {
	t.Parallel()
	theirsData, err := os.ReadFile(travelInboxDoc)
	if err != nil {
		t.Fatal(err)
	}
	theirs, theirsTree := loadDoc(t, theirsData)
	_, ours := loadDoc(t, spec.OpenAPI)
	if len(theirs.Paths) == 0 || len(theirs.Components.Schemas) == 0 {
		t.Fatal("TravelInbox document parsed empty")
	}
	for _, p := range supersetProblems(theirsTree, ours, relaxedRequired) {
		t.Error(p)
	}
}

// TestAdminJobRepresentationEnum: JobMetadata.representation (additionalProperties
// false) lists every label a job can store, the service-prepared PDF ones of ADR 0010
// included.
func TestAdminJobRepresentationEnum(t *testing.T) {
	t.Parallel()
	doc, _ := loadDoc(t, spec.AdminOpenAPI)
	prop, _ := doc.Components.Schemas["JobMetadata"].Properties["representation"].(map[string]any)
	var enum []string
	for _, v := range prop["enum"].([]any) {
		enum = append(enum, v.(string))
	}
	for _, rep := range []document.Representation{
		{Kind: document.Native}, {Kind: document.Pages}, {Kind: document.Text},
		{Kind: document.Text, Source: document.SourceTextLayer}, {Kind: document.Pages, Source: document.SourceRendered},
	} {
		if !slices.Contains(enum, rep.Label()) {
			t.Errorf("JobMetadata.representation enum %v lacks %q", enum, rep.Label())
		}
	}
}
