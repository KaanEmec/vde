package api

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	spec "github.com/kaanemec/vde/api"
)

// snapshotDoc is the /v1 document as frozen at 1.0.0 (docs/compatibility.md); the current
// api/openapi.yaml must stay a superset of it.
const snapshotDoc = "../../api/openapi.v1.0.0.yaml"

// collections are the keys whose map entries a newer document may add to but never drop:
// a missing entry under one of them is a removed path, response, schema, header, field,
// parameter or media type. A missing HTTP method is a removed operation.
var collections = []string{"paths", "responses", "schemas", "properties", "headers", "parameters", "content",
	"encoding", "callbacks", "securitySchemes"}

var stabilityValues = []string{"stable", "experimental"}

// requestSchemas are the schemas clients send: a newer document may relax their nested
// required fields but never add one.
var requestSchemas = []string{"SubmitJob", "SubmitJobJSON"}

// topRequired matches a schema's top-level required list.
var topRequired = regexp.MustCompile(`^/components/schemas/([^/]+)/required$`)

// TestOpenAPISupersetOfSnapshot: within /v1 changes are additive only (contract §11).
// Paths, methods, response codes, fields, headers and required fields of the 1.0.0
// document stay; enums (statuses, verification, feedback sources, request values) and
// error codes only grow; what was stable stays stable.
func TestOpenAPISupersetOfSnapshot(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(snapshotDoc)
	if err != nil {
		t.Fatal(err)
	}
	base, baseTree := loadDoc(t, data)
	_, curTree := loadDoc(t, spec.OpenAPI)
	if len(base.Paths) == 0 || len(base.Components.Schemas) == 0 || len(errorCodes(baseTree)) == 0 {
		t.Fatal("snapshot document parsed empty or without error codes")
	}
	for _, p := range supersetProblems(baseTree, curTree, nil) {
		t.Error(p)
	}
}

// TestSupersetProblemsCatchLosses removes one thing of each kind from a copy of the
// snapshot and checks supersetProblems reports it.
func TestSupersetProblemsCatchLosses(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(snapshotDoc)
	if err != nil {
		t.Fatal(err)
	}
	_, base := loadDoc(t, data)
	if p := supersetProblems(base, base, nil); len(p) != 0 {
		t.Fatalf("the snapshot is not a superset of itself: %v", p)
	}
	node := func(tree any, keys ...string) map[string]any {
		for _, k := range keys {
			tree = tree.(map[string]any)[k]
		}
		return tree.(map[string]any)
	}
	without := func(list any, v string) []any {
		return slices.DeleteFunc(slices.Clone(list.([]any)), func(x any) bool { return x == v })
	}
	for _, tt := range []struct {
		name, want string
		change     func(cur any)
	}{
		{"path", "/paths//quota missing", func(cur any) { delete(node(cur, "paths"), "/quota") }},
		{"operation", "/paths//jobs/{id}/delete missing", func(cur any) { delete(node(cur, "paths", "/jobs/{id}"), "delete") }},
		{"response", "/paths//jobs/post/responses/202 missing", func(cur any) {
			delete(node(cur, "paths", "/jobs", "post", "responses"), "202")
		}},
		{"schema", "/components/schemas/Quota missing", func(cur any) { delete(node(cur, "components", "schemas"), "Quota") }},
		{"property", "/components/schemas/Job/properties/attempts missing", func(cur any) {
			delete(node(cur, "components", "schemas", "Job", "properties"), "attempts")
		}},
		{"header", "/paths//jobs/post/responses/202/headers/Location missing", func(cur any) {
			delete(node(cur, "paths", "/jobs", "post", "responses", "202", "headers"), "Location")
		}},
		{"enum value", "/components/schemas/JobStatus/enum: value extracted missing", func(cur any) {
			s := node(cur, "components", "schemas", "JobStatus")
			s["enum"] = without(s["enum"], "extracted")
		}},
		{"error code", "error code forbidden missing", func(cur any) {
			code := node(cur, "components", "schemas", "Error", "properties", "error", "properties", "code")
			code["description"] = strings.Replace(code["description"].(string), "forbidden (403)", "", 1)
		}},
		{"error code status", "error code not_found: status 410, was 404", func(cur any) {
			code := node(cur, "components", "schemas", "Error", "properties", "error", "properties", "code")
			code["description"] = strings.Replace(code["description"].(string), "not_found (404)", "not_found (410)", 1)
		}},
		{"required field", "/components/schemas/Job/required: attempts no longer required", func(cur any) {
			s := node(cur, "components", "schemas", "Job")
			s["required"] = without(s["required"], "attempts")
		}},
		{"nested required field", "/components/schemas/Error/properties/error/required: code no longer required", func(cur any) {
			s := node(cur, "components", "schemas", "Error", "properties", "error")
			s["required"] = without(s["required"], "code")
		}},
		{"newly required request field", "/components/schemas/SubmitJob/required: retention newly required", func(cur any) {
			s := node(cur, "components", "schemas", "SubmitJob")
			s["required"] = append(slices.Clone(s["required"].([]any)), "retention")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, cur := loadDoc(t, data)
			tt.change(cur)
			if p := supersetProblems(base, cur, nil); !slices.Contains(p, tt.want) {
				t.Errorf("problems %q lack %q", p, tt.want)
			}
		})
	}
}

