package config_test

import (
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

func TestSchemaCompiles(t *testing.T) {
	t.Parallel()
	if !json.Valid(config.Schema()) {
		t.Fatal("schema.json is not valid JSON")
	}
	if _, err := config.CheckSchema([]byte("endpoints: {}\ngroups: {}\nroles: {extractor: {group: a}, judge: {group: a}}\n")); err != nil {
		t.Fatalf("CheckSchema: %v", err)
	}
}

func TestExamplesMatchSchema(t *testing.T) {
	t.Parallel()
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
			issues, err := config.CheckSchema(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range issues {
				t.Errorf("%s", i)
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

func TestCheckSchemaViolations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, extra, key, message string
	}{
		{"typo in a key", "server: {listne: ':8090'}", "server.listne", "unknown key"},
		{"typo at the top level", "limit: {max_attempts: 3}", "limit", "unknown key"},
		{"wrong enum value", "documents: {pdf: {mode: images}}", "documents.pdf.mode", "value must be one of"},
		{"wrong enum value in a list", "applications: [{id: a, judge: {policy: sometimes}}]", "applications[0].judge.policy", "value must be one of"},
		{"wrong type", "limits: {max_attempts: three}", "limits.max_attempts", "want integer"},
		{"bare number duration", "limits: {job_timeout: 300}", "limits.job_timeout", "want string"},
		{"malformed duration", "worker: {lease: 2 minutes}", "worker.lease", `"2 minutes" does not have the expected form; expected: A Go duration`},
		{"out of range", "documents: {pdf: {dpi: 600}}", "documents.pdf.dpi", "maximum"},
		{"missing required key", "applications: [{name: no id}]", "applications[0].id", "is required"},
		{"numeric map key", "endpoints: {7: {provider: fake, models: {m: {accepts: [text/plain], extra: 1}}}}", "endpoints.7.models.m.extra", "unknown key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := schemaBase + tc.extra + "\n"
			if strings.HasPrefix(tc.extra, "endpoints:") { // replaces the base's endpoints
				data = tc.extra + "\n" + schemaBase[strings.Index(schemaBase, "groups:"):]
			}
			issues, err := config.CheckSchema([]byte(data))
			if err != nil {
				t.Fatal(err)
			}
			if len(issues) != 1 {
				t.Fatalf("got %d violations, want 1: %v", len(issues), issues)
			}
			if issues[0].Key != tc.key || !strings.Contains(issues[0].Message, tc.message) || issues[0].Warning {
				t.Errorf("got %v, want error at %s containing %q", issues[0], tc.key, tc.message)
			}
		})
	}
}

func TestCheckSchemaInput(t *testing.T) {
	t.Parallel()
	t.Run("not YAML", func(t *testing.T) {
		t.Parallel()
		if _, err := config.CheckSchema([]byte("endpoints: [unclosed")); err == nil {
			t.Error("no error for a YAML syntax error")
		}
	})
	t.Run("null counts as absent", func(t *testing.T) {
		t.Parallel()
		issues, err := config.CheckSchema([]byte(schemaBase + "server:\nlimits: {pages: ~}\n"))
		if err != nil || len(issues) != 0 {
			t.Errorf("got %v, %v; want no violations", issues, err)
		}
	})
	t.Run("empty strings take the defaults", func(t *testing.T) {
		t.Parallel()
		for _, extra := range []string{
			`applications: [{id: a, quota: {window: ""}}]`,
			`applications: [{id: a, retention: {default: ""}}]`,
		} {
			data := []byte(schemaBase + extra + "\n")
			if issues, err := config.CheckSchema(data); err != nil || len(issues) != 0 {
				t.Errorf("%s: got %v, %v; want no violations", extra, issues, err)
			}
			cfg, err := config.Parse(data, func(string) string { return "" })
			if err != nil {
				t.Fatalf("%s: Parse: %v", extra, err)
			}
			if err := cfg.Validate().Err(false); err != nil {
				t.Errorf("%s: Validate: %v", extra, err)
			}
		}
	})
	t.Run("empty file", func(t *testing.T) {
		t.Parallel()
		issues, err := config.CheckSchema(nil)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, i := range issues {
			keys = append(keys, i.Key)
		}
		if want := []string{"endpoints", "groups", "roles"}; !slices.Equal(keys, want) {
			t.Errorf("got %v, want missing %v", keys, want)
		}
	})
	t.Run("not a mapping", func(t *testing.T) {
		t.Parallel()
		issues, err := config.CheckSchema([]byte("- a\n"))
		if err != nil || len(issues) != 1 || issues[0].Key != "(root)" {
			t.Errorf("got %v, %v; want one violation at (root)", issues, err)
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
