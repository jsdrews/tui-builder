// Package build constructs live tuilib components and a layout.Node tree
// from the YAML config. The output is owned by the screen, which holds the
// component pointers for state preservation across theme swaps.
package build

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/ansi"
	"github.com/jsdrews/tuilib/pkg/eventlog"
	"github.com/jsdrews/tuilib/pkg/inspector"
	"github.com/jsdrews/tuilib/pkg/list"
	"github.com/jsdrews/tuilib/pkg/logview"
	"github.com/jsdrews/tuilib/pkg/remote"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/textview"
	"github.com/jsdrews/tuilib/pkg/theme"
	"github.com/jsdrews/tuilib/pkg/tree"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// Kind enumerates the supported leaf component types.
type Kind int

const (
	KList Kind = iota
	KTable
	KLogview
	KTree
	KInspector
	KTextview
	KEventlog
)

// Component is a live, themed component pointer plus the originating config
// — kept so SetTheme can rebuild the same component against a new theme.
type Component struct {
	Cfg  *cfg.Component
	Kind Kind

	// Exactly one of these is non-nil based on Kind.
	List      *list.Model
	Table     *table.Model
	Logview   *logview.Model
	Tree      *tree.Model
	Inspector *inspector.Model
	Textview  *textview.Model
	Eventlog  *eventlog.Model

	// Remote is set on a table bound to a windowed source, which tuilib's
	// pkg/remote drives; Table then points at its embedded table. Every
	// message goes through Remote.Update rather than Table.Update.
	Remote *remote.Table
	// AtNewest is set on a span table anchored at its newest row. tuilib
	// puts a span table's cursor on its first row whenever a new answer
	// lands, which for a view anchored at the newest is the wrong end and
	// leaves it at the older edge, loading older rows by itself. The
	// screen moves it to the newest row instead; landed records which
	// answer it last did that for.
	AtNewest bool
	landed   string
	// RemoteLog is the eventlog's counterpart: an eventlog always binds a
	// windowed source, and Eventlog points at this binding's embedded
	// model once the screen has built it.
	RemoteLog *remote.Eventlog

	// StreamRows is the ring buffer used by KTable components bound to
	// a streaming source. Newest events first; trimmed to Cfg.MaxRows
	// (defaulted to 100 for streaming bindings) on every append. nil
	// for non-streaming tables and non-table kinds.
	StreamRows []any

	// RowsByKey resolves a markable table's mark key back to the row it
	// names. Rebuilt on every data application, alongside the rows
	// themselves, so it can't describe a set the table no longer holds.
	// nil unless Cfg.Markable is set on a table.
	RowsByKey map[string]Selection

	// last is the data the most recent fetch applied, kept so Rebuild can
	// re-render it with a new theme's colors. hasLast tells "never
	// fetched" apart from a fetch that returned nil.
	last    any
	hasLast bool
}

// NewComponent builds a Component from a config leaf and the active theme.
func NewComponent(c *cfg.Component, th theme.Theme) (*Component, error) {
	switch c.Type {
	case "list":
		m := buildList(c, th)
		return &Component{Cfg: c, Kind: KList, List: &m}, nil
	case "table":
		m := buildTable(c, th)
		return &Component{Cfg: c, Kind: KTable, Table: &m}, nil
	case "logview":
		m := buildLogview(c, th)
		return &Component{Cfg: c, Kind: KLogview, Logview: &m}, nil
	case "tree":
		m := buildTree(c, th)
		return &Component{Cfg: c, Kind: KTree, Tree: &m}, nil
	case "inspector":
		m := buildInspector(c, th)
		return &Component{Cfg: c, Kind: KInspector, Inspector: &m}, nil
	case "textview":
		m := buildTextview(c, th)
		return &Component{Cfg: c, Kind: KTextview, Textview: &m}, nil
	case "eventlog":
		// A stand-in until the screen binds it to its source with
		// NewRemoteEventlog; it draws an empty pane meanwhile.
		m := eventlog.New(eventlogOptions(c, th))
		return &Component{Cfg: c, Kind: KEventlog, Eventlog: &m}, nil
	}
	return nil, fmt.Errorf("unknown component type %q", c.Type)
}

