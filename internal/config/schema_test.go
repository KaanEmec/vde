package config_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// schemaFiles are every configuration file in the repository that must validate against
// the schema.
var schemaFiles = []string{
	"../../examples/*.yaml",
	"../../examples/compose/vde.yaml",
	"../../examples/compose/scaled/vde.yaml",
	"../../testdata/scenarios/config/*.yaml",
	"testdata/*.yaml",
}

// compileSchema compiles schema.json, as an editor would.
func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(config.Schema()))
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://github.com/kaanemec/vde/internal/config/schema.json"
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(url)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// jsonOf turns decoded YAML into JSON values, dropping null entries (absent for Parse).
func jsonOf(v any) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			if child != nil {
				out[k] = jsonOf(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, child := range node {
			out[i] = jsonOf(child)
		}
		return out
	}
	return v
}

func TestExamplesMatchSchema(t *testing.T) {
	t.Parallel()
	schema := compileSchema(t)
	var files []string
	for _, pattern := range schemaFiles {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(m) == 0 {
			t.Fatalf("%s matches no file", pattern)
		}
		files = append(files, m...)
	}
	for _, file := range files {
		t.Run(strings.TrimPrefix(file, "../../"), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var raw any
			if err := yaml.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			js, err := json.Marshal(jsonOf(raw))
			if err != nil {
				t.Fatal(err)
			}
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(inst); err != nil {
				t.Error(err)
			}
		})
	}
}

// schemaBase is a valid file the violation cases add one mistake to.
const schemaBase = `
endpoints:
  local:
    provider: fake
    models:
      m: {accepts: [text/plain]}
groups:
  main:
    endpoints: [{endpoint: local, model: m}]
roles:
  extractor: {group: main}
  judge: {group: main}
`

// TestFormerSchemaViolations: every file the schema refused when config check still ran
// it is refused by Parse or Validate, so by config check and serve alike, at the key.
func TestFormerSchemaViolations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, extra, key string
	}{
		{"typo in a key", "server: {listne: ':8090'}", "server.listne"},
		{"typo at the top level", "limit: {max_attempts: 3}", "limit"},
		{"wrong enum value", "documents: {pdf: {mode: images}}", "documents.pdf.mode"},
		{"wrong enum value in a list", "applications: [{id: a, judge: {policy: sometimes}}]", "applications[0].judge.policy"},
		{"wrong type", "limits: {max_attempts: three}", "limits.max_attempts"},
		{"number for a string", "applications: [{id: a, name: 42}]", "applications[0].name"},
		{"bare number duration", "limits: {job_timeout: 300}", "limits.job_timeout"},
		{"malformed duration", "worker: {lease: 2 minutes}", "worker.lease"},
		{"out of range", "documents: {pdf: {dpi: 600}}", "documents.pdf.dpi"},
		{"negative feedback cap", "limits: {feedback_message_chars: -1}", "limits.feedback_message_chars"},
		{"pool out of range with sqlite", "storage: {max_open_conns: 1001}", "storage.max_open_conns"},
		{"unknown sse with fs", "blobs: {sse: aws:kms}", "blobs.sse"},
		{"missing required key", "applications: [{name: no id}]", "applications[0].id"},
		{"numeric map key", "endpoints: {7: {provider: fake, models: {m: {accepts: [text/plain], extra: 1}}}}", "endpoints.7.models.m.extra"},
		{"not a mapping", "- a", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := schemaBase + tc.extra + "\n"
			switch {
			case strings.HasPrefix(tc.extra, "endpoints:"): // replaces the base's endpoints
				data = tc.extra + "\n" + schemaBase[strings.Index(schemaBase, "groups:"):]
			case strings.HasPrefix(tc.extra, "- "):
				data = tc.extra + "\n"
			}
			cfg, err := config.Parse([]byte(data), func(string) string { return "" })
			if err != nil {
				if !strings.Contains(err.Error(), tc.key) {
					t.Errorf("Parse: %v, want it at %s", err, tc.key)
				}
				return
			}
			issues := cfg.Validate()
			if issues.Err(false) == nil {
				t.Fatalf("accepted; issues %v", issues)
			}
			found := false
			for _, i := range issues {
				found = found || (!i.Warning && i.Key == tc.key)
			}
			if !found {
				t.Errorf("issues %v, want an error at %s", issues, tc.key)
			}
		})
	}
	t.Run("empty strings and nulls take the defaults", func(t *testing.T) {
		t.Parallel()
		for _, extra := range []string{
			`applications: [{id: a, quota: {window: ""}}]`,
			`applications: [{id: a, retention: {default: ""}}]`,
			"server:\nlimits: {pages: ~}",
		} {
			cfg, err := config.Parse([]byte(schemaBase+extra+"\n"), func(string) string { return "" })
			if err != nil {
				t.Fatalf("%s: Parse: %v", extra, err)
			}
			if err := cfg.Validate().Err(false); err != nil {
				t.Errorf("%s: Validate: %v", extra, err)
			}
		}
	})
}

// schemaNode is a decoded schema object.
type schemaNode = map[string]any

