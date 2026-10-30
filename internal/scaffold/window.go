package scaffold

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/columns"
)

// WindowMark is one --window: an argv value that's a window token.
type WindowMark struct {
	// Token is offset, limit, search, sort, sort_dir, cursor, dir or
	// filters.NAME.
	Token string
	Value string
}

var windowTokens = []string{"offset", "limit", "search", "sort", "sort_dir", "cursor", "dir"}

var filterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseWindow reads TOKEN=VALUE, or filter.NAME=VALUE.
func ParseWindow(s string) (WindowMark, error) {
	tok, value, ok := strings.Cut(s, "=")
	if !ok || value == "" {
		return WindowMark{}, fmt.Errorf("--window %s: want TOKEN=VALUE, e.g. offset=0", s)
	}
	if name, ok := strings.CutPrefix(tok, "filter."); ok {
		if !filterName.MatchString(name) {
			return WindowMark{}, fmt.Errorf("--window %s: %q isn't a filter name (letters, digits and _)", s, name)
		}
		return WindowMark{Token: "filters." + name, Value: value}, nil
	}
	if !slices.Contains(windowTokens, tok) {
		return WindowMark{}, fmt.Errorf("--window %s: %q isn't a window token (%s, or filter.NAME)", s, tok, strings.Join(windowTokens, ", "))
	}
	return WindowMark{Token: tok, Value: value}, nil
}

// Window options on ExecOptions.
type WindowOptions struct {
	Marks []WindowMark
	// TotalPath is --total-path; CursorPath is --cursor-path.
	TotalPath, CursorPath string
	// Growing is --growing; FollowEvery is --follow-every.
	Growing     bool
	FollowEvery time.Duration
}

var windowToken = regexp.MustCompile(`\$\{window\.([A-Za-z_.]+)\}`)

// tokensIn lists the window tokens argv references.
func tokensIn(argv []string) map[string]bool {
	out := map[string]bool{}
	for _, a := range argv {
		for _, m := range windowToken.FindAllStringSubmatch(a, -1) {
			out[m[1]] = true
		}
	}
	return out
}

// sampleDefaults are what window tokens written into the command are
// sampled with: the first page, unfiltered, unsorted, from the newest.
var sampleDefaults = map[string]string{"offset": "0", "limit": "50", "dir": "older"}

// SampleArgv is the command to sample: argv as given, except window
// tokens written straight into it, which take sampleDefaults. An
// argument whose tokens are all empty is dropped, as at runtime.
func SampleArgv(argv []string) []string {
	var out []string
	for _, a := range argv {
		if !windowToken.MatchString(a) {
			out = append(out, a)
			continue
		}
		allEmpty := true
		s := windowToken.ReplaceAllStringFunc(a, func(m string) string {
			v := sampleDefaults[windowToken.FindStringSubmatch(m)[1]]
			allEmpty = allEmpty && v == ""
			return v
		})
		if !allEmpty {
			out = append(out, s)
		}
	}
	return out
}

// windowKind is how a windowed source is read.
type windowKind int

const (
	noWindow windowKind = iota
	seekable            // offset + limit, with a total
	anchored            // cursor + dir + limit
)

