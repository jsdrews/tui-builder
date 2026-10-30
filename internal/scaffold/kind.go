package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

//go:embed kinds
var kindFiles embed.FS

// docsURL is where the reference pages are published.
const docsURL = "https://jsdrews.github.io/tui-builder/reference/"

// Kinds are the kinds `init <kind>` takes: components, then sources.
func Kinds() []string { return append(slices.Clone(ComponentKinds), SourceKinds...) }

// Example is what `tui-builder init <kind>` writes: the config, and
// any files it reads, by name, to write beside it.
type Example struct {
	Config []byte
	Files  map[string][]byte
}

// sideFiles are the files an example reads, by kind.
var sideFiles = map[string][]string{"file": {"services.json"}}

// Kind is what `tui-builder init <kind>` writes, titled title: a small
// config built around kind that runs, with every field kind takes below
// it, commented out. dir is the directory the config goes in, as the
// command line gave it, so a path in it finds the files beside it.
func Kind(kind, title, dir string) (Example, error) {
	b, err := kindWith(kind, title, nil)
	if err != nil {
		return Example{}, err
	}
	ex := Example{Config: withDir(b, dir)}
	for _, name := range sideFiles[kind] {
		f, err := kindFiles.ReadFile("kinds/" + name)
		if err != nil {
			return Example{}, err
		}
		if ex.Files == nil {
			ex.Files = map[string][]byte{}
		}
		ex.Files[name] = f
	}
	return ex, nil
}

// withDir fills in __DIR__: dir with a slash, or nothing for ".".
func withDir(b []byte, dir string) []byte {
	d := ""
	if dir != "" && dir != "." {
		d = strings.TrimSuffix(filepath.ToSlash(dir), "/") + "/"
	}
	return bytes.ReplaceAll(b, []byte("__DIR__"), []byte(d))
}

// entry is one commented-out field: a key and value, or a block of
// entries under the key.
type entry struct {
	key, value, desc string
	required         bool
	list             bool // a block that's a list: its first entry gets "- "
	block            []*entry
	// instead are keys of the running config this field replaces.
	instead []string
	// path names the entry for tests: "columns[].metric.bar".
	path string
}

// kindWith is Kind with the entries on reports true for left
// uncommented, for the tests that load each field on its own. A live
// entry that replaces keys of the running config drops them.
func kindWith(kind, title string, on func(*entry) bool) ([]byte, error) {
	body, err := kindFiles.ReadFile("kinds/" + kind + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("no kind %q", kind)
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	markers := map[int][]*entry{}
	drop := map[int]bool{}
	for i, line := range lines {
		ind, rest := splitIndent(line)
		if !strings.HasPrefix(rest, "# @fields") {
			continue
		}
		es, err := markerEntries(kind, strings.TrimSpace(strings.TrimPrefix(rest, "# @fields")), siblings(lines, i, len(ind)))
		if err != nil {
			return nil, err
		}
		markers[i] = es
		if on == nil {
			continue
		}
		for _, e := range es {
			if on(e) {
				for _, j := range keyLines(lines, i, len(ind), e.instead) {
					drop[j] = true
				}
			}
		}
	}
	var out []string
	out = append(out, header(kind)...)
	for i, line := range lines {
		line = strings.Replace(line, "__TITLE__", yamlScalar(title), 1)
		switch {
		case drop[i]:
		case markers[i] != nil:
			ind, _ := splitIndent(line)
			out = append(out, render(markers[i], ind, on)...)
		default:
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, "\n") + "\n"), nil
}

// markerRoots are the entries every marker in kind's example writes,
// for the tests.
func markerRoots(kind string) ([]*entry, error) {
	body, err := kindFiles.ReadFile("kinds/" + kind + ".yaml")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(body), "\n")
	var out []*entry
	for i, line := range lines {
		ind, rest := splitIndent(line)
		if strings.HasPrefix(rest, "# @fields") {
			es, err := markerEntries(kind, strings.TrimSpace(strings.TrimPrefix(rest, "# @fields")), siblings(lines, i, len(ind)))
			if err != nil {
				return nil, err
			}
			out = append(out, es...)
		}
	}
	return out, nil
}

