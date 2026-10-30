package scaffold

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/columns"
	"github.com/jsdrews/tui-builder/internal/config"
)

// ExecOptions is what `tui-builder add exec` was asked for.
type ExecOptions struct {
	// Argv is the command, exactly as given after --.
	Argv []string
	// Timeout is --timeout; zero leaves the source on the exec default
	// and writes no timeout:.
	Timeout time.Duration
	// Root is --root: where the rows are, overriding inference.
	Root string
	// MaxColumns is --max-columns: how many columns are kept, the rest
	// written commented out. Zero is columns.DefaultMax.
	MaxColumns int
	// NoStyle is --no-style: no bold, colour rules or widths.
	NoStyle bool
	// Params are --param: argv values made into source parameters.
	Params []Param
	// NoInputs is --no-inputs: parameters without input fields.
	NoInputs bool
	// Columns is --columns: the set and order, skipping ranking.
	Columns []ColumnSpec
	// Component is --component; "" infers it.
	Component string
	// Name is --name for the source and component; "" names them from
	// the command.
	Name string
	// Title is --title for the component; "" title-cases the name.
	Title string
	// Refresh is --refresh; zero leaves the source one-shot.
	Refresh time.Duration
	// Follow is --follow / --no-follow; with --no-run only FollowYes
	// matters, and writes a stream source.
	Follow Follow
	// Window is --window and the flags that shape a window.
	Window WindowOptions
}

// Components add exec can write.
var Components = []string{"table", "list", "inspector", "eventlog", "logview"}

// Result is a written config and what to tell the user about it.
type Result struct {
	Config []byte
	// parts are its pieces, for splicing into another config.
	parts *parts
	// Summary lists each choice and whether it was inferred or came
	// from a flag.
	Summary []string
	// Warnings are flags the sample didn't bear out, short of an error.
	Warnings []string
}

// plan is what's been decided about the config being written.
type plan struct {
	opts    ExecOptions
	res     Result
	name    string
	argv    []string // with the --param values templated
	root    string
	comp    *yaml.Node
	kind    string
	more    []string
	compKey string // the component's name in tui.components
	follow  bool   // the source streams
	text    bool   // the source is format: text
	// window is the source's window: block, when it has one.
	window   *yaml.Node
	unsorted bool // columns can't sort: a window that doesn't pass one
}

func (p *plan) note(what, value string, fromFlag string) {
	how := "inferred"
	if fromFlag != "" {
		how = fromFlag
	}
	p.res.Summary = append(p.res.Summary, fmt.Sprintf("%s: %s (%s)", what, value, how))
}

// ExecConfig infers a source and the component that shows it from a
// sample of the command's output, and returns a complete config.
func ExecConfig(opts ExecOptions, out Output) (Result, error) {
	p, err := newPlan(opts)
	if err != nil {
		return Result{}, err
	}
	wkind, toks, err := p.markWindow()
	if err != nil {
		return Result{}, err
	}
	if out.Stream {
		if wkind != noWindow {
			return Result{}, fmt.Errorf("the command streams, and a stream isn't paged: drop the window tokens, or pass --no-follow if it finishes")
		}
		if err := p.stream(out); err != nil {
			return Result{}, err
		}
		return p.finish()
	}
	values, err := decodeSample(out)
	if err != nil {
		return Result{}, err
	}
	sh, err := findRows(values, opts.Root)
	if err != nil {
		return Result{}, err
	}
	p.root = sh.root
	if sh.root != "" {
		p.note("rows", "root: "+sh.root, flagIf(opts.Root != "", "--root"))
	}
	if wkind != noWindow {
		var doc any
		if len(values) == 1 {
			doc = values[0]
		}
		if err := p.windowed(wkind, toks, doc, sh); err != nil {
			return Result{}, err
		}
		return p.finish()
	}
	rows := sh.rows
	kind := opts.Component
	if kind == "" {
		kind = "table"
		if sh.single != nil && len(opts.Columns) == 0 {
			kind = "inspector"
		}
	}
	if sh.single != nil {
		rows = []*object{sh.single}
	}
	if err := p.component(kind, rows); err != nil {
		return Result{}, err
	}
	return p.finish()
}