// Rebuild reconstructs the component against a new theme. Options carry
// the palette, so tuilib can't restyle a component in place (its rule 4);
// instead each component's State captures everything the user and the data
// did to it, and Restore puts that back on the fresh build. That includes
// the data itself, so a fetched table or list stays populated across a
// theme swap rather than waiting for a refetch a one-shot source will never
// make.
//
// Restore brings back cells as they were rendered, colors included, so a
// component with color rules is then re-rendered from what it was given
// (see recolor). Windowed tables and eventlogs restyle through tuilib's
// remote binding instead, and pick up new rule colors with the next page
// they fetch.
func (c *Component) Rebuild(th theme.Theme) {
	switch c.Kind {
	case KList:
		st := c.List.State()
		*c.List = buildList(c.Cfg, th)
		c.List.Restore(st)
	case KTable:
		if c.Remote != nil {
			c.Remote.Restyle(tableOptions(c.Cfg, th))
			return
		}
		st := c.Table.State()
		*c.Table = buildTable(c.Cfg, th)
		c.Table.Restore(st)
	case KLogview:
		st := c.Logview.State()
		*c.Logview = buildLogview(c.Cfg, th)
		c.Logview.Restore(st)
	case KTree:
		st := c.Tree.State()
		*c.Tree = buildTree(c.Cfg, th)
		c.Tree.Restore(st)
	case KInspector:
		st := c.Inspector.State()
		*c.Inspector = buildInspector(c.Cfg, th)
		c.Inspector.Restore(st)
	case KTextview:
		st := c.Textview.State()
		*c.Textview = buildTextview(c.Cfg, th)
		c.Textview.Restore(st)
	case KEventlog:
		if c.RemoteLog != nil {
			c.RemoteLog.Restyle(eventlogOptions(c.Cfg, th))
			return
		}
		st := c.Eventlog.State()
		*c.Eventlog = eventlog.New(eventlogOptions(c.Cfg, th))
		c.Eventlog.Restore(st)
	}
	c.recolor(th)
}

// recolor re-renders a component's content with th so a `theme:` token
// in a color rule follows a theme swap. It runs after Restore and goes
// through the same setters a poll does, so cursor, filter, sort and marks
// survive it the way they survive a refresh. Components without color
// rules have nothing theme-dependent in their cells and are left alone.
func (c *Component) recolor(th theme.Theme) {
	if !hasColorRules(c.Cfg) {
		return
	}
	switch {
	case c.StreamRows != nil:
		renderStreamRows(c, th)
	case c.hasLast:
		ApplyData(c, c.last, th)
	case c.Cfg.Source == "":
		c.restatic(th)
	}
}

// restatic re-renders content declared in the config itself.
func (c *Component) restatic(th theme.Theme) {
	switch c.Kind {
	case KList:
		c.List.SetItems(staticListItems(c.Cfg, th))
	case KTable:
		c.Table.SetRows(staticTableRows(c.Cfg, th))
	case KLogview:
		c.Logview.Clear()
		c.Logview.AppendLines(staticLogLines(c.Cfg, th))
	case KInspector:
		c.Inspector.SetFields(convertFields(c.Cfg.Fields, th))
	}
}

// hasColorRules reports whether any part of c renders through a color
// rule: the component's own rules, a column's, or an inspector field's
// at any depth.
func hasColorRules(c *cfg.Component) bool {
	if len(c.ColorRules) > 0 {
		return true
	}
	for _, col := range c.Columns {
		if len(col.ColorRules) > 0 {
			return true
		}
	}
	var fields func([]cfg.InspectorField) bool
	fields = func(fs []cfg.InspectorField) bool {
		for _, f := range fs {
			if len(f.ColorRules) > 0 || fields(f.Children) {
				return true
			}
		}
		return false
	}
	return fields(c.Fields)
}

// ---------------------------------------------------------------- list ---

func buildList(c *cfg.Component, th theme.Theme) list.Model {
	opts := th.List()
	remapListKeys(&opts.Keys)
	opts.Title = c.Title
	if c.Source == "" {
		opts.Items = staticListItems(c, th)
	}
	opts.Filterable = c.Filterable
	opts.Markable = c.Markable
	if busy := busyFunc(c.Activity); busy != nil {
		opts.BusyWhen = func(it list.KeyedItem) (string, bool) { return busy(it.Data) }
	}
	if rev := revisionFunc(c.Activity); rev != nil {
		opts.Revision = func(it list.KeyedItem) string { return rev(it.Data) }
	}
	opts.LoadingLabel = c.LoadingLabel
	if c.FilterPlaceholder != "" {
		opts.Filter.Placeholder = c.FilterPlaceholder
	}
	if cs := c.Colors; cs != nil {
		if v := parseColor(cs.BorderActive, th); v != nil {
			opts.ActiveColor = v
		}
		if v := parseColor(cs.BorderInactive, th); v != nil {
			opts.InactiveColor = v
		}
		if v := parseColor(cs.Selected, th); v != nil {
			opts.SelectedStyle = opts.SelectedStyle.Foreground(v)
		}
		if v := parseColor(cs.Spinner, th); v != nil {
			opts.SpinnerStyle = opts.SpinnerStyle.Foreground(v)
		}
		if v := parseColor(cs.Mark, th); v != nil {
			opts.MarkStyle = opts.MarkStyle.Foreground(v)
		}
	}
	m := list.New(opts)
	// Options.Items seeds display strings but no keys, and marks are
	// held by key — so a static markable list has to go back through
	// the keyed setter or its gutter would never respond.
	if c.Markable && c.Source == "" {
		m.SetKeyedItems(keyedItems(opts.Items))
	}
	if c.InitialFilter != "" {
		m.SetValue(c.InitialFilter)
	}
	if c.InitialCursor > 0 {
		m.SetCursor(c.InitialCursor)
	}
	return m
}

// --------------------------------------------------------------- table ---

