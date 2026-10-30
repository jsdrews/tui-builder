package scaffold

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jsdrews/tui-builder/internal/config"
)

// Field is one row of a docs/reference field table, its cells as the
// docs write them (markdown and all).
type Field struct {
	Name, Type, Default, ValidOn, Desc string
}

// ComponentKinds and SourceKinds are what `init <kind>` takes.
var (
	ComponentKinds = config.ComponentKinds
	SourceKinds    = []string{"http", "exec", "file", "websocket", "static", "merge"}
)

// kindSections are the reference sections whose fields belong on a
// kind, in the order init writes them. A shared section's fields are
// kept only where their "Valid on/with" cell allows (see kindsOf).
var kindSections = func() map[string][]string {
	m := map[string][]string{}
	for _, k := range ComponentKinds {
		m[k] = []string{"components.md#common-fields", "components.md#" + k}
	}
	// The menu section is its items' fields.
	m["menu"] = m["menu"][:1]
	for _, k := range SourceKinds {
		m[k] = []string{"sources.md#fields-shared-by-leaf-sources", "sources.md#" + k}
	}
	return m
}()

// componentBlocks and sourceBlocks are the sections holding a nested
// block's fields, by the block's key.
var componentBlocks = map[string]string{
	"columns":     "components.md#columns",
	"metric":      "components.md#metrics",
	"fields":      "components.md#inspector-fields",
	"color_rules": "components.md#color-rules",
	"colors":      "components.md#colors",
	"on_cursor":   "components.md#on_cursor",
	"on_input":    "components.md#on_input",
	"activity":    "components.md#activity",
}

var sourceBlocks = map[string]string{
	"graphql":    "sources.md#graphql",
	"auth":       "sources.md#auth",
	"retry":      "sources.md#retry",
	"children":   "sources.md#merge-children",
	"parameters": "sources.md#parameters",
	"paginate":   "sources.md#paginate",
	"window":     "sources.md#window",
	"find":       "sources.md#find",
	"growing":    "sources.md#growing",
	"resume":     "sources.md#resume",
	"cache":      "sources.md#cache",
}

// filtered are the sections shared by several kinds, whose "Valid
// on/with" cell says which.
var filtered = map[string]bool{
	"components.md#common-fields":              true,
	"components.md#colors":                     true,
	"sources.md#fields-shared-by-leaf-sources": true,
}

// anyKind are the cells that put no limit on the kind.
var anyKind = map[string]bool{"—": true, "": true, "all": true, "any kind": true, "any leaf kind": true}

var word = regexp.MustCompile("[a-z]+")

// lenient are blocks shared by several source kinds whose "Valid
// on/with" cells mostly say something else (`needs filter_ops`), so a
// cell that names no kind keeps the field.
var lenient = map[string]bool{
	"sources.md#window": true,
	"sources.md#auth":   true,
}

// kindsOf reads a shared field's "Valid on/with" cell: the kinds out of
// all it names, or nil when it allows every kind. Each ;-separated
// clause adds the kinds it names, or takes them away when it starts
// "not": "required on http; not on exec" is http. A cell naming no kind
// is read as a limit on other fields ("not with `follow`") when it
// mentions one in backticks, or as "has a source" when it says
// source-bound. Any other cell is an error, so a field is never dropped
// because its cell was misread.
func kindsOf(cell string, all []string) ([]string, error) {
	var add, drop []string
	for _, clause := range strings.Split(cell, ";") {
		clause = strings.TrimSpace(clause)
		for _, w := range word.FindAllString(strings.ReplaceAll(clause, "`", ""), -1) {
			if !slices.Contains(all, w) {
				continue
			}
			if strings.HasPrefix(clause, "not ") {
				drop = append(drop, w)
			} else {
				add = append(add, w)
			}
		}
	}
	keep := func(k string) bool {
		return (len(add) == 0 || slices.Contains(add, k)) && !slices.Contains(drop, k)
	}
	switch {
	case len(add) > 0 || len(drop) > 0:
		return slices.DeleteFunc(slices.Clone(all), func(k string) bool { return !keep(k) }), nil
	case anyKind[cell], strings.HasPrefix(cell, "required"):
		return nil, nil
	case strings.HasPrefix(cell, "source-bound"):
		return slices.DeleteFunc(slices.Clone(all), func(k string) bool {
			return slices.Contains(config.InputKinds, k) || k == "menu"
		}), nil
	case strings.Contains(cell, "`"):
		return nil, nil
	}
	return nil, fmt.Errorf("can't tell which kinds %q means", cell)
}

// fieldsFor are the fields init writes for a kind, section by section.
func fieldsFor(kind string) ([][]Field, error) {
	all := ComponentKinds
	if slices.Contains(SourceKinds, kind) {
		all = SourceKinds
	}
	secs, ok := kindSections[kind]
	if !ok {
		return nil, fmt.Errorf("no kind %q", kind)
	}
	var out [][]Field
	for _, key := range secs {
		var keep []Field
		for _, f := range reference[key] {
			if filtered[key] {
				kinds, err := kindsOf(f.ValidOn, all)
				if err != nil {
					return nil, fmt.Errorf("%s `%s`: %w", key, f.Name, err)
				}
				if kinds != nil && !slices.Contains(kinds, kind) {
					continue
				}
			}
			keep = append(keep, f)
		}
		out = append(out, keep)
	}
	return out, nil
}