// keyLines are the lines of the keys set beside a marker, with what's
// nested under them.
func keyLines(lines []string, at, indent int, keys []string) []int {
	if len(keys) == 0 {
		return nil
	}
	start, end := blockAround(lines, at, indent)
	var out []int
	for i := start; i <= end; i++ {
		ind, rest := splitIndent(lines[i])
		k, _, _ := strings.Cut(rest, ":")
		if len(ind) != indent || !slices.Contains(keys, k) {
			continue
		}
		out = append(out, i)
		for j := i + 1; j <= end; j++ {
			if jind, jrest := splitIndent(lines[j]); jrest != "" && len(jind) <= indent {
				break
			}
			out = append(out, j)
		}
	}
	return out
}

// header is the comment at the top of every init <kind> config.
func header(kind string) []string {
	page := "components"
	if slices.Contains(SourceKinds, kind) {
		page = "sources"
	}
	return []string{
		fmt.Sprintf("# An example %s from `tui-builder init %s`. Run it with", kind, kind),
		"#   tui-builder <this file>",
		fmt.Sprintf("# Every field a %s takes is below, commented out: uncomment a line to", kind),
		"# use it. A line shows the field's default where it has one; a block",
		"# shows every field it takes, so keep the ones you need.",
		"# " + docsURL + page + "/#" + kind,
		"",
	}
}

// markerEntries are what a `# @fields` marker writes: with no block, the
// kind's own fields not already set (have); with a list block, a
// commented item of it; inside a map block (auth:), its fields not
// already set.
func markerEntries(kind, block string, have []string) ([]*entry, error) {
	if block == "" {
		secs, err := fieldsFor(kind)
		if err != nil {
			return nil, err
		}
		var out []*entry
		for i, fs := range secs {
			key := kindSections[kind][i]
			var group []*entry
			for _, f := range fs {
				if slices.Contains(have, f.Name) {
					continue
				}
				e := newEntry(kind, key, f, 0, "")
				e.instead = slices.DeleteFunc(e.instead, func(k string) bool { return !slices.Contains(have, k) })
				group = append(group, e)
			}
			if len(group) == 0 {
				continue
			}
			page, anchor, _ := strings.Cut(key, "#")
			title := "Common fields"
			if i > 0 {
				title = strings.ToUpper(kind[:1]) + kind[1:] + " fields"
			}
			out = append(out, &entry{desc: fmt.Sprintf("%s: %s%s/#%s", title, docsURL, strings.TrimSuffix(page, ".md"), anchor)})
			out = append(out, group...)
		}
		return out, nil
	}
	sec, ok := blockSection(kind, block)
	if !ok {
		return nil, fmt.Errorf("kinds/%s.yaml: no block %q", kind, block)
	}
	page, anchor, _ := strings.Cut(sec, "#")
	if !listBlocks[block] {
		var out []*entry
		for _, e := range blockEntries(kind, sec, 1, block) {
			if slices.Contains(have, e.key) {
				continue
			}
			e.instead = slices.DeleteFunc(e.instead, func(k string) bool { return !slices.Contains(have, k) })
			out = append(out, e)
		}
		heading := &entry{desc: fmt.Sprintf("Every %s field: %s%s/#%s", block, docsURL, strings.TrimSuffix(page, ".md"), anchor)}
		return append([]*entry{heading}, out...), nil
	}
	item := &entry{list: true, path: block + "[]"}
	item.block = blockEntries(kind, sec, 1, item.path)
	requiredFirst(item.block)
	return []*entry{
		{desc: fmt.Sprintf("Every %s field: %s%s/#%s", itemNames[block], docsURL, strings.TrimSuffix(page, ".md"), anchor)},
		item,
	}, nil
}

// itemNames are what a list block's items are called.
var itemNames = map[string]string{"columns": "column", "fields": "inspector field", "items": "menu item"}