func buildTable(c *cfg.Component, th theme.Theme) table.Model {
	opts := tableOptions(c, th)
	m := table.New(opts)
	initTable(&m, c, opts)
	return m
}

// tableOptions is everything about a table that comes from its config and
// the theme. Split from buildTable so a windowed table, which tuilib's
// remote.NewTable constructs, and its Restyle on a theme swap start from
// the same options as an ordinary one.
func tableOptions(c *cfg.Component, th theme.Theme) table.Options {
	opts := th.Table()
	remapTableKeys(&opts.Keys)
	opts.Title = c.Title
	opts.Filterable = c.Filterable
	opts.Markable = c.Markable
	if busy := busyFunc(c.Activity); busy != nil {
		opts.ActivityColumn = cfg.ActivityColumn(c)
		opts.BusyWhen = func(r table.KeyedRow) (string, bool) { return busy(r.Data) }
	}
	if rev := revisionFunc(c.Activity); rev != nil {
		opts.Revision = func(r table.KeyedRow) string { return rev(r.Data) }
	}
	opts.LoadingLabel = c.LoadingLabel
	if c.FilterPlaceholder != "" {
		opts.Filter.Placeholder = c.FilterPlaceholder
	}
	if c.Windowed {
		// A windowed table holds one page of a larger set, so filtering
		// and sorting it locally would answer over the 100 rows that
		// happen to be resident and present that as the answer over all
		// of them. Both go to the source instead; the screen turns the
		// resulting QueryChangedMsg into a request.
		opts.FilterMode = table.FilterRemote
		opts.SortMode = table.SortRemote
	}
	if cs := c.Colors; cs != nil {
		if v := parseColor(cs.BorderActive, th); v != nil {
			opts.ActiveColor = v
		}
		if v := parseColor(cs.BorderInactive, th); v != nil {
			opts.InactiveColor = v
		}
		if v := parseColor(cs.Header, th); v != nil {
			opts.HeaderStyle = opts.HeaderStyle.Foreground(v)
		}
		if v := parseColor(cs.SelectedFG, th); v != nil {
			opts.SelectedStyle = opts.SelectedStyle.Foreground(v)
		}
		if v := parseColor(cs.SelectedBG, th); v != nil {
			opts.SelectedStyle = opts.SelectedStyle.Background(v)
		}
		if v := parseColor(cs.Cell, th); v != nil {
			opts.CellStyle = opts.CellStyle.Foreground(v)
		}
		if v := parseColor(cs.Spinner, th); v != nil {
			opts.SpinnerStyle = opts.SpinnerStyle.Foreground(v)
		}
		if v := parseColor(cs.Mark, th); v != nil {
			opts.MarkStyle = opts.MarkStyle.Foreground(v)
		}
		if n := colorIndexFromSpec(cs.ColumnSeparator, th); n >= 0 {
			opts.Borders.Vertical = ansi.CellColor(n, "│")
		}
		if n := colorIndexFromSpec(cs.HeaderRule, th); n >= 0 {
			opts.Borders.HeaderRule = ansi.CellColor(n, "─")
		}
	}
	opts.Columns = make([]table.Column, len(c.Columns))
	for i, col := range c.Columns {
		opts.Columns[i] = table.Column{
			Title:    col.Title,
			Width:    col.Width,
			Flex:     col.Flex,
			MaxWidth: col.MaxWidth,
			Align:    parseAlign(col.Align),
			Sortable: col.Sortable,
			Less:     parseLess(col.Sort),
			Hidden:   col.Hidden,
		}
	}
	if c.Source == "" {
		opts.Rows = staticTableRows(c, th)
	}
	if c.Spanned {
		opts.MaxItems = c.MaxItems
	}
	if c.SortDebounce != "" {
		// Validated at load. tuilib reads zero as its default, so "0"
		// (sort on every change) is its negative value.
		if d, err := time.ParseDuration(c.SortDebounce); err == nil {
			if d == 0 {
				d = -1
			}
			opts.SortDebounce = d
		}
	}
	return opts
}

// initTable applies the config's starting state to a freshly built table.
func initTable(m *table.Model, c *cfg.Component, opts table.Options) {
	// Same reason as buildList: Options.Rows carries cells but no
	// keys. A static table's rows are fixed at load, so position is a
	// legitimate identity — nothing repolls them into a new order.
	if c.Markable && c.Source == "" {
		m.SetKeyedRows(indexKeyedRows(opts.Rows))
	}
	if c.InitialFilter != "" {
		m.SetValue(c.InitialFilter)
	}
	if c.InitialCursor > 0 {
		m.SetCursor(c.InitialCursor)
	}
	if c.InitialSort != nil {
		if idx, ok := resolveSortColumn(c.InitialSort.Column, c.Columns); ok {
			m.SetSort(idx, c.InitialSort.Desc)
		}
	}
}

