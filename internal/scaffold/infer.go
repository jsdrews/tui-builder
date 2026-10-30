package scaffold

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// shape is where a sample's rows are: a list of objects (under root,
// when the output wraps them), or one object with no list in it.
type shape struct {
	root   string
	rows   []*object
	single *object
}

// findRows locates the rows in the decoded sample. rootFlag, when set,
// is --root and wins; a sample it doesn't fit is an error naming both.
func findRows(values []any, rootFlag string) (shape, error) {
	if len(values) > 1 {
		if rootFlag != "" {
			return shape{}, fmt.Errorf("--root %s: the output is one JSON value per line, so each line is already a row", rootFlag)
		}
		rows, err := objects(values, "the lines")
		return shape{rows: rows}, err
	}
	doc := values[0]
	if rootFlag != "" {
		o, _ := doc.(*object)
		v, ok := lookup(o, rootFlag)
		list, isList := v.([]any)
		if !ok || !isList {
			return shape{}, fmt.Errorf("--root %s: the output has no list at %s%s", rootFlag, rootFlag, listFields(o))
		}
		rows, err := objects(list, rootFlag)
		return shape{root: rootFlag, rows: rows}, err
	}
	switch d := doc.(type) {
	case []any:
		rows, err := objects(d, "the list")
		return shape{rows: rows}, err
	case *object:
		var lists, empty []string
		for _, k := range d.keys {
			l, ok := d.vals[k].([]any)
			switch {
			case !ok:
			case len(l) == 0:
				empty = append(empty, k)
			case allObjects(l):
				lists = append(lists, k)
			}
		}
		switch {
		case len(lists) == 1:
			rows, _ := objects(d.vals[lists[0]].([]any), lists[0])
			return shape{root: lists[0], rows: rows}, nil
		case len(lists) > 1:
			return shape{}, fmt.Errorf("the output has several lists of objects (%s): pick the rows with --root", strings.Join(lists, ", "))
		case len(empty) > 0:
			return shape{}, fmt.Errorf("%s is empty, so there are no rows to take columns from: run it where it returns some, or pass --no-run", strings.Join(empty, ", "))
		}
		return shape{single: d}, nil
	}
	return shape{}, fmt.Errorf("the output is a single %s, not an object or a list of them", jsonKind(doc))
}

func objects(list []any, where string) ([]*object, error) {
	if len(list) == 0 {
		return nil, fmt.Errorf("%s is empty, so there are no rows to take columns from: run it where it returns some, or pass --no-run", where)
	}
	rows := make([]*object, 0, len(list))
	for _, v := range list {
		o, ok := v.(*object)
		if !ok {
			return nil, fmt.Errorf("%s holds a %s, not objects: add exec makes tables of objects", where, jsonKind(v))
		}
		rows = append(rows, o)
	}
	return rows, nil
}

func allObjects(l []any) bool {
	for _, v := range l {
		if _, ok := v.(*object); !ok {
			return false
		}
	}
	return true
}

// listFields names an object's list fields, for an error about --root.
func listFields(o *object) string {
	if o == nil {
		return ""
	}
	var lists []string
	for _, k := range o.keys {
		if _, ok := o.vals[k].([]any); ok {
			lists = append(lists, k)
		}
	}
	if len(lists) == 0 {
		return ""
	}
	return " (its lists: " + strings.Join(lists, ", ") + ")"
}

// lookup resolves a dot-path through nested objects.
func lookup(o *object, path string) (any, bool) {
	var v any = o
	for _, seg := range strings.Split(path, ".") {
		cur, ok := v.(*object)
		if !ok || cur == nil {
			return nil, false
		}
		if v, ok = cur.vals[seg]; !ok {
			return nil, false
		}
	}
	return v, true
}

func jsonKind(v any) string {
	switch v.(type) {
	case *object:
		return "object"
	case []any:
		return "list"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	}
	return "null"
}

// field is one column candidate: a scalar (or list of scalars) at a
// dot-path, with what the sample says about it.
type field struct {
	path  string
	title string
	sort  string // "", "number" or "si"
	// set counts the rows where it has a value (not null, not an
	// empty list).
	set int
	// values are the rendered values, one per row it's set in.
	values []string
	list   bool // a list of scalars, shown comma-joined
	// objects marks a list of objects somewhere at this path: skipped.
	objects bool
	// kinds seen among the set values.
	numbers, strs, bools, others int
}

// fields collects the columns from the union of the rows, in the order
// they're first seen. Nested objects flatten two levels deep.
func fields(rows []*object) []*field {
	var order []*field
	byPath := map[string]*field{}
	get := func(path string) *field {
		f := byPath[path]
		if f == nil {
			f = &field{path: path}
			byPath[path] = f
			order = append(order, f)
		}
		return f
	}
	var walk func(prefix string, o *object, depth int)
	walk = func(prefix string, o *object, depth int) {
		for _, k := range o.keys {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			switch v := o.vals[k].(type) {
			case *object:
				if depth == 0 {
					walk(path, v, 1)
				}
			case []any:
				f := get(path)
				if !allScalars(v) {
					f.objects = true
					continue
				}
				f.list = true
				if len(v) > 0 {
					f.set++
					f.others++
					f.values = append(f.values, joinScalars(v))
				}
			case nil:
				get(path)
			case json.Number:
				f := get(path)
				f.set++
				f.numbers++
				f.values = append(f.values, v.String())
			case string:
				f := get(path)
				f.set++
				f.strs++
				f.values = append(f.values, v)
			case bool:
				f := get(path)
				f.set++
				f.bools++
				f.values = append(f.values, fmt.Sprint(v))
			}
		}
	}
	for _, r := range rows {
		walk("", r, 0)
	}
	var kept []*field
	for _, f := range order {
		if f.objects || f.set == 0 {
			continue
		}
		f.sort = sortFor(f)
		kept = append(kept, f)
	}
	titleFields(kept)
	return kept
}