func loadSchema(t *testing.T) schemaNode {
	t.Helper()
	var root schemaNode
	if err := json.Unmarshal(config.Schema(), &root); err != nil {
		t.Fatal(err)
	}
	return root
}

// resolve follows a local $ref.
func resolve(root, n schemaNode) schemaNode {
	for n != nil {
		ref, ok := n["$ref"].(string)
		if !ok {
			return n
		}
		n, _ = root["$defs"].(schemaNode)[strings.TrimPrefix(ref, "#/$defs/")].(schemaNode)
	}
	return nil
}

// schemaLeaves returns the key paths of every leaf of the schema, map keys as <>, list
// items as [] (endpoints.<>.models.<>.accepts, applications[].id).
func schemaLeaves(root schemaNode) []string {
	var out []string
	var walk func(n schemaNode, path string)
	walk = func(n schemaNode, path string) {
		n = resolve(root, n)
		props, _ := n["properties"].(schemaNode)
		items, _ := n["items"].(schemaNode)
		extra, _ := n["additionalProperties"].(schemaNode)
		switch {
		case props != nil:
			for name, child := range props {
				walk(child.(schemaNode), strings.TrimPrefix(path+"."+name, "."))
			}
		case extra != nil:
			walk(extra, path+".<>")
		case items != nil && (resolve(root, items)["properties"] != nil):
			walk(items, path+"[]")
		default:
			out = append(out, path)
		}
	}
	walk(root, "")
	slices.Sort(out)
	return out
}

// structLeaves returns the key paths of every leaf of config.Config in the notation of
// schemaLeaves.
func structLeaves() []string {
	var out []string
	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		switch {
		case typ == reflect.TypeFor[time.Duration]():
		case typ.Kind() == reflect.Map:
			walk(typ.Elem(), path+".<>")
			return
		case typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Struct:
			walk(typ.Elem(), path+"[]")
			return
		case typ.Kind() == reflect.Struct:
			for f := range typ.Fields() {
				if !f.IsExported() {
					continue
				}
				name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
				if opts == "inline" {
					walk(f.Type, path)
					continue
				}
				if name == "" || name == "-" {
					continue
				}
				walk(f.Type, strings.TrimPrefix(path+"."+name, "."))
			}
			return
		}
		out = append(out, path)
	}
	walk(reflect.TypeFor[config.Config](), "")
	slices.Sort(out)
	return out
}

// notInSchema are keys the decoder accepts only for Validate to refuse them.
var notInSchema = []string{"roles.extractor.policy"}

// TestSchemaMatchesStructs keeps schema.json and config.Config in step: every key of the
// struct is in the schema, and the schema has no key the decoder would refuse.
func TestSchemaMatchesStructs(t *testing.T) {
	t.Parallel()
	schema, structs := schemaLeaves(loadSchema(t)), structLeaves()
	for _, k := range structs {
		if !slices.Contains(schema, k) && !slices.Contains(notInSchema, k) {
			t.Errorf("schema.json lacks %s", k)
		}
	}
	for _, k := range schema {
		if !slices.Contains(structs, k) {
			t.Errorf("schema.json has %s, which config.Config does not decode", k)
		}
	}
	if len(schema) < 90 {
		t.Fatalf("walked %d schema keys; is the walk broken?", len(schema))
	}
}

// TestDocumentedKeysAreInSchema checks that every key in the Key column of
// docs/operator/configuration.md's tables exists in the schema.
func TestDocumentedKeysAreInSchema(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "operator", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	root := loadSchema(t)
	leaves := schemaLeaves(root)
	// Section headings that put the table's keys below a map or a list.
	prefixes := map[string]string{
		"endpoints.<name>": "endpoints.<>.",
		"groups.<name>":    "groups.<>.",
		"applications[]":   "applications[].",
	}
	heading := regexp.MustCompile("^## `([^`]+)`")
	keyCell := regexp.MustCompile("`([^`]+)`")
	placeholder := regexp.MustCompile(`<[a-z_]+>`)
	prefix, inTable, checked := "", false, 0
	for line := range strings.Lines(string(data)) {
		if m := heading.FindStringSubmatch(line); m != nil {
			prefix = prefixes[m[1]]
		}
		if strings.HasPrefix(line, "| Key |") {
			inTable = true
			continue
		}
		if !strings.HasPrefix(line, "|") {
			inTable = false
			continue
		}
		if !inTable || strings.HasPrefix(line, "| ---") {
			continue
		}
		cell := strings.Split(line, "|")[1]
		for _, m := range keyCell.FindAllStringSubmatch(cell, -1) {
			key := placeholder.ReplaceAllString(prefix+m[1], "<>")
			checked++
			found := slices.ContainsFunc(leaves, func(l string) bool {
				return l == key || strings.HasPrefix(l, key+".") || strings.HasPrefix(l, key+"[]")
			})
			if !found {
				t.Errorf("configuration.md documents %s, which schema.json lacks", key)
			}
		}
	}
	if checked < 80 {
		t.Fatalf("checked %d documented keys; is the parse broken?", checked)
	}
}