// NewRemoteTable turns c into a table bound to a remote source through
// tuilib's pkg/remote, which routes the viewport, the filter and the sort
// to the source and installs what comes back. c.Table keeps pointing at
// the embedded table, so everything that reads a table reads this one.
func NewRemoteTable(c *Component, th theme.Theme, shape remote.Shape[table.KeyedRow]) {
	opts := tableOptions(c.Cfg, th)
	c.Remote = remote.NewTable(opts, shape)
	initTable(&c.Remote.Model, c.Cfg, opts)
	c.Table = &c.Remote.Model
	// A remote filter suggests only what's on the loaded page unless it's
	// told the values; `distinct: {source:}` arrives later, by fetch.
	for i, col := range c.Cfg.Columns {
		if col.Distinct != nil && len(col.Distinct.Values) > 0 {
			c.Table.SetDistinct(i, col.Distinct.Values)
		}
	}
}

// ApplyDistinct hands column col of a windowed table the values its
// `distinct: {source:}` answered: a list, each item as a string.
func ApplyDistinct(c *Component, col int, data any) {
	if c == nil || c.Table == nil {
		return
	}
	c.Table.SetDistinct(col, distinctValues(data))
}

// distinctValues reads a source's answer as a list of values, skipping
// empties.
func distinctValues(data any) []string {
	items := ds.Iter(data)
	values := make([]string, 0, len(items))
	for _, it := range items {
		if s := ds.String(it, ""); s != "" {
			values = append(values, s)
		}
	}
	return values
}

// resolveSortColumn maps a column reference (1-based number or title-prefix
// match, case-insensitive) to its 0-based column index, returning ok=false
// if the column isn't found or isn't Sortable.
func resolveSortColumn(ref string, cols []cfg.Column) (int, bool) {
	if n, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil {
		i := n - 1
		if i >= 0 && i < len(cols) && cols[i].Sortable {
			return i, true
		}
		return 0, false
	}
	q := strings.ToLower(strings.TrimSpace(ref))
	for i, col := range cols {
		if !col.Sortable {
			continue
		}
		if strings.HasPrefix(strings.ToLower(col.Title), q) {
			return i, true
		}
	}
	return 0, false
}

// ------------------------------------------------------------- logview ---

func buildLogview(c *cfg.Component, th theme.Theme) logview.Model {
	opts := th.Logview()
	opts.Title = c.Title
	opts.Searchable = c.Searchable
	opts.MaxLines = c.MaxLines
	opts.LineNumbers = c.LineNumbers
	opts.FilterMode = c.FilterMode
	if c.FilterPlaceholder != "" {
		opts.Filter.Placeholder = c.FilterPlaceholder
	}
	applyPaneColors(&opts.ActiveColor, &opts.InactiveColor, &opts.SpinnerStyle, c.Colors, th)
	if cs := c.Colors; cs != nil {
		if v := parseColor(cs.Match, th); v != nil {
			opts.MatchStyle = opts.MatchStyle.Foreground(v)
		}
		if v := parseColor(cs.CurrentLineBG, th); v != nil {
			opts.CurrentLineStyle = opts.CurrentLineStyle.Background(v)
		}
		if v := parseColor(cs.Gutter, th); v != nil {
			opts.GutterStyle = opts.GutterStyle.Foreground(v)
		}
	}
	m := logview.New(opts)
	if len(c.Lines) > 0 {
		m.AppendLines(staticLogLines(c, th))
	}
	if c.InitialQuery != "" {
		m.SetQuery(c.InitialQuery)
	}
	return m
}

// ------------------------------------------------------------ textview ---

func buildTextview(c *cfg.Component, th theme.Theme) textview.Model {
	opts := th.TextView()
	opts.Title = c.Title
	opts.Content = c.Content
	opts.Wrap = c.Wrap
	opts.Searchable = c.Searchable
	if c.FilterPlaceholder != "" {
		opts.Filter.Placeholder = c.FilterPlaceholder
	}
	applyPaneColors(&opts.ActiveColor, &opts.InactiveColor, &opts.SpinnerStyle, c.Colors, th)
	if cs := c.Colors; cs != nil {
		if v := parseColor(cs.Match, th); v != nil {
			opts.MatchStyle = opts.MatchStyle.Foreground(v)
		}
		if v := parseColor(cs.CurrentLineBG, th); v != nil {
			opts.CurrentLineStyle = opts.CurrentLineStyle.Background(v)
		}
	}
	m := textview.New(opts)
	if c.InitialQuery != "" {
		m.SetQuery(c.InitialQuery)
	}
	return m
}

// ---------------------------------------------------------------- tree ---

// yamlNode adapts a config.TreeNode into tuilib's tree.Node interface.
type yamlNode struct {
	label    string
	children []tree.Node
	// data is the source item a node was built from, for activity's
	// BusyWhen. nil for group headers and static nodes.
	data any
}

func (n *yamlNode) Label() string         { return n.label }
func (n *yamlNode) Children() []tree.Node { return n.children }

// sourceTreeRootLabel picks the display label for a source-bound tree's
// root node. Priority: explicit `root_label:` (templated), then Title,
// then the source name. The label doubles as the identity key tuilib's
// tree uses for expand-state preservation across SetRoot swaps — so it
// must be stable across refreshes. All three inputs are static (or
// templated once per push), which satisfies that.
func sourceTreeRootLabel(c *cfg.Component) string {
	switch {
	case c.RootLabel != "":
		return c.RootLabel
	case c.Title != "":
		return c.Title
	default:
		return c.Source
	}
}