func allScalars(l []any) bool {
	for _, v := range l {
		switch v.(type) {
		case *object, []any:
			return false
		}
	}
	return true
}

func joinScalars(l []any) string {
	parts := make([]string, len(l))
	for i, v := range l {
		if v != nil {
			parts[i] = fmt.Sprint(v)
		}
	}
	return strings.Join(parts, ", ")
}

var (
	numberText = regexp.MustCompile(`^-?[0-9][0-9,]*(\.[0-9]+)?$`)
	// siText is what the table's si sort parses: a number with K, M, G
	// or T (binary with i, bytes with B) or a bare B.
	siText   = regexp.MustCompile(`^-?[0-9]*\.?[0-9]+\s?([kKmMgGtT]i?[bB]?|[bB])?$`)
	siSuffix = regexp.MustCompile(`[a-zA-Z]$`)
)

// sortFor picks a column's comparator: number for numbers (or strings
// that are all numbers), si for strings like 12Ki or 3.5M in every row.
func sortFor(f *field) string {
	if f.list || f.others > 0 || f.bools > 0 {
		return ""
	}
	if f.strs == 0 {
		return "number"
	}
	allNumbers, allSI, suffixed := true, true, false
	for _, v := range f.values {
		allNumbers = allNumbers && numberText.MatchString(v)
		allSI = allSI && siText.MatchString(v)
		suffixed = suffixed || siSuffix.MatchString(v)
	}
	switch {
	case allNumbers:
		return "number"
	case allSI && suffixed:
		return "si"
	}
	return ""
}

// titleFields titles each field from its last path segment in words,
// prefixing the parent's when two would clash ("Spec name").
func titleFields(fs []*field) {
	count := map[string]int{}
	for _, f := range fs {
		f.title = words(lastSegment(f.path))
		count[f.title]++
	}
	for _, f := range fs {
		if count[f.title] > 1 && strings.Contains(f.path, ".") {
			segs := strings.Split(f.path, ".")
			f.title = words(segs[len(segs)-2]) + " " + strings.ToLower(f.title)
		}
	}
}

func lastSegment(path string) string { return path[strings.LastIndex(path, ".")+1:] }

// words turns a field name into a title: creationTimestamp, owner_id
// and restart-count become "Creation timestamp", "Owner id",
// "Restart count".
func words(name string) string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.' || r == '@':
			flush()
			continue
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) ||
			(i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1]))):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	if len(out) == 0 {
		return name
	}
	s := strings.Join(out, " ")
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// markKeyCandidates are the fields a row's identity is looked for in,
// in order.
var markKeyCandidates = []string{"id", "uid", "metadata.uid", "name", "metadata.name"}

// markKey is the path of the first candidate (any case: Docker prints
// ID) set in every row and unique across them; "" when none is.
func markKey(fs []*field, rows int) string {
	for _, c := range markKeyCandidates {
		i := slices.IndexFunc(fs, func(f *field) bool { return strings.EqualFold(f.path, c) })
		if i < 0 || fs[i].list || fs[i].set != rows {
			continue
		}
		seen := map[string]bool{}
		unique := true
		for _, v := range fs[i].values {
			unique = unique && !seen[v]
			seen[v] = true
		}
		if unique {
			return fs[i].path
		}
	}
	return ""
}

// everyRowKey is the path of the first mark-key candidate set in every
// row, unique or not; "" when none is.
func everyRowKey(fs []*field, rows int) string {
	for _, c := range markKeyCandidates {
		i := slices.IndexFunc(fs, func(f *field) bool { return strings.EqualFold(f.path, c) })
		if i >= 0 && !fs[i].list && fs[i].set == rows {
			return fs[i].path
		}
	}
	return ""
}

// verbs are command words that say what to do rather than what's
// listed, skipped when naming a source from its command.
var verbs = map[string]bool{
	"get": true, "list": true, "ls": true, "show": true, "describe": true,
	"view": true, "status": true, "info": true, "query": true, "search": true,
	"api": true, "exec": true, "print": true, "dump": true,
}

// sourceName names a source from its command: the last of the leading
// words (before the first flag) that isn't a verb, else the program.
// `kubectl get pods` is pods, `gh pr list` is pr, `docker ps` is ps,
// `cat data/users.json` is users. Numbers (a page size) don't name.
func sourceName(argv []string) string {
	name := filepath.Base(argv[0])
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			break
		}
		if !verbs[strings.ToLower(a)] && !numberText.MatchString(a) {
			name = a
		}
	}
	// A file argument (`cat data/users.json`) names by its base name.
	if strings.ContainsAny(name, `/\`) || filepath.Ext(name) != "" {
		name = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	}
	return identifier(name)
}

// identifier makes s a plain snake_case name.
func identifier(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			under = false
			b.WriteRune(r)
		} else {
			under = true
		}
	}
	if b.Len() == 0 {
		return "items"
	}
	out := b.String()
	if unicode.IsDigit(rune(out[0])) {
		out = "x" + out
	}
	return out
}