// ExecConfigNoRun is --no-run: the source, unsampled. Without a sample
// there's no shape, so it's shown by an auto inspector unless
// --columns gives a table or list its columns.
func ExecConfigNoRun(opts ExecOptions) (Result, error) {
	p, err := newPlan(opts)
	if err != nil {
		return Result{}, err
	}
	p.root = opts.Root
	if kind, _, err := p.markWindow(); err != nil {
		return Result{}, err
	} else if kind != noWindow {
		return Result{}, fmt.Errorf("--no-run: a window's total, cursor and key come from a sample, so run the command once")
	}
	if opts.Follow == FollowYes {
		return p.unsampledStream()
	}
	kind := opts.Component
	switch {
	case kind == "" && len(opts.Columns) > 0:
		kind = "table"
	case kind == "":
		kind = "inspector"
	case (kind == "table" || kind == "list") && len(opts.Columns) == 0:
		return Result{}, fmt.Errorf("--component %s with --no-run needs --columns: there's no sample to take them from", kind)
	}
	if err := p.component(kind, nil); err != nil {
		return Result{}, err
	}
	if kind == "inspector" {
		p.comp.HeadComment = "Not sampled (--no-run), so no columns: an auto inspector shows whatever\n" +
			"the command returns. Swap it for a table once you know the fields."
	}
	return p.finish()
}

func flagIf(set bool, flag string) string {
	if set {
		return flag
	}
	return ""
}

var nameText = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func newPlan(opts ExecOptions) (*plan, error) {
	p := &plan{opts: opts}
	argv, err := templateArgv(opts.Argv, opts.Params)
	if err != nil {
		return nil, err
	}
	p.argv = argv
	p.name = opts.Name
	if p.name == "" {
		p.name = sourceName(opts.Argv)
		p.note("name", p.name, "")
	} else {
		if !nameText.MatchString(p.name) {
			return nil, fmt.Errorf("--name %q: use letters, digits, _ and -, starting with a letter", p.name)
		}
		p.note("name", p.name, "--name")
	}
	return p, nil
}

// component builds the component of the given kind over rows (nil when
// unsampled).
func (p *plan) component(kind string, rows []*object) error {
	fromFlag := flagIf(p.opts.Component != "", "--component")
	title := p.opts.Title
	if title == "" {
		title = titleCase(p.name)
	}
	p.kind = kind
	switch kind {
	case "inspector":
		p.comp = mapping("type", scalar("inspector"), "title", scalar(title), "source", scalar(p.name), "auto", yes())
	case "table", "list":
		cols, key, err := p.columns(rows)
		if err != nil {
			return err
		}
		if kind == "list" {
			item := ""
			if len(p.opts.Columns) > 0 {
				item = cols[0].Path
			} else {
				fs := fields(rows)
				item = identityOf(fs, key)
			}
			if item == "" {
				return fmt.Errorf("--component list: the rows have no name, title or id to show; pick the field with --columns PATH")
			}
			p.comp = mapping("type", scalar("list"), "title", scalar(title), "source", scalar(p.name), "item", scalar(item), "filterable", yes())
			p.note("list item", item, flagIf(len(p.opts.Columns) > 0, "--columns"))
			break
		}
		p.comp, p.more = p.table(title, cols, key)
	case "eventlog":
		return fmt.Errorf("--component eventlog: an eventlog shows a windowed source, and this one isn't")
	case "logview":
		return fmt.Errorf("--component logview: a logview shows text or a stream, and this command printed JSON")
	default:
		return fmt.Errorf("--component %q: want one of %s", kind, strings.Join(Components, ", "))
	}
	p.note("component", kind, fromFlag)
	return nil
}