func convertTree(n *cfg.TreeNode) tree.Node {
	if n == nil {
		return nil
	}
	out := &yamlNode{label: n.Label}
	for _, ch := range n.Children {
		if c := convertTree(ch); c != nil {
			out.children = append(out.children, c)
		}
	}
	return out
}

func buildTree(c *cfg.Component, th theme.Theme) tree.Model {
	opts := th.Tree()
	remapTreeKeys(&opts.Keys)
	opts.Title = c.Title
	if busy := busyFunc(c.Activity); busy != nil {
		opts.BusyWhen = func(n tree.Node) (string, bool) {
			if y, ok := n.(*yamlNode); ok && y.data != nil {
				return busy(y.data)
			}
			return "", false
		}
	}
	if rev := revisionFunc(c.Activity); rev != nil {
		opts.Revision = func(n tree.Node) string {
			if y, ok := n.(*yamlNode); ok && y.data != nil {
				return rev(y.data)
			}
			return ""
		}
	}
	opts.LoadingLabel = c.LoadingLabel
	opts.Searchable = c.Searchable
	opts.Markable = c.Markable
	opts.InitialDepth = c.InitialDepth
	if c.Source != "" {
		// Source-bound trees start empty until the first fetch fills
		// them via applyTree → SetRoot. We seed a labeled root plus a
		// single dummy child so InitialDepth's preExpand actually
		// fires (it early-returns when kids are empty). That leaves
		// m.expanded[<root-label>] = true. When SetRoot swaps in the
		// real tree, pruneExpanded keeps every reachable path and drops
		// the rest — the root's expanded entry survives because its
		// label matches, but the dummy child's entry gets pruned. Net
		// effect: on first data fill, the tree opens to its top level
		// automatically instead of showing a collapsed root the user
		// has to hit `E` to open.
		opts.Root = &yamlNode{
			label:    sourceTreeRootLabel(c),
			children: []tree.Node{&yamlNode{label: "__seed__"}},
		}
	} else {
		opts.Root = convertTree(c.Root)
	}
	if c.FilterPlaceholder != "" {
		opts.Filter.Placeholder = c.FilterPlaceholder
	}
	applyPaneColors(&opts.ActiveColor, &opts.InactiveColor, &opts.SpinnerStyle, c.Colors, th)
	if cs := c.Colors; cs != nil {
		if v := parseColor(cs.Match, th); v != nil {
			opts.MatchStyle = opts.MatchStyle.Foreground(v)
		}
		if v := parseColor(cs.CurrentLineBG, th); v != nil {
			opts.CurrentLineStyle = opts.CurrentLineStyle.Background(v)
		}
		if v := parseColor(cs.Mark, th); v != nil {
			opts.MarkStyle = opts.MarkStyle.Foreground(v)
		}
	}
	m := tree.New(opts)
	if c.InitialQuery != "" {
		m.SetQuery(c.InitialQuery)
	}
	if c.InitialCursor > 0 {
		m.SetCursor(c.InitialCursor)
	}
	return m
}

// staticListItems renders a static list's `items:` through its color
// rules.
func staticListItems(c *cfg.Component, th theme.Theme) []string {
	out := make([]string, len(c.Items))
	for i, item := range c.Items {
		out[i] = applyColorRules(item, c.ColorRules, th)
	}
	return out
}

// staticTableRows renders a static table's `rows:`. Bare-string cells go
// through column color_rules so hand-authored static tables can use the
// same rule language as data-bound ones. Mapping cells ({value, color} or
// {label, url}) keep their explicit styling — they're already declaring
// intent per cell.
func staticTableRows(c *cfg.Component, th theme.Theme) []table.Row {
	out := make([]table.Row, len(c.Rows))
	for i, row := range c.Rows {
		cells := make([]string, len(row))
		for j, v := range row {
			if s, ok := v.(string); ok && j < len(c.Columns) && len(c.Columns[j].ColorRules) > 0 {
				cells[j] = applyColorRules(s, c.Columns[j].ColorRules, th)
			} else {
				cells[j] = renderCell(v)
			}
		}
		out[i] = table.Row(cells)
	}
	return out
}

// staticLogLines renders a static logview's `lines:` through its color
// rules.
func staticLogLines(c *cfg.Component, th theme.Theme) []string {
	if len(c.ColorRules) == 0 {
		return c.Lines
	}
	out := make([]string, len(c.Lines))
	for i, ln := range c.Lines {
		out[i] = applyColorRules(ln, c.ColorRules, th)
	}
	return out
}

// ----------------------------------------------------- inspector ---

func convertFields(in []cfg.InspectorField, th theme.Theme) []inspector.Field {
	if len(in) == 0 {
		return nil
	}
	out := make([]inspector.Field, len(in))
	for i, f := range in {
		out[i] = inspector.Field{
			Label:    f.Label,
			Value:    applyColorRules(f.Value, f.ColorRules, th),
			Children: convertFields(f.Children, th),
		}
	}
	return out
}