// newEntry is a field's entry, its block filled in when it's one.
// depth bounds the nesting (inspector fields hold fields); parent is
// the enclosing entry's path.
func newEntry(kind, sec string, f Field, depth int, parent string) *entry {
	path := f.Name
	if parent != "" {
		path = parent + "." + f.Name
	}
	e := &entry{
		path:     path,
		key:      f.Name,
		desc:     describe(sec, f),
		required: isRequired(f.ValidOn),
		instead:  insteadOf[sec+"."+f.Name],
	}
	if b, ok := blockSection(kind, f.Name); ok && depth < 2 && b != sec {
		e.list = listBlocks[f.Name]
		p := path
		if e.list {
			p += "[]"
		}
		if name, ok := mapBlocks[f.Name]; ok {
			// A map of named entries: show one, named name.
			one := &entry{key: name, path: p + "." + name, desc: "One entry, named " + name + "."}
			one.block = blockEntries(kind, b, depth+1, one.path)
			e.block = []*entry{one}
			return e
		}
		e.block = blockEntries(kind, b, depth+1, p)
		if e.list {
			requiredFirst(e.block)
		}
		return e
	}
	e.value = value(sec, f)
	return e
}

// isRequired is whether a "Valid on/with" cell says the field is
// required, in any of its clauses: "required", "`basic`; required".
func isRequired(cell string) bool {
	for _, c := range strings.FieldsFunc(cell, func(r rune) bool { return r == ';' || r == ',' }) {
		if strings.HasPrefix(strings.TrimSpace(c), "required") {
			return true
		}
	}
	return false
}

// mapBlocks are the blocks that map names to entries, and the name the
// example entry gets.
var mapBlocks = map[string]string{"parameters": "q"}

// leadKey is a "Valid on/with" cell's leading `key`.
var leadKey = regexp.MustCompile("^`([a-z0-9_]+)`")

// blockEntries are a block's entries. A row whose "Valid on/with"
// starts with another row's key, when that row is a map, is one of its
// keys: auth's `user` (`basic`; required) sits under `basic:`.
func blockEntries(kind, sec string, depth int, parent string) []*entry {
	fs := blockFields(kind, sec)
	maps := map[string]bool{}
	for _, f := range reference[sec] {
		if strings.HasPrefix(f.Type, "map") {
			maps[f.Name] = true
		}
	}
	var out []*entry
	byName := map[string]*entry{}
	for _, f := range fs {
		if m := leadKey.FindStringSubmatch(f.ValidOn); m != nil && maps[m[1]] {
			p := byName[m[1]]
			if p == nil {
				continue // its block isn't valid on kind: websocket has no oauth2
			}
			p.value = ""
			p.block = append(p.block, newEntry(kind, sec, f, depth+1, p.path))
			continue
		}
		e := newEntry(kind, sec, f, depth, parent)
		byName[f.Name] = e
		out = append(out, e)
	}
	return out
}

// requiredFirst moves a list item's required keys to the front, so the
// "- " is on a key that's always set.
func requiredFirst(es []*entry) {
	slices.SortStableFunc(es, func(a, b *entry) int {
		switch {
		case a.required == b.required:
			return 0
		case a.required:
			return -1
		}
		return 1
	})
}

// blockSection is the section holding a block's fields on kind.
func blockSection(kind, key string) (string, bool) {
	if kind == "menu" && key == "items" {
		return "components.md#menu", true
	}
	blocks := componentBlocks
	if slices.Contains(SourceKinds, kind) {
		blocks = sourceBlocks
	}
	s, ok := blocks[key]
	return s, ok
}

// blockFields are a block's fields, kept to those valid on kind when the
// block is shared.
func blockFields(kind, sec string) []Field {
	if !filtered[sec] && !lenient[sec] {
		return reference[sec]
	}
	all := ComponentKinds
	if slices.Contains(SourceKinds, kind) {
		all = SourceKinds
	}
	var out []Field
	for _, f := range reference[sec] {
		// TestEveryKindCellReads covers the error in a strict section.
		kinds, err := kindsOf(f.ValidOn, all)
		if err != nil || kinds == nil || slices.Contains(kinds, kind) {
			out = append(out, f)
		}
	}
	return out
}

// listBlocks are the blocks whose value is a list of maps.
var listBlocks = map[string]bool{"columns": true, "color_rules": true, "fields": true, "items": true, "children": true}