// supersetProblems walks base and cur side by side and lists what cur lost, at any depth:
// an entry of a collection or an operation, an enum value, an error code of the Error
// schema or its status, a required field, and a stable marker that changed. A response
// may only add required fields, a request schema none (it may relax nested ones); at a
// schema's top level relaxed names the fields a request deliberately makes optional.
// A subtree marked experimental may change.
func supersetProblems(base, cur any, relaxed map[string][]string) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	var walk func(path, parent string, base, cur any)
	walk = func(path, parent string, base, cur any) {
		switch b := base.(type) {
		case map[string]any:
			if b["x-stability"] == "experimental" {
				return
			}
			c, ok := cur.(map[string]any)
			if !ok {
				add("%s: no longer an object", path)
				return
			}
			for k, bv := range b {
				p := path + "/" + k
				cv, ok := c[k]
				switch {
				case k == "enum":
					cl, ok := cv.([]any)
					if !ok {
						add("%s: enum removed", p)
						continue
					}
					for _, v := range bv.([]any) {
						if !slices.Contains(cl, v) {
							add("%s: value %v missing", p, v)
						}
					}
				case k == "x-stability":
					if bv == "stable" && cv != "stable" {
						add("%s: was stable, now %v", p, cv)
					}
				case k == "required":
					requiredProblems(add, p, bv, cv, relaxed)
				case !ok:
					if (slices.Contains(collections, parent) || slices.Contains(httpMethods, k)) && !onlyExperimental(bv) {
						add("%s missing", p)
					}
				default:
					walk(p, k, bv, cv)
				}
			}
		case []any:
			c, _ := cur.([]any)
			for i, bv := range b {
				if i < len(c) {
					walk(fmt.Sprintf("%s/%d", path, i), fmt.Sprint(i), bv, c[i])
				}
			}
		}
	}
	walk("", "", base, cur)
	curCodes := errorCodes(cur)
	for code, status := range errorCodes(base) {
		if got, ok := curCodes[code]; !ok {
			add("error code %s missing", code)
		} else if got != status {
			add("error code %s: status %s, was %s", code, got, status)
		}
	}
	slices.Sort(problems)
	return problems
}

// requiredProblems compares the required lists of one schema.
func requiredProblems(add func(string, ...any), path string, base, cur any, relaxed map[string][]string) {
	b, _ := base.([]any)
	c, _ := cur.([]any)
	request := slices.ContainsFunc(requestSchemas, func(name string) bool {
		return strings.HasPrefix(path, "/components/schemas/"+name+"/")
	})
	if m := topRequired.FindStringSubmatch(path); m != nil {
		for _, v := range b {
			if !slices.Contains(c, v) && !slices.Contains(relaxed[m[1]], fmt.Sprint(v)) {
				add("%s: %v no longer required", path, v)
			}
		}
		if request {
			for _, v := range c {
				if !slices.Contains(b, v) {
					add("%s: %v newly required", path, v)
				}
			}
		}
		return
	}
	from, to, what := b, c, "no longer required"
	if request {
		from, to, what = c, b, "newly required"
	}
	for _, v := range from {
		if !slices.Contains(to, v) {
			add("%s: %v %s", path, v, what)
		}
	}
}

// onlyExperimental reports whether a subtree carries an experimental marker and no stable
// one: an experimental callback or field may be dropped.
func onlyExperimental(n any) bool {
	var stable, experimental bool
	var walk func(any)
	walk = func(n any) {
		switch n := n.(type) {
		case map[string]any:
			switch n["x-stability"] {
			case "stable":
				stable = true
			case "experimental":
				experimental = true
			}
			for _, v := range n {
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(n)
	return experimental && !stable
}

// errorCodes reads the "code (status)" pairs of the Error schema's code description.
func errorCodes(tree any) map[string]string {
	node := tree
	for _, k := range strings.Split("components/schemas/Error/properties/error/properties/code/description", "/") {
		m, _ := node.(map[string]any)
		node = m[k]
	}
	desc, _ := node.(string)
	codes := map[string]string{}
	for _, m := range regexp.MustCompile(`\b([a-z_]+) \((\d{3})`).FindAllStringSubmatch(desc, -1) {
		codes[m[1]] = m[2]
	}
	return codes
}

// TestOpenAPIStabilityMarkers: every operation, callbacks included, is marked stable or
// experimental, and no marker anywhere has another value.
func TestOpenAPIStabilityMarkers(t *testing.T) {
	t.Parallel()
	doc, tree := loadDoc(t, spec.OpenAPI)
	for path, item := range doc.Paths {
		for _, m := range operations(item) {
			op, _ := item[m].(map[string]any)
			if !slices.Contains(stabilityValues, fmt.Sprint(op["x-stability"])) {
				t.Errorf("%s %s: x-stability %v, want one of %v", strings.ToUpper(m), path, op["x-stability"], stabilityValues)
			}
			callbacks, _ := op["callbacks"].(map[string]any)
			for name, cb := range callbacks {
				exprs, _ := cb.(map[string]any)
				for _, e := range exprs {
					cbItem, _ := e.(map[string]any)
					for _, cm := range operations(cbItem) {
						cbOp, _ := cbItem[cm].(map[string]any)
						if !slices.Contains(stabilityValues, fmt.Sprint(cbOp["x-stability"])) {
							t.Errorf("callback %s of %s %s: x-stability %v", name, strings.ToUpper(m), path, cbOp["x-stability"])
						}
					}
				}
			}
		}
	}
	var walk func(string, any)
	walk = func(path string, n any) {
		switch n := n.(type) {
		case map[string]any:
			for k, v := range n {
				if k == "x-stability" && !slices.Contains(stabilityValues, fmt.Sprint(v)) {
					t.Errorf("%s/x-stability: %v, want one of %v", path, v, stabilityValues)
				}
				walk(path+"/"+k, v)
			}
		case []any:
			for i, v := range n {
				walk(fmt.Sprintf("%s/%d", path, i), v)
			}
		}
	}
	walk("", tree)
}