func buildInspector(c *cfg.Component, th theme.Theme) inspector.Model {
	opts := th.Inspector()
	opts.Title = c.Title
	opts.Filterable = c.Filterable
	opts.InitialDepth = c.InitialDepth
	if c.Source == "" {
		opts.Fields = convertFields(c.Fields, th)
	}
	if c.FilterPlaceholder != "" {
		opts.Filter.Placeholder = c.FilterPlaceholder
	}
	applyPaneColors(&opts.ActiveColor, &opts.InactiveColor, &opts.SpinnerStyle, c.Colors, th)
	if cs := c.Colors; cs != nil {
		if v := parseColor(cs.Label, th); v != nil {
			opts.LabelStyle = opts.LabelStyle.Foreground(v)
		}
		if v := parseColor(cs.Value, th); v != nil {
			opts.ValueStyle = opts.ValueStyle.Foreground(v)
		}
		if v := parseColor(cs.Match, th); v != nil {
			opts.MatchStyle = opts.MatchStyle.Foreground(v)
		}
		if v := parseColor(cs.CurrentLineBG, th); v != nil {
			opts.CurrentLineStyle = opts.CurrentLineStyle.Background(v)
		}
	}
	m := inspector.New(opts)
	if c.InitialQuery != "" {
		m.SetQuery(c.InitialQuery)
	}
	if c.InitialCursor > 0 {
		m.SetCursor(c.InitialCursor)
	}
	return m
}

// ----------------------------------------------------- cell rendering ---

// renderCell converts a YAML cell value into the rendered string used by
// pkg/table. Plain strings pass through; mappings unlock styled cells:
//
//	{value: "ERROR", color: red}        ansi.CellColor
//	{label: "open", url: "https://…"}   ansi.Hyperlink
//
// Unknown shapes fall back to fmt.Sprint.
func renderCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case map[string]any:
		if url, ok := x["url"].(string); ok && url != "" {
			label := str(x["label"])
			if label == "" {
				label = url
			}
			return ansi.Hyperlink(url, label)
		}
		if c, ok := x["color"]; ok {
			text := str(x["value"])
			n := colorIndex(c)
			return ansi.CellColor(n, text)
		}
		return str(x["value"])
	default:
		return fmt.Sprint(v)
	}
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// colorIndex maps a named-or-numeric color spec to a 0-255 palette index.
// Named colors cover the standard 16; an unknown name returns -1, which
// CellColor passes through as plain text.
func colorIndex(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case string:
		s := strings.TrimSpace(x)
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
		return namedColor(strings.ToLower(s))
	default:
		return -1
	}
}

// applyPaneColors is the shared border + spinner overrides every
// pane-backed component (list/table/logview/tree/inspector) accepts.
func applyPaneColors(active, inactive *lipgloss.TerminalColor, spinner *lipgloss.Style, cs *cfg.Colors, th theme.Theme) {
	if cs == nil {
		return
	}
	if v := parseColor(cs.BorderActive, th); v != nil {
		*active = v
	}
	if v := parseColor(cs.BorderInactive, th); v != nil {
		*inactive = v
	}
	if v := parseColor(cs.Spinner, th); v != nil {
		*spinner = spinner.Foreground(v)
	}
}

// parseColor turns a config color spec into a lipgloss.TerminalColor.
// Supported forms:
//
//	""                  — no override
//	"red", "bright_*"   — named color
//	"160"               — 0-255 palette index
//	"#ff8800"           — hex
//	"theme:<token>"     — semantic token from the active theme; re-resolves
//	                      on every rebuild so cycling themes (`t`) updates
//	                      the override to the new palette's value.
//
// Recognised theme tokens: accent, current, muted, subtle, key,
// border-active, border-inactive, bar-bg, bar-fg, info-bg, info-fg,
// error-bg, error-fg.
//
// Returns nil when the spec is empty / unrecognised so callers can leave
// the theme default in place.
func parseColor(s string, th theme.Theme) lipgloss.TerminalColor {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "theme:") {
		return resolveThemeToken(strings.TrimPrefix(s, "theme:"), th)
	}
	if strings.HasPrefix(s, "#") {
		return lipgloss.Color(s)
	}
	n := colorIndex(s)
	if n < 0 {
		return nil
	}
	return lipgloss.Color(strconv.Itoa(n))
}

// colorIndexFromSpec resolves a color spec (named / 0-255 / theme:token)
// to a 0-255 palette index for use with pkg/ansi.CellColor, which only
// accepts indices. Returns -1 when the spec is empty, unrecognised, or
// resolves to a non-palette value (e.g. a hex color or a theme token
// whose color isn't a 0-255 index). Used for column separators and the
// header rule — both ANSI glyphs that need a palette index, not a
// lipgloss.TerminalColor.
func colorIndexFromSpec(s string, th theme.Theme) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1
	}
	if strings.HasPrefix(s, "theme:") {
		c := resolveThemeToken(strings.TrimPrefix(s, "theme:"), th)
		if c == nil {
			return -1
		}
		if v, ok := c.(lipgloss.Color); ok {
			if n, err := strconv.Atoi(string(v)); err == nil {
				return n
			}
		}
		return -1
	}
	return colorIndex(s)
}