// columns works out the columns: ranked from the sample, or the
// --columns set in its order, styled from what the sample shows of
// each. It also returns the mark key.
func (p *plan) columns(rows []*object) ([]columns.Column, string, error) {
	fs := fields(rows)
	key := ""
	if len(rows) > 0 {
		key = markKey(fs, len(rows))
	}
	ranked := classify(fs, key)
	if len(p.opts.Columns) == 0 {
		if len(ranked) == 0 {
			return nil, "", fmt.Errorf("the rows have no fields to show: every field is null, empty or a list of objects")
		}
		return ranked, key, nil
	}
	var chosen []columns.Column
	titles := map[string]string{}
	for _, spec := range p.opts.Columns {
		i := slices.IndexFunc(ranked, func(c columns.Column) bool { return c.Path == spec.Path })
		var c columns.Column
		switch {
		case i >= 0:
			c = ranked[i]
		default:
			c = columns.Column{Path: spec.Path, Title: words(lastSegment(spec.Path)), Rank: columns.Short}
			if rows != nil && !anyRowHas(rows, spec.Path) {
				p.res.Warnings = append(p.res.Warnings, fmt.Sprintf("--columns %s: no sampled row has it", spec.Path))
			}
		}
		if spec.Title != "" {
			c.Title = spec.Title
		}
		if other, dup := titles[strings.ToLower(c.Title)]; dup {
			return nil, "", fmt.Errorf("--columns: %s and %s are both titled %q; name one with PATH=Title", other, spec.Path, c.Title)
		}
		titles[strings.ToLower(c.Title)] = spec.Path
		chosen = append(chosen, c)
	}
	p.note("columns", fmt.Sprint(len(chosen)), "--columns")
	return chosen, key, nil
}

func anyRowHas(rows []*object, path string) bool {
	for _, r := range rows {
		if v, ok := lookup(r, path); ok && v != nil {
			return true
		}
	}
	return false
}

// table writes the table, and returns the commented-out lines for the
// columns past the cap.
func (p *plan) table(title string, cols []columns.Column, key string) (*yaml.Node, []string) {
	t := mapping(
		"type", scalar("table"),
		"title", scalar(title),
		"source", scalar(p.name),
		"filterable", yes(),
	)
	if key != "" {
		add(t, "markable", yes())
		add(t, "mark_key", scalar(key))
		p.note("mark_key", key, "")
	}
	kept, rest := cols, []columns.Column(nil)
	if len(p.opts.Columns) == 0 {
		max := p.opts.MaxColumns
		if max == 0 {
			max = columns.DefaultMax
		}
		kept, rest = columns.Pick(cols, max)
		shown := fmt.Sprintf("%d, ranked", len(kept))
		if len(rest) > 0 {
			shown = fmt.Sprintf("%d of %d, ranked; the rest commented out", len(kept), len(cols))
		}
		p.note("columns", shown, flagIf(p.opts.MaxColumns != 0 && p.opts.MaxColumns != columns.DefaultMax, "--max-columns"))
	}
	styles := columns.Styles(kept)
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for i, c := range kept {
		style := styles[i]
		if p.opts.NoStyle {
			style = columns.Style{}
		}
		seq.Content = append(seq.Content, columnNode(c, style, p.unsorted))
	}
	if len(rest) > 0 {
		// yaml.v3 misplaces a sequence's foot comment, so a placeholder
		// item marks the spot and finish swaps in the comment lines.
		seq.Content = append(seq.Content, scalar(moreColumnsMarker))
	}
	var more []string
	for _, c := range rest {
		more = append(more, "- "+flowText(columnNode(c, columns.Style{}, p.unsorted)))
	}
	add(t, "columns", seq)
	return t, more
}