// markWindow templates the --window values into p.argv and works out
// which kind of window the tokens make.
func (p *plan) markWindow() (windowKind, map[string]bool, error) {
	w := p.opts.Window
	params := make([]Param, len(w.Marks))
	for i, m := range w.Marks {
		params[i] = Param{Name: m.Token, Value: m.Value}
	}
	argv, err := templateWith(p.argv, params, "--window", func(name string) string { return "${window." + name + "}" })
	if err != nil {
		return noWindow, nil, err
	}
	p.argv = argv
	toks := tokensIn(argv)
	if len(toks) == 0 {
		for _, f := range []struct {
			set  bool
			flag string
		}{{w.TotalPath != "", "--total-path"}, {w.CursorPath != "", "--cursor-path"}, {w.Growing, "--growing"}, {w.FollowEvery > 0, "--follow-every"}} {
			if f.set {
				return noWindow, nil, fmt.Errorf("%s: the command has no window; mark one with --window", f.flag)
			}
		}
		return noWindow, toks, nil
	}
	for t := range toks {
		if !slices.Contains(windowTokens, t) && !strings.HasPrefix(t, "filters.") {
			return noWindow, nil, fmt.Errorf("${window.%s}: not a window token (%s, or filters.NAME)", t, strings.Join(windowTokens, ", "))
		}
	}
	seek := toks["offset"]
	anch := toks["cursor"] || toks["dir"]
	switch {
	case seek && anch:
		return noWindow, nil, fmt.Errorf("the command takes both an offset and a cursor: a window is seekable (offset, limit) or anchored (cursor, dir, limit), not both")
	case seek && toks["limit"]:
		return seekable, toks, nil
	case anch && toks["cursor"] && toks["dir"] && toks["limit"]:
		return anchored, toks, nil
	case anch:
		return noWindow, nil, fmt.Errorf("an anchored window needs ${window.cursor}, ${window.dir} and ${window.limit} in the command")
	}
	return noWindow, nil, fmt.Errorf("a window needs ${window.offset} and ${window.limit} (seekable) or ${window.cursor}, ${window.dir} and ${window.limit} (anchored) in the command")
}

// totalCandidates are the envelope fields a total is looked for in.
var totalCandidates = []string{"total", "count", "totalCount", "total_count", "numFound", "totalResults", "total_results"}

// cursorCandidates are the fields an anchored window's cursor is looked
// for in.
var cursorCandidates = []string{"ts", "time", "timestamp", "@timestamp", "id"}

// eventText are the field names an eventlog's text prefers.
var eventText = []string{"msg", "message", "text", "line", "log", "stdout", "output"}

