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

// TestSupersetProblemsCatchLosses removes or changes one thing of each kind in a copy of
// the snapshot and checks supersetProblems reports it, and that a relaxed field is not.
func TestSupersetProblemsCatchLosses(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(snapshotDoc)
	if err != nil {
		t.Fatal(err)
	}
	_, snapshot := loadDoc(t, data)
	if p := supersetProblems(snapshot, snapshot, nil); len(p) != 0 {
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
	withoutRef := func(list any, ref string) []any {
		return slices.DeleteFunc(slices.Clone(list.([]any)), func(x any) bool { return x.(map[string]any)["$ref"] == ref })
	}
	for _, tt := range []struct {
		name, want string // want "" is no problem at all
		change     func(cur any)
		prepare    func(doc any)       // applied to base and cur before change
		relaxed    map[string][]string // passed to supersetProblems
	}{
		{name: "path", want: "/paths//quota missing", change: func(cur any) { delete(node(cur, "paths"), "/quota") }},
		{name: "operation", want: "/paths//jobs/{id}/delete missing", change: func(cur any) { delete(node(cur, "paths", "/jobs/{id}"), "delete") }},
		{name: "response", want: "/paths//jobs/post/responses/202 missing", change: func(cur any) {
			delete(node(cur, "paths", "/jobs", "post", "responses"), "202")
		}},
		{name: "schema", want: "/components/schemas/Quota missing", change: func(cur any) { delete(node(cur, "components", "schemas"), "Quota") }},
		{name: "property", want: "/components/schemas/Job/properties/attempts missing", change: func(cur any) {
			delete(node(cur, "components", "schemas", "Job", "properties"), "attempts")
		}},
		{name: "header", want: "/paths//jobs/post/responses/202/headers/Location missing", change: func(cur any) {
			delete(node(cur, "paths", "/jobs", "post", "responses", "202", "headers"), "Location")
		}},
		{name: "enum value", want: "/components/schemas/JobStatus/enum: value extracted missing", change: func(cur any) {
			s := node(cur, "components", "schemas", "JobStatus")
			s["enum"] = without(s["enum"], "extracted")
		}},
		{name: "error code", want: "error code forbidden missing", change: func(cur any) {
			code := node(cur, "components", "schemas", "Error", "properties", "error", "properties", "code")
			code["description"] = strings.Replace(code["description"].(string), "forbidden (403)", "", 1)
		}},
		{name: "error code status", want: "error code not_found: status 410, was 404", change: func(cur any) {
			code := node(cur, "components", "schemas", "Error", "properties", "error", "properties", "code")
			code["description"] = strings.Replace(code["description"].(string), "not_found (404)", "not_found (410)", 1)
		}},
		{name: "required field", want: "/components/schemas/Job/required: attempts no longer required", change: func(cur any) {
			s := node(cur, "components", "schemas", "Job")
			s["required"] = without(s["required"], "attempts")
		}},
		{name: "nested required field", want: "/components/schemas/Error/properties/error/required: code no longer required", change: func(cur any) {
			s := node(cur, "components", "schemas", "Error", "properties", "error")
			s["required"] = without(s["required"], "code")
		}},
		{name: "newly required request field", want: "/components/schemas/SubmitJob/required: retention newly required", change: func(cur any) {
			s := node(cur, "components", "schemas", "SubmitJob")
			s["required"] = append(slices.Clone(s["required"].([]any)), "retention")
		}},
		{name: "parameter by ref", want: "/paths//jobs/post/parameters: $ref #/components/parameters/IdempotencyKey missing", change: func(cur any) {
			op := node(cur, "paths", "/jobs", "post")
			op["parameters"] = withoutRef(op["parameters"], "#/components/parameters/IdempotencyKey")
		}},
		{name: "inline parameter", want: "/paths//jobs/{id}/parameters: parameter path id missing", change: func(cur any) {
			node(cur, "paths", "/jobs/{id}")["parameters"] = []any{}
		}},
		{name: "media type", want: "/paths//jobs/post/requestBody/content/application/json missing", change: func(cur any) {
			delete(node(cur, "paths", "/jobs", "post", "requestBody", "content"), "application/json")
		}},
		{name: "stable callback", want: "/paths//jobs/post/callbacks/jobFinished missing", prepare: func(doc any) {
			node(doc, "paths", "/jobs", "post", "callbacks", "jobFinished", "{$request.body#/webhook}", "post")["x-stability"] = "stable"
		}, change: func(cur any) {
			delete(node(cur, "paths", "/jobs", "post", "callbacks"), "jobFinished")
		}},
		{name: "security scheme", want: "/components/securitySchemes/bearer missing", change: func(cur any) {
			delete(node(cur, "components", "securitySchemes"), "bearer")
		}},
		{name: "stable to experimental", want: "/paths//jobs/post/x-stability: was stable, now experimental", change: func(cur any) {
			node(cur, "paths", "/jobs", "post")["x-stability"] = "experimental"
		}},
		{name: "parameter no longer required", want: "/paths//jobs/{id}/parameters/0/required: no longer required", change: func(cur any) {
			node(cur, "paths", "/jobs/{id}")["parameters"].([]any)[0].(map[string]any)["required"] = false
		}},
		{name: "parameter newly required", want: "/components/parameters/IdempotencyKey/required: newly required", change: func(cur any) {
			node(cur, "components", "parameters", "IdempotencyKey")["required"] = true
		}},
		{name: "request body no longer required", want: "/paths//jobs/post/requestBody/required: no longer required", change: func(cur any) {
			delete(node(cur, "paths", "/jobs", "post", "requestBody"), "required")
		}},
		{name: "changed ref", want: "/paths//jobs/{id}/get/responses/200/content/application/json/schema/$ref: #/components/schemas/Quota, was #/components/schemas/Job", change: func(cur any) {
			node(cur, "paths", "/jobs/{id}", "get", "responses", "200", "content", "application/json", "schema")["$ref"] = "#/components/schemas/Quota"
		}},
		{name: "changed type", want: "/components/schemas/Quota/properties/limit/type: [string], was [integer]", change: func(cur any) {
			node(cur, "components", "schemas", "Quota", "properties", "limit")["type"] = "string"
		}},
		{name: "relaxed required field", relaxed: map[string][]string{"Job": {"attempts"}}, change: func(cur any) {
			s := node(cur, "components", "schemas", "Job")
			s["required"] = without(s["required"], "attempts")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, base := loadDoc(t, data)
			_, cur := loadDoc(t, data)
			if tt.prepare != nil {
				tt.prepare(base)
				tt.prepare(cur)
			}
			tt.change(cur)
			p := supersetProblems(base, cur, tt.relaxed)
			switch {
			case tt.want == "" && len(p) != 0:
				t.Errorf("problems %q, want none", p)
			case tt.want != "" && !slices.Contains(p, tt.want):
				t.Errorf("problems %q lack %q", p, tt.want)
			}
		})
	}
}

// supersetProblems walks base and cur side by side and lists what cur lost, at any depth:
// an entry of a collection or an operation, a list item (a parameter, a server, a tag, a
// oneOf alternative), an enum value, an error code of the Error schema or its status, a
// required field, a changed $ref or type, and a stable marker that changed. A response
// may only add required fields, a request schema none (it may relax nested ones), and a
// parameter or request body stays as required as it was; at a schema's top level relaxed
// names the fields a request deliberately makes optional. A subtree marked experimental
// may change; examples may change.
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
			// Below a collection the keys are names (a property called "type"), not keywords.
			keywords := !slices.Contains(collections, parent)
			if _, ok := b["required"]; keywords && !ok {
				if cv, ok := c["required"]; ok {
					requiredProblems(add, path+"/required", nil, cv, relaxed)
				}
			}
			for k, bv := range b {
				p := path + "/" + k
				cv, ok := c[k]
				switch {
				case keywords && (k == "example" || k == "examples"):
				case keywords && k == "enum":
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
				case keywords && k == "x-stability":
					if bv == "stable" && cv != "stable" {
						add("%s: was stable, now %v", p, cv)
					}
				case keywords && k == "required":
					requiredProblems(add, p, bv, cv, relaxed)
				case keywords && k == "$ref":
					if cv != bv {
						add("%s: %v, was %v", p, cv, bv)
					}
				case keywords && k == "type" && !isMap(bv):
					if bt, ct := typeSet(bv), typeSet(cv); !slices.Equal(bt, ct) {
						add("%s: %v, was %v", p, ct, bt)
					}
				case !ok:
					if (slices.Contains(collections, parent) || slices.Contains(httpMethods, k)) && !onlyExperimental(bv) {
						add("%s missing", p)
					}
				default:
					walk(p, k, bv, cv)
				}
			}
		case []any:
			// Lists are sets: an item with an identity ($ref, a parameter's in and name, a
			// tag's name, a server's url, a scalar's value) is matched by it wherever it
			// stands; the others are matched in order among the items without one.
			c, _ := cur.([]any)
			var anonymous []any
			for _, cv := range c {
				if itemKey(cv) == "" {
					anonymous = append(anonymous, cv)
				}
			}
			n := 0
			for i, bv := range b {
				p := fmt.Sprintf("%s/%d", path, i)
				key := itemKey(bv)
				if key == "" {
					if n < len(anonymous) {
						walk(p, fmt.Sprint(i), bv, anonymous[n])
					} else if !onlyExperimental(bv) {
						add("%s missing", p)
					}
					n++
					continue
				}
				j := slices.IndexFunc(c, func(cv any) bool { return itemKey(cv) == key })
				switch {
				case j >= 0:
					walk(p, fmt.Sprint(i), bv, c[j])
				case !onlyExperimental(bv):
					add("%s: %s missing", path, key)
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

// itemKey is the identity a list item is matched by, "" for an object without one.
func itemKey(item any) string {
	m, ok := item.(map[string]any)
	switch {
	case !ok:
		return fmt.Sprintf("value %v", item)
	case m["$ref"] != nil:
		return fmt.Sprintf("$ref %v", m["$ref"])
	case m["name"] != nil && m["in"] != nil:
		return fmt.Sprintf("parameter %v %v", m["in"], m["name"])
	case m["name"] != nil:
		return fmt.Sprintf("name %v", m["name"])
	case m["url"] != nil:
		return fmt.Sprintf("url %v", m["url"])
	}
	return ""
}

func isMap(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

// typeSet is a schema's type as a sorted list: "string" and [string] are the same type.
func typeSet(v any) []string {
	var types []string
	switch v := v.(type) {
	case string:
		types = []string{v}
	case []any:
		for _, t := range v {
			types = append(types, fmt.Sprint(t))
		}
	}
	slices.Sort(types)
	return types
}

// requiredProblems compares a schema's required lists, or the required flags of a
// parameter, request body or header. A request's parameter or body may not become
// required; a response header may.
func requiredProblems(add func(string, ...any), path string, base, cur any, relaxed map[string][]string) {
	if _, list := base.([]any); !list {
		if _, list := cur.([]any); !list {
			switch {
			case base == true && cur != true:
				add("%s: no longer required", path)
			case base != true && cur == true && !strings.Contains(path, "/responses/") && !strings.HasPrefix(path, "/components/headers/"):
				add("%s: newly required", path)
			}
			return
		}
	}
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