// columnNode writes one column, on one line unless it has colour rules.
// unsorted leaves out sortable and sort, for a window whose command
// can't sort.
func columnNode(c columns.Column, s columns.Style, unsorted bool) *yaml.Node {
	col := mapping("title", scalar(c.Title), "value", scalar(c.Path))
	if !unsorted {
		add(col, "sortable", yes())
		if c.Sort != "" {
			add(col, "sort", scalar(c.Sort))
		}
	}
	if s.Bold {
		add(col, "bold", yes())
	}
	if s.Align != "" {
		add(col, "align", scalar(s.Align))
	}
	if s.Flex > 0 {
		add(col, "flex", intNode(s.Flex))
	}
	if s.MaxWidth > 0 {
		add(col, "max_width", intNode(s.MaxWidth))
	}
	if len(s.Rules) == 0 {
		col.Style = yaml.FlowStyle
		return col
	}
	rules := &yaml.Node{Kind: yaml.SequenceNode}
	for _, r := range s.Rules {
		rule := mapping("when", scalar(r.When), "color", scalar(r.Color))
		rule.Style = yaml.FlowStyle
		rules.Content = append(rules.Content, rule)
	}
	add(col, "color_rules", rules)
	return col
}

// flowText is a node written as one line of flow YAML.
func flowText(n *yaml.Node) string {
	b, err := yaml.Marshal(n)
	if err != nil {
		panic(err) // nodes built here always marshal
	}
	return strings.TrimSpace(string(b))
}

func intNode(n int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(n)}
}

// finish wraps the source, the component and any input fields in a
// complete config, and checks it loads.
// parts are what add exec adds to a config: one source, its
// component(s), and the layout node that shows them.
type parts struct {
	name   string
	title  string
	kind   string
	argv   []string // the command as given, for the comment
	source *yaml.Node
	// components are name, node pairs: the component, then any input
	// fields.
	components *yaml.Node
	layout     *yaml.Node
	more       []string
}

func (p *plan) finish() (Result, error) {
	pt := p.parts()
	appTitle := p.opts.Title
	if appTitle == "" {
		appTitle = titleCase(p.name)
	}
	doc := mapping(
		"app", mapping("title", scalar(appTitle)),
		"data", mapping("sources", mapping(pt.name, pt.source)),
		"tui", mapping(
			"components", pt.components,
			"screen", mapping("layout", pt.layout),
		),
	)
	doc.HeadComment = "Written by `tui-builder add exec -- " + ShellJoin(p.opts.Argv) + "`."
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return Result{}, err
	}
	if err := enc.Close(); err != nil {
		return Result{}, err
	}
	b := expandMoreColumns(buf.Bytes(), p.more)
	if err := checkLoads(b); err != nil {
		return Result{}, fmt.Errorf("the config written doesn't load, which is a bug in add exec: %w", err)
	}
	p.res.Config = b
	p.res.parts = pt
	return p.res, nil
}