// window plans a windowed source and the table or eventlog over it.
func (p *plan) windowed(kind windowKind, toks map[string]bool, doc any, sh shape) error {
	w := p.opts.Window
	if p.opts.Refresh > 0 {
		return fmt.Errorf("--refresh: a window refetches as you scroll, filter and sort, so it isn't polled")
	}
	win := &yaml.Node{Kind: yaml.MappingNode}
	rows := sh.rows
	if sh.single != nil {
		return fmt.Errorf("the window's first page is a single object, not a list of rows")
	}
	fs := fields(rows)
	switch kind {
	case seekable:
		total := w.TotalPath
		how := "--total-path"
		if total == "" {
			how = ""
			total = findTotal(doc, sh.root)
			if total == "" {
				return fmt.Errorf("a seekable window needs a total: add one to the command's output, or use a plain source")
			}
		} else if _, ok := asNumber(doc, total); !ok {
			return fmt.Errorf("--total-path %s: the output has no number at %s", total, total)
		}
		add(win, "total_path", scalar(total))
		p.note("window", "seekable, total at "+total, how)
	case anchored:
		cur := w.CursorPath
		how := "--cursor-path"
		if cur == "" {
			how = ""
			cur = everyRowField(fs, cursorCandidates, len(rows))
			if cur == "" {
				return fmt.Errorf("an anchored window needs each item's cursor: none of %s is in every row; name it with --cursor-path", strings.Join(cursorCandidates, ", "))
			}
		} else if !allRowsHave(rows, cur) {
			return fmt.Errorf("--cursor-path %s: not every sampled row has it", cur)
		}
		add(win, "cursor", scalar(cur))
		p.note("window", "anchored, cursor "+cur+", opens at the newest", how)
		p.res.Summary = append(p.res.Summary, "next step: an input bound to window.anchor jumps to a time or cursor (see Jumping to a cursor in the sources reference)")
	}
	key := markKey(fs, len(rows))
	comp := p.opts.Component
	if comp == "" {
		comp = "table"
		if kind == anchored || readsAsEvents(fs) {
			comp = "eventlog"
		}
	}
	searchable := toks["search"]
	var filters []string
	for t := range toks {
		if name, ok := strings.CutPrefix(t, "filters."); ok {
			filters = append(filters, name)
		}
	}
	slices.Sort(filters)
	switch comp {
	case "table":
		if kind == anchored && key == "" {
			return fmt.Errorf("a table over an anchored window needs a key unique in every row (%s), and the sample has none; use --component eventlog", strings.Join(markKeyCandidates, ", "))
		}
		p.unsorted = kind == anchored || !(toks["sort"] && toks["sort_dir"])
		cols, _, err := p.columns(rows)
		if err != nil {
			return err
		}
		p.kind = comp
		// A span table (anchored) holds its rows by key; a seekable
		// window can't be marked.
		tableKey := key
		if kind == seekable {
			tableKey = ""
		}
		p.comp, p.more = p.table(p.title(), cols, tableKey)
		if !searchable && len(filters) == 0 {
			dropKeys(p.comp, "filterable")
		}
		if !p.unsorted {
			p.note("sort", "the command gets the column's title in ${window.sort}, and asc or desc in ${window.sort_dir}", "")
		}
		if fm, err := filterMap(filters, cols); err != nil {
			return err
		} else if fm != nil {
			add(win, "filters", fm)
		}
	case "eventlog":
		if key == "" && kind == anchored {
			key = keyOf(fs, cur(win), len(rows))
		}
		if key == "" {
			return fmt.Errorf("an eventlog needs a key unique in every row (%s), and the sample has none; use --component table", strings.Join(markKeyCandidates, ", "))
		}
		text := eventTextField(fs)
		if text == "" {
			return fmt.Errorf("an eventlog needs a text field to show, and the rows have no text; use --component table")
		}
		p.kind = comp
		ev := mapping("type", scalar("eventlog"), "title", scalar(p.title()), "source", scalar(p.name), "key", scalar(key), "text", scalar(text))
		if mark := timeField(fs); mark != "" && mark != text {
			add(ev, "mark", scalar(mark))
		}
		add(ev, "searchable", yes())
		if searchable || len(filters) > 0 {
			add(ev, "filterable", yes())
		}
		if levels(fieldValues(fs, text)) && !p.opts.NoStyle {
			rules := &yaml.Node{Kind: yaml.SequenceNode}
			for _, r := range []columns.Rule{{When: "~^(ERROR|FATAL)", Color: "theme:error"}, {When: "~^WARN", Color: "theme:warning"}} {
				n := mapping("when", scalar(r.When), "color", scalar(r.Color))
				n.Style = yaml.FlowStyle
				rules.Content = append(rules.Content, n)
			}
			add(ev, "color_rules", rules)
		}
		p.comp = ev
		p.note("eventlog", "key "+key+", text "+text, "")
		if len(filters) > 0 {
			fm := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
			for _, f := range filters {
				add(fm, words(f), scalar(f))
			}
			add(win, "filters", fm)
		}
	case "list", "inspector", "logview":
		return fmt.Errorf("--component %s: a window is paged by a table or an eventlog", comp)
	default:
		return p.component(comp, nil)
	}
	if w.Growing {
		if comp != "eventlog" {
			return fmt.Errorf("--growing: only an eventlog follows a growing window; this is a %s", comp)
		}
		add(win, "growing", yes())
		if w.FollowEvery > 0 {
			add(win, "follow_every", scalar(w.FollowEvery.String()))
		}
		p.note("growing", "true", "--growing")
	} else if w.FollowEvery > 0 {
		return fmt.Errorf("--follow-every needs --growing")
	}
	p.window = win
	p.note("component", comp, flagIf(p.opts.Component != "", "--component"))
	return nil
}

func cur(win *yaml.Node) string {
	for i := 0; i+1 < len(win.Content); i += 2 {
		if win.Content[i].Value == "cursor" {
			return win.Content[i+1].Value
		}
	}
	return ""
}