func resolveThemeToken(name string, th theme.Theme) lipgloss.TerminalColor {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "accent":
		return th.Accent
	case "current":
		return th.Current
	case "muted":
		return th.Muted
	case "subtle":
		return th.Subtle
	case "key", "key-fg":
		return th.KeyFG
	case "border-active":
		return th.BorderActive
	case "border-inactive":
		return th.BorderInactive
	case "bar-bg":
		return th.BarBG
	case "bar-fg":
		return th.BarFG
	case "info-bg":
		return th.InfoBG
	case "info-fg":
		return th.InfoFG
	case "error-bg":
		return th.ErrorBG
	case "error-fg":
		return th.ErrorFG
	}
	return nil
}

func namedColor(name string) int {
	switch name {
	case "black":
		return 0
	case "red":
		return 1
	case "green":
		return 2
	case "yellow":
		return 3
	case "blue":
		return 4
	case "magenta":
		return 5
	case "cyan":
		return 6
	case "white":
		return 7
	case "gray", "grey", "bright_black":
		return 8
	case "bright_red":
		return 9
	case "bright_green":
		return 10
	case "bright_yellow":
		return 11
	case "bright_blue":
		return 12
	case "bright_magenta":
		return 13
	case "bright_cyan":
		return 14
	case "bright_white":
		return 15
	}
	return -1
}

// -------------------------------------------------------------- helpers ---

func parseAlign(s string) lipgloss.Position {
	switch s {
	case "right":
		return lipgloss.Right
	case "center":
		return lipgloss.Center
	default:
		return lipgloss.Left
	}
}

func parseLess(mode string) func(a, b string) bool {
	switch mode {
	case "number":
		return func(a, b string) bool { return parseNumber(a) < parseNumber(b) }
	case "si":
		return func(a, b string) bool { return parseSI(a) < parseSI(b) }
	}
	return nil
}

