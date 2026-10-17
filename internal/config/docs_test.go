package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
)

// TestEveryKeyIsDocumented keeps docs/operator/configuration.md complete: every YAML key
// of config.Config appears in a code span of the page, by its full dotted path or by its
// path below the nearest map or list (endpoints.<name>, applications[]), map keys written
// as <placeholder>.
func TestEveryKeyIsDocumented(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "operator", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	spans := regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(string(data), -1)
	documented := func(full, relative []string) bool {
		for _, path := range [][]string{full, relative} {
			if len(path) == 0 {
				continue
			}
			parts := make([]string, len(path))
			for i, p := range path {
				if p == "<>" {
					parts[i] = `<[a-z_]+>`
				} else {
					parts[i] = regexp.QuoteMeta(p)
				}
			}
			re := regexp.MustCompile(`(^|[^a-z_])` + strings.Join(parts, `\.`) + `($|[^a-z_])`)
			for _, s := range spans {
				if re.MatchString(s[1]) {
					return true
				}
			}
		}
		return false
	}
	var keys int
	var walk func(t reflect.Type, full, relative []string)
	walk = func(typ reflect.Type, full, relative []string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		switch {
		case typ == reflect.TypeFor[time.Duration]():
		case typ.Kind() == reflect.Map:
			walk(typ.Elem(), append(full, "<>"), nil)
			return
		case typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Struct:
			walk(typ.Elem(), full, nil)
			return
		case typ.Kind() == reflect.Struct:
			for f := range typ.Fields() {
				name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
				if name == "" || name == "-" {
					continue
				}
				walk(f.Type, append(full[:len(full):len(full)], name), append(relative[:len(relative):len(relative)], name))
			}
			return
		}
		keys++
		if !documented(full, relative) {
			t.Errorf("configuration.md does not document %s", strings.Join(full, "."))
		}
	}
	walk(reflect.TypeFor[config.Config](), nil, nil)
	if keys < 80 {
		t.Fatalf("walked %d keys; is the walk broken?", keys)
	}
}