// insteadOf are the fields that replace a key of the running config: a
// list's static items: replace its source: and item:, and so on.
var insteadOf = map[string][]string{
	"components.md#common-fields.source": {"lines", "content"},
	"components.md#list.items":           {"source", "item"},
	"components.md#table.rows":           {"source"},
	"components.md#tree.root":            {"source", "group_by", "label"},
	"components.md#tree.children":        {"group_by"},
	"components.md#inspector.auto":       {"fields"},
	"sources.md#merge.children":          {"sources", "tag_field"},
	"sources.md#auth.basic":              {"bearer"},
	"sources.md#auth.api_key":            {"bearer"},
	"sources.md#auth.oauth2":             {"bearer"},
}

// examples are values for fields whose default or zero value says
// nothing, or wouldn't load once uncommented (a block's required keys).
var examples = map[string]string{
	"components.md#columns.title":          "Title",
	"components.md#columns.value":          "name",
	"components.md#inspector-fields.label": "Label",
	"components.md#menu.label":             "Label",
	"components.md#menu.screen":            "services",
	"components.md#color-rules.color":      "red",
	"components.md#color-rules.when":       "stopped",
	"components.md#common-fields.mark_key": "name",
	"components.md#activity.status":        "status",
	"components.md#activity.busy":          "[pending]",
	"components.md#on_cursor.source":       "other_table",
	"components.md#on_input.bind":          `{q: "${input.search}"}`,
	"sources.md#parameters.type":           "string",
	"sources.md#cache.ttl":                 "30s",
	"sources.md#resume.timestamp":          `'^(\S+)'`,
	"sources.md#graphql.query":             `"{ things { name } }"`,
	"sources.md#paginate.strategy":         "page",
	"sources.md#paginate.page_param":       "page",
	"sources.md#find.position":             "id",
	"sources.md#window.offset_param":       "offset",
	"sources.md#window.limit_param":        "limit",
	"sources.md#window.total_path":         "total",
	"sources.md#find.params":               `{name: "${window.search}"}`,
	"sources.md#growing.source":            "services",
	"sources.md#growing.while":             `"${item.status} == running"`,
	"sources.md#merge-children.source":     "eu",
	"sources.md#auth.user":                 "me",
	"sources.md#auth.in":                   "header",
	"sources.md#auth.name":                 "X-API-Key",
	"sources.md#auth.token_url":            "https://example.com/oauth/token",
	"sources.md#auth.client_id":            "my-client",
	"sources.md#auth.client_secret":        "${env.CLIENT_SECRET}",
}

var (
	leadingCode = regexp.MustCompile("^`([^`]*)`(.*)$")
	mdLink      = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)

// value is what a commented field line shows: an example, else the
// default when the docs give a literal one, else the type's zero.
func value(sec string, f Field) string {
	if v, ok := examples[sec+"."+f.Name]; ok {
		return v
	}
	typ := mdLink.ReplaceAllString(f.Type, "$1")
	if m := leadingCode.FindStringSubmatch(f.Default); m != nil && (m[2] == "" || strings.HasPrefix(m[2], " (")) {
		if bareType(typ) || strings.HasPrefix(m[1], "[") || strings.HasPrefix(m[1], "{") {
			return m[1]
		}
		return yamlScalar(m[1])
	}
	switch {
	case strings.HasPrefix(typ, "bool"):
		return "false"
	case strings.HasPrefix(typ, "int"), strings.HasPrefix(typ, "number"):
		return "0"
	case strings.HasPrefix(typ, "list"):
		return "[]"
	case strings.HasPrefix(typ, "map"), typ == "node":
		return "{}"
	}
	return `""`
}

// bareType are the types whose defaults are written unquoted.
func bareType(typ string) bool {
	for _, p := range []string{"bool", "int", "number", "duration", "size"} {
		if strings.HasPrefix(typ, p) {
			return true
		}
	}
	return false
}