func parseNumber(s string) float64 {
	s = strings.TrimSpace(xansi.Strip(s))
	s = strings.ReplaceAll(s, ",", "")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// parseSI handles values like "9M", "8.3M", "130K", "1.2B" — case-insensitive
// K/M/B/G/T suffix. Unrecognized suffixes leave the multiplier at 1.
func parseSI(s string) float64 {
	s = strings.TrimSpace(xansi.Strip(s))
	if s == "" {
		return 0
	}
	mult := 1.0
	last := s[len(s)-1]
	switch last {
	case 'K', 'k':
		mult = 1e3
		s = s[:len(s)-1]
	case 'M', 'm':
		mult = 1e6
		s = s[:len(s)-1]
	case 'B', 'b', 'G', 'g':
		mult = 1e9
		s = s[:len(s)-1]
	case 'T', 't':
		mult = 1e12
		s = s[:len(s)-1]
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f * mult
}

// ------------------------------------------------------------- eventlog ---

// eventlogOptions is an eventlog's config and theme as tuilib options,
// shared by the stand-in NewComponent builds, the remote binding and its
// Restyle on a theme swap.
func eventlogOptions(c *cfg.Component, th theme.Theme) eventlog.Options {
	opts := th.Eventlog()
	opts.Title = c.Title
	opts.Searchable = c.Searchable
	opts.Filterable = c.Filterable
	opts.MaxItems = c.MaxItems
	opts.NoGutter = c.Gutter != nil && !*c.Gutter
	switch c.NewFor {
	case "":
	case "-":
		opts.NewFor = -1 // tuilib: negative never marks
	default:
		opts.NewFor, _ = time.ParseDuration(c.NewFor) // validated at load
	}
	if c.FilterPlaceholder != "" {
		opts.Filter.Placeholder = c.FilterPlaceholder
	}
	if cs := c.Colors; cs != nil {
		if v := parseColor(cs.BorderActive, th); v != nil {
			opts.ActiveColor = v
		}
		if v := parseColor(cs.BorderInactive, th); v != nil {
			opts.InactiveColor = v
		}
		if v := parseColor(cs.Spinner, th); v != nil {
			opts.SpinnerStyle = opts.SpinnerStyle.Foreground(v)
		}
		if v := parseColor(cs.Match, th); v != nil {
			opts.MatchStyle = opts.MatchStyle.Foreground(v)
		}
		if v := parseColor(cs.SelectedFG, th); v != nil {
			opts.SelectedStyle = opts.SelectedStyle.Foreground(v)
		}
		if v := parseColor(cs.SelectedBG, th); v != nil {
			opts.SelectedStyle = opts.SelectedStyle.Background(v)
		}
		if v := parseColor(cs.Cell, th); v != nil {
			opts.CellStyle = opts.CellStyle.Foreground(v)
		}
		if v := parseColor(cs.Gutter, th); v != nil {
			opts.GutterStyle = opts.GutterStyle.Foreground(v)
		}
		if v := parseColor(cs.New, th); v != nil {
			opts.NewStyle = opts.NewStyle.Foreground(v)
		}
	}
	return opts
}

// NewRemoteEventlog binds c to its windowed source through tuilib's
// pkg/remote. Eventlog keeps pointing at the binding's embedded model, so
// everything that reads the eventlog reads this one.
func NewRemoteEventlog(c *Component, th theme.Theme, shape remote.Shape[eventlog.Item]) {
	c.RemoteLog = remote.NewEventlog(eventlogOptions(c.Cfg, th), shape)
	c.Eventlog = &c.RemoteLog.Model
	// tuilib's eventlog starts out following the newest item, and stops
	// following by itself when an Anchored source is anchored at its
	// oldest. A Seekable log opens at the top unless `start: newest` asks
	// otherwise. A growing source turns following back on through
	// SetGrowing, whatever `start:` says.
	if _, anchored := shape.(remote.Anchored[eventlog.Item]); !anchored {
		c.Eventlog.SetFollow(c.Cfg.Start == "newest")
	}
}

// EventlogItems turns a page of source items into eventlog items: the key
// and mark plucked by dot-path, the text split into lines with the
// component's color rules applied per line, and the raw item kept as Data
// for enter and the inspector.
func EventlogItems(c *Component, items []any, th theme.Theme) []eventlog.Item {
	out := make([]eventlog.Item, len(items))
	for i, it := range items {
		var lines []string
		if text := ds.FirstString(it, c.Cfg.Text); text != "" {
			for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
				lines = append(lines, applyColorRules(line, c.Cfg.ColorRules, th))
			}
		}
		out[i] = eventlog.Item{
			Key:   ds.FirstString(it, c.Cfg.Key),
			Lines: lines,
			Data:  it,
			Mark:  ds.FirstString(it, c.Cfg.Mark),
		}
	}
	return out
}

// keySep joins an Anchored item's cursor to its component key in the key
// tuilib holds. tuilib treats an item's key as its cursor, so two items
// with the same cursor (Prefect logs stamped the same millisecond) would
// collapse into one; prefixing the component key keeps them apart.
// JSON never contains a raw unit separator, so the split is unambiguous.
const keySep = "\x1f"

// AnchoredKey is the key tuilib holds for an Anchored item.
func AnchoredKey(cursor, key string) string { return cursor + keySep + key }

// SplitAnchoredKey recovers the cursor and component key from an
// AnchoredKey. A key with no separator is a plain (Seekable) key.
func SplitAnchoredKey(k string) (cursor, key string) {
	if i := strings.Index(k, keySep); i >= 0 {
		return k[:i], k[i+len(keySep):]
	}
	return "", k
}

// LandAtNewest puts a span table anchored at its newest row on that row
// each time a new answer lands: the first page, and the first page of a
// re-anchor after a filter. Called after every message the remote table
// handles; it does nothing between answers, so scrolling is left alone.
func (c *Component) LandAtNewest() {
	if !c.AtNewest || c.Table == nil {
		return
	}
	a, ok := c.Table.Answered()
	if !ok || c.Table.Stale() {
		return
	}
	if key := a.Raw + "\x00" + a.Sort; key != c.landed {
		c.landed = key
		c.Table.SetCursor(len(c.Table.Rows()) - 1)
	}
}

// busyFunc turns an `activity:` block into the predicate tuilib's
// BusyWhen calls with each row's source item: the status at the path,
// and whether it means busy. nil when there's no activity.
func busyFunc(a *cfg.Activity) func(data any) (string, bool) {
	if a == nil {
		return nil
	}
	if a.BusyWhen != "" {
		// Validated at load, so it compiles.
		prog, _ := expr.Compile(a.BusyWhen)
		return func(data any) (string, bool) {
			busy, err := expr.EvalBool(prog, data)
			return ds.FirstString(data, a.Status), err == nil && busy
		}
	}
	values := make(map[string]bool, len(a.Busy))
	for _, v := range a.Busy {
		values[v] = true
	}
	return func(data any) (string, bool) {
		status := ds.FirstString(data, a.Status)
		return status, values[status]
	}
}

// revisionFunc reads `activity.revision:` off a row's source item, for
// tuilib's Revision. nil without one.
func revisionFunc(a *cfg.Activity) func(data any) string {
	if a == nil || len(a.Revision) == 0 {
		return nil
	}
	return func(data any) string { return ds.FirstString(data, a.Revision) }
}

// SetTitleSuffix redraws c's pane title as its configured title plus
// suffix ("" to clear), for state the screen reports on the border such
// as "· reads failing".
func SetTitleSuffix(c *Component, suffix string) {
	if c == nil || c.Cfg == nil {
		return
	}
	title := c.Cfg.Title + suffix
	switch c.Kind {
	case KList:
		c.List.SetTitle(title)
	case KTable:
		c.Table.SetTitle(title)
	case KLogview:
		c.Logview.SetTitle(title)
	case KTree:
		c.Tree.SetTitle(title)
	case KInspector:
		c.Inspector.SetTitle(title)
	case KTextview:
		c.Textview.SetTitle(title)
	}
}