func dropKeys(m *yaml.Node, keys ...string) {
	for i := 0; i+1 < len(m.Content); {
		if slices.Contains(keys, m.Content[i].Value) {
			m.Content = slices.Delete(m.Content, i, i+2)
			continue
		}
		i += 2
	}
}

// filterMap maps each filters.NAME token to the column showing NAME.
func filterMap(names []string, cols []columns.Column) (*yaml.Node, error) {
	if len(names) == 0 {
		return nil, nil
	}
	fm := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	for _, n := range names {
		i := slices.IndexFunc(cols, func(c columns.Column) bool {
			return strings.EqualFold(c.Path, n) || strings.EqualFold(lastSegment(c.Path), n)
		})
		if i < 0 {
			return nil, fmt.Errorf("filters.%s: no column shows %s, so a %s:value term has nowhere to come from; add it with --columns", n, n, n)
		}
		add(fm, cols[i].Title, scalar(n))
	}
	return fm, nil
}

// findTotal finds a numeric total beside the rows: a top-level field
// named like one.
func findTotal(doc any, root string) string {
	if root == "" {
		return ""
	}
	for _, c := range totalCandidates {
		if _, ok := asNumber(doc, c); ok {
			return c
		}
	}
	return ""
}

func asNumber(doc any, path string) (json.Number, bool) {
	o, _ := doc.(*object)
	v, ok := lookup(o, path)
	n, isNum := v.(json.Number)
	return n, ok && isNum
}

// everyRowField is the first of names (any case) set in every row.
func everyRowField(fs []*field, names []string, rows int) string {
	for _, n := range names {
		if i := slices.IndexFunc(fs, func(f *field) bool { return strings.EqualFold(f.path, n) }); i >= 0 && !fs[i].list && fs[i].set == rows {
			return fs[i].path
		}
	}
	return ""
}

func allRowsHave(rows []*object, path string) bool {
	for _, r := range rows {
		if v, ok := lookup(r, path); !ok || v == nil {
			return false
		}
	}
	return true
}

// keyOf is path when it's unique across the rows.
func keyOf(fs []*field, path string, rows int) string {
	i := slices.IndexFunc(fs, func(f *field) bool { return f.path == path })
	if i < 0 || fs[i].set != rows || len(distinct(fs[i].values)) != rows {
		return ""
	}
	return path
}

// readsAsEvents is a sample with a timestamp and a message-like text.
func readsAsEvents(fs []*field) bool {
	return timeField(fs) != "" && slices.ContainsFunc(fs, func(f *field) bool {
		return f.strs > 0 && slices.Contains(eventText, strings.ToLower(lastSegment(f.path)))
	})
}

// eventTextField is what an eventlog item draws as: a message-like field,
// else the longest text.
func eventTextField(fs []*field) string {
	for _, n := range eventText {
		if i := slices.IndexFunc(fs, func(f *field) bool { return f.strs > 0 && strings.EqualFold(lastSegment(f.path), n) }); i >= 0 {
			return fs[i].path
		}
	}
	best, bestLen := "", 0.0
	for _, f := range fs {
		if f.strs > 0 && !f.list {
			if l := averageLength(f.values); l > bestLen {
				best, bestLen = f.path, l
			}
		}
	}
	return best
}

// timeField is the first timestamp field.
func timeField(fs []*field) string {
	for _, f := range fs {
		if !f.list && (columns.IsTimeName(lastSegment(f.path)) || allTimestamps(f.values)) && f.set > 0 {
			return f.path
		}
	}
	return ""
}

func fieldValues(fs []*field, path string) []string {
	if i := slices.IndexFunc(fs, func(f *field) bool { return f.path == path }); i >= 0 {
		return fs[i].values
	}
	return nil
}

var levelPrefix = regexp.MustCompile(`(?i)^\W*(error|fatal|warn)`)

// levels is true when the text carries its level up front ("ERROR …").
func levels(values []string) bool {
	return slices.ContainsFunc(values, levelPrefix.MatchString)
}
