package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// referencePages says which docs/reference pages may document the
// fields of each config struct. A field passes when one of its pages has
// a table row whose first cell is the field's YAML name in backticks.
//
// The map is the contract: a struct reachable from Config that isn't
// listed here fails the test too, so a new block of schema can't arrive
// without someone deciding where it's documented.
var referencePages = map[string][]string{
	"Config":         {"app.md"},
	"App":            {"app.md"},
	"Glyphs":         {"app.md"},
	"Borders":        {"app.md"},
	"Prompt":         {"app.md"},
	"EnvSpec":        {"app.md"},
	"OutputConfig":   {"app.md"},
	"DataBlock":      {"app.md"},
	"TUIBlock":       {"app.md"},
	"Screen":         {"app.md"},
	"Tab":            {"app.md"},
	"Node":           {"app.md"},
	"Item":           {"app.md"},
	"ZStack":         {"app.md"},
	"Component":      {"components.md"},
	"Column":         {"components.md"},
	"Distinct":       {"components.md"},
	"Metric":         {"components.md"},
	"Sort":           {"components.md"},
	"ColorRule":      {"components.md"},
	"TreeNode":       {"components.md"},
	"InspectorField": {"components.md"},
	"Colors":         {"components.md"},
	"OnCursor":       {"components.md"},
	"OnInput":        {"components.md"},
	"Activity":       {"components.md"},
	"Source":         {"sources.md", "pipelines.md"},
	"Parameter":      {"sources.md", "actions.md", "app.md"},
	"PaginateConfig": {"sources.md"},
	"AuthSpec":       {"sources.md"},
	"BasicAuth":      {"sources.md"},
	"APIKeyAuth":     {"sources.md"},
	"OAuth2Auth":     {"sources.md"},
	"WindowConfig":   {"sources.md"},
	"FindConfig":     {"sources.md"},
	"ResumeSpec":     {"sources.md"},
	"CacheSpec":      {"sources.md"},
	"Growing":        {"sources.md"},
	"MergeChild":     {"sources.md"},
	"JoinDriver":     {"pipelines.md"},
	"JoinLookup":     {"pipelines.md"},
	"Action":         {"actions.md"},
	"ActionBinding":  {"actions.md"},
	"Claim":          {"actions.md"},
	"Confirm":        {"actions.md"},
}

// Fields documented as sub-keys of their parent's row rather than with a
// row of their own, because the parent is a two-or-three-key map whose
// keys read better inline. Keep this short.
var documentedInline = map[string]string{
	"Sort.column":       "initial_sort row",
	"Sort.desc":         "initial_sort row",
	"TreeNode.label":    "tree root row",
	"TreeNode.children": "tree root row",
	"JoinDriver.from":   "join driver row",
	"JoinLookup.from":   "join lookups row",
	"JoinLookup.on":     "join lookups row",
}

// TestReferenceDocumentsEveryField fails when a YAML field has no row in
// docs/reference. The docs are hand-written; this keeps them from
// drifting as the schema grows.
func TestReferenceDocumentsEveryField(t *testing.T) {
	rows := map[string]map[string]bool{} // page -> documented names
	dir := filepath.Join("..", "..", "docs", "reference")
	rowRe := regexp.MustCompile("^\\|\\s*`([a-z0-9_]+)`\\s*\\|")
	for _, pages := range referencePages {
		for _, page := range pages {
			if rows[page] != nil {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, page))
			if err != nil {
				t.Fatalf("read %s: %v", page, err)
			}
			names := map[string]bool{}
			for _, line := range strings.Split(string(b), "\n") {
				if m := rowRe.FindStringSubmatch(line); m != nil {
					names[m[1]] = true
				}
			}
			rows[page] = names
		}
	}

	var missing []string
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(typ reflect.Type) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || typ.PkgPath() != reflect.TypeOf(Config{}).PkgPath() || seen[typ] {
			return
		}
		seen[typ] = true
		pages, ok := referencePages[typ.Name()]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s: struct has no entry in referencePages", typ.Name()))
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("yaml")
			name := strings.Split(tag, ",")[0]
			if name == "-" {
				continue
			}
			if strings.Contains(tag, ",inline") {
				walk(f.Type)
				continue
			}
			walk(f.Type)
			if name == "" || !ok {
				continue
			}
			if _, inline := documentedInline[typ.Name()+"."+name]; inline {
				continue
			}
			found := false
			for _, p := range pages {
				if rows[p][name] {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, fmt.Sprintf("%s.%s (`%s`): no row in %s", typ.Name(), f.Name, name, strings.Join(pages, " or ")))
			}
		}
	}
	walk(reflect.TypeOf(Config{}))

	sort.Strings(missing)
	for _, m := range missing {
		t.Error(m)
	}
}

// enumPages says which docs/reference page must name each value of an
// enum the code keeps a registry for. Only registries are checked: an
// enum that lives in a switch has no list to compare against.
var enumPages = []struct {
	name   string
	page   string
	values func() []string
}{
	{"component kinds", "components.md", func() []string { return ComponentKinds }},
	{"leaf source kinds", "sources.md", func() []string { return keys(leafTypes) }},
	{"operator kinds", "pipelines.md", func() []string { return keys(operatorTypes) }},
	{"border shapes", "app.md", func() []string { return BorderShapeNames }},
	{"slot brackets", "app.md", func() []string { return SlotBracketNames }},
	{"keymap bindings", "app.md", func() []string { return KeymapNames }},
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestReferenceDocumentsEveryEnumValue fails when a value the validator
// accepts never appears, in backticks, on the page that documents it.
func TestReferenceDocumentsEveryEnumValue(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "reference")
	for _, e := range enumPages {
		b, err := os.ReadFile(filepath.Join(dir, e.page))
		if err != nil {
			t.Fatalf("read %s: %v", e.page, err)
		}
		for _, v := range e.values() {
			if !strings.Contains(string(b), "`"+v+"`") {
				t.Errorf("%s: `%s` is not named on %s", e.name, v, e.page)
			}
		}
	}
}
