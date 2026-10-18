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
// a missing entry under one of them is a removed header, field, parameter or media type.
// paths, schemas and operation responses are checked by assertSuperset.
var collections = []string{"properties", "headers", "parameters", "content", "encoding", "callbacks", "securitySchemes"}

var stabilityValues = []string{"stable", "experimental"}

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
	cur, curTree := loadDoc(t, spec.OpenAPI)
	if len(base.Paths) == 0 || len(base.Components.Schemas) == 0 {
		t.Fatal("snapshot document parsed empty")
	}
	assertSuperset(t, base, cur, nil)
	assertTreeSuperset(t, "", baseTree, curTree)

	baseCodes, curCodes := errorCodes(t, baseTree), errorCodes(t, curTree)
	if len(baseCodes) == 0 {
		t.Fatal("no error codes in the snapshot's Error schema")
	}
	for code, status := range baseCodes {
		if got, ok := curCodes[code]; !ok {
			t.Errorf("error code %s missing", code)
		} else if got != status {
			t.Errorf("error code %s: status %s, was %s", code, got, status)
		}
	}
}

// assertTreeSuperset walks base and cur side by side and reports, at any depth, a dropped
// entry of a collection, a lost enum value, a field that became required in a request
// schema or optional in a response, and a stable marker that changed.
func assertTreeSuperset(t *testing.T, path string, base, cur any) {
	t.Helper()
	switch b := base.(type) {
	case map[string]any:
		if b["x-stability"] == "experimental" {
			return
		}
		c, ok := cur.(map[string]any)
		if !ok {
			t.Errorf("%s: no longer an object", path)
			return
		}
		parent := path[strings.LastIndex(path, "/")+1:]
		for k, bv := range b {
			p := path + "/" + k
			cv, ok := c[k]
			switch {
			case k == "enum":
				assertEnumSuperset(t, p, bv, cv)
			case k == "x-stability":
				if bv == "stable" && cv != "stable" {
					t.Errorf("%s: was stable, now %v", p, cv)
				}
			case k == "required":
				assertRequired(t, p, bv, cv)
			case !ok:
				if slices.Contains(collections, parent) && !onlyExperimental(bv) {
					t.Errorf("%s missing", p)
				}
			default:
				assertTreeSuperset(t, p, bv, cv)
			}
		}
	case []any:
		c, _ := cur.([]any)
		for i, bv := range b {
			if i < len(c) {
				assertTreeSuperset(t, fmt.Sprintf("%s/%d", path, i), bv, c[i])
			}
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

func assertEnumSuperset(t *testing.T, path string, base, cur any) {
	t.Helper()
	c, ok := cur.([]any)
	if !ok {
		t.Errorf("%s: enum removed", path)
		return
	}
	for _, v := range base.([]any) {
		if !slices.Contains(c, v) {
			t.Errorf("%s: value %v missing", path, v)
		}
	}
}

// assertRequired checks required lists below the top level of a schema (assertSuperset
// checks those): a request may relax them, a response may only add to them.
func assertRequired(t *testing.T, path string, base, cur any) {
	t.Helper()
	if regexp.MustCompile(`^/components/schemas/[^/]+/required$`).MatchString(path) {
		return
	}
	b, _ := base.([]any)
	c, _ := cur.([]any)
	request := false
	for _, name := range requestSchemas {
		if strings.HasPrefix(path, "/components/schemas/"+name+"/") {
			request = true
		}
	}
	from, to, what := b, c, "no longer required"
	if request {
		from, to, what = c, b, "newly required"
	}
	for _, v := range from {
		if !slices.Contains(to, v) {
			t.Errorf("%s: %v %s", path, v, what)
		}
	}
}

// errorCodes reads the "code (status)" pairs of the Error schema's code description.
func errorCodes(t *testing.T, tree any) map[string]string {
	t.Helper()
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