// describe is a field's comment: the first sentence of the docs' "What
// it does", then where it's valid when that limits it.
func describe(sec string, f Field) string {
	d := firstSentence(mdLink.ReplaceAllString(f.Desc, "$1"))
	if v := f.ValidOn; v != "—" && v != "" && !filtered[sec] {
		d += " (" + mdLink.ReplaceAllString(v, "$1") + ")"
	} else if strings.HasPrefix(v, "required") {
		d += " (" + v + ")"
	}
	return d
}

// firstSentence cuts s after its first full stop that ends a sentence.
func firstSentence(s string) string {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '.' || s[i+1] != ' ' {
			continue
		}
		if before := s[:i]; strings.HasSuffix(before, "e.g") || strings.HasSuffix(before, "i.e") || strings.HasSuffix(before, "etc") {
			continue
		}
		if strings.Count(s[:i], "`")%2 == 1 {
			continue
		}
		return s[:i+1]
	}
	return s
}

// commentCol is where a field line's description starts, and width
// where it wraps.
const (
	commentCol = 40
	width      = 100
)

// render writes entries at indent ind, commented unless on says so.
// The # sits at ind on every line and the YAML's own indent follows it,
// so deleting "# " from a line leaves it where it belongs.
func render(es []*entry, ind string, on func(*entry) bool) []string {
	var out []string
	for _, e := range es {
		out = append(out, renderEntry(e, ind, "", on)...)
	}
	return out
}

// renderEntry writes e at indent ind, its key after pre (the YAML
// indent inside the comment, and "- " on a list item's first key).
func renderEntry(e *entry, ind, pre string, on func(*entry) bool) []string {
	mark := "# "
	if on != nil && on(e) {
		mark = ""
	}
	if e.key == "" && e.block == nil {
		// A group heading.
		return []string{"", ind + "# " + e.desc}
	}
	var out []string
	sub := strings.Repeat(" ", len(pre))
	if e.key != "" {
		text := e.key + ":"
		if e.block == nil {
			text += " " + e.value
		}
		desc := e.desc
		if len(e.instead) > 0 {
			desc += " Instead of " + strings.Join(e.instead, ", ") + "."
		}
		out = commentLines(ind+mark+pre+text, desc, ind)
		sub += "  "
	}
	for i, c := range e.block {
		p := sub
		if e.list || e.key == "" {
			// A list's one item: its first key carries the dash.
			p = sub + "  "
			if i == 0 {
				p = sub + "- "
			}
		}
		out = append(out, renderEntry(c, ind, p, on)...)
	}
	return out
}

// commentLines is a field line with its description beside it, wrapped
// onto comment lines under it when it's long.
func commentLines(line, desc, ind string) []string {
	if desc == "" {
		return []string{line}
	}
	col := max(commentCol, len(line)+2)
	words := strings.Fields(desc)
	var out []string
	cur := line + strings.Repeat(" ", col-len(line)) + "#"
	for _, w := range words {
		if len(cur)+1+len(w) > width && !strings.HasSuffix(cur, "#") {
			out = append(out, cur)
			pad := ind + "#"
			cur = pad + strings.Repeat(" ", max(1, col-len(pad))) + "#"
		}
		cur += " " + w
	}
	return append(out, cur)
}

// siblings are the keys set beside a marker: those at its indent in the
// mapping it sits in.
func siblings(lines []string, at, indent int) []string {
	var keys []string
	start, end := blockAround(lines, at, indent)
	for i := start; i <= end; i++ {
		ind, rest := splitIndent(lines[i])
		if len(ind) != indent || strings.HasPrefix(rest, "#") || strings.HasPrefix(rest, "- ") {
			continue
		}
		if k, _, ok := strings.Cut(rest, ":"); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// blockAround is the first and last line of the block a line at indent sits
// in.
func blockAround(lines []string, at, indent int) (int, int) {
	inBlock := func(i int) bool {
		ind, rest := splitIndent(lines[i])
		return rest == "" || len(ind) >= indent
	}
	start, end := at, at
	for start > 0 && inBlock(start-1) {
		start--
	}
	for end+1 < len(lines) && inBlock(end+1) {
		end++
	}
	return start, end
}

func splitIndent(line string) (string, string) {
	rest := strings.TrimLeft(line, " ")
	return line[:len(line)-len(rest)], rest
}