// parts builds the source, the components and the layout.
func (p *plan) parts() *parts {
	src := mapping("type", scalar("exec"), "command", flowSeq(p.argv))
	if len(p.opts.Params) > 0 {
		params := &yaml.Node{Kind: yaml.MappingNode}
		for _, pa := range p.opts.Params {
			decl := mapping()
			if pa.Type != "" && pa.Type != "string" {
				add(decl, "type", scalar(pa.Type))
			}
			add(decl, "default", scalar(pa.Value))
			decl.Style = yaml.FlowStyle
			add(params, pa.Name, decl)
		}
		add(src, "parameters", params)
	}
	if p.root != "" {
		add(src, "root", scalar(p.root))
	}
	if p.follow {
		add(src, "follow", yes())
	}
	if p.window != nil {
		add(src, "window", p.window)
	}
	if p.text {
		add(src, "format", scalar("text"))
	}
	if p.opts.Timeout > 0 && !p.follow {
		add(src, "timeout", scalar(p.opts.Timeout.String()))
	}
	if p.opts.Refresh > 0 {
		add(src, "refresh", scalar(p.opts.Refresh.String()))
		p.note("refresh", p.opts.Refresh.String(), "--refresh")
	}

	comps := mapping(p.name, p.comp)
	layout := mapping("component", scalar(p.name))
	if len(p.opts.Params) > 0 && !p.opts.NoInputs {
		bind := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
		row := &yaml.Node{Kind: yaml.SequenceNode}
		for _, pa := range p.opts.Params {
			in := inputName(pa.Name, p.name)
			field := mapping("type", scalar("input"), "title", scalar(words(pa.Name)), "initial", scalar(pa.Value))
			if pa.Type == "bool" {
				field = mapping("type", scalar("toggle"), "title", scalar(words(pa.Name)), "initial", scalar(pa.Value))
			}
			add(comps, in, field)
			add(bind, pa.Name, scalar("${input."+in+"}"))
			row.Content = append(row.Content, mapping("component", scalar(in)))
		}
		add(p.comp, "on_input", mapping("bind", bind))
		var inputs *yaml.Node
		if len(row.Content) == 1 {
			inputs = mapping("fixed", intNode(3), "component", row.Content[0].Content[1])
		} else {
			inputs = mapping("fixed", intNode(3), "hstack", row)
		}
		layout = mapping("vstack", &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{
			inputs,
			mapping("flex", intNode(1), "component", scalar(p.name)),
		}})
		p.note("input fields", fmt.Sprint(len(p.opts.Params)), "--param")
	} else if len(p.opts.Params) > 0 {
		p.note("input fields", "none", "--no-inputs")
	}

	return &parts{
		name: p.name, title: p.title(), kind: p.kind, argv: p.opts.Argv,
		source: src, components: comps, layout: layout, more: p.more,
	}
}

// inputName names a parameter's input field: the parameter's own name,
// unless the source and component already have it.
func inputName(param, taken string) string {
	if param == taken {
		return param + "_input"
	}
	return param
}

// moreColumnsMarker is the placeholder column standing in for the
// commented-out rest until expandMoreColumns replaces it.
const moreColumnsMarker = "__more_columns__"

// expandMoreColumns replaces the placeholder item with the
// commented-out columns, at its indentation.
func expandMoreColumns(b []byte, more []string) []byte {
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		indent, ok := strings.CutSuffix(l, "- "+moreColumnsMarker)
		if !ok || strings.TrimSpace(indent) != "" {
			continue
		}
		block := []string{indent + "# More fields from the sample; uncomment one to add it:"}
		for _, c := range more {
			block = append(block, indent+"# "+c)
		}
		lines = slices.Replace(lines, i, i+1, block...)
		break
	}
	return []byte(strings.Join(lines, "\n"))
}

// checkLoads runs config.Load on b, from a temporary file.
func checkLoads(b []byte) error {
	dir, err := os.MkdirTemp("", "tui-builder-add-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	_, err = config.LoadTo(path, io.Discard)
	return err
}

// scalar is a string value, quoted when it would read as anything else.
func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func yes() *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"} }

func mapping(kv ...any) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i < len(kv); i += 2 {
		add(m, kv[i].(string), kv[i+1].(*yaml.Node))
	}
	return m
}

func add(m *yaml.Node, k string, v *yaml.Node) {
	m.Content = append(m.Content, scalar(k), v)
}

func flowSeq(items []string) *yaml.Node {
	s := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, it := range items {
		s.Content = append(s.Content, scalar(it))
	}
	return s
}

// titleCase turns a snake_case name into a title: pods → "Pods",
// node_pools → "Node pools".
func titleCase(name string) string {
	s := strings.ReplaceAll(name, "_", " ")
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// ShellJoin writes argv the way you'd type it, quoting what a shell
// would split or expand.
func ShellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if shellSafe.MatchString(a) {
			parts[i] = a
		} else {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}
