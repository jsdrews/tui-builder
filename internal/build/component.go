// Package build constructs live tuilib components and a layout.Node tree
// from the YAML config. The output is owned by the screen, which holds the
// component pointers for state preservation across theme swaps.
package build

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/ansi"
	"github.com/jsdrews/tuilib/pkg/inspector"
	"github.com/jsdrews/tuilib/pkg/list"
	"github.com/jsdrews/tuilib/pkg/logview"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"
	"github.com/jsdrews/tuilib/pkg/tree"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Kind enumerates the supported leaf component types.
type Kind int

const (
	KList Kind = iota
	KTable
	KLogview
	KTree
	KInspector
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

	// StreamRows is the ring buffer used by KTable components bound to
	// a streaming source. Newest events first; trimmed to Cfg.MaxRows
	// (defaulted to 100 for streaming bindings) on every append. nil
	// for non-streaming tables and non-table kinds.
	StreamRows []any
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
	}
	return nil, fmt.Errorf("unknown component type %q", c.Type)
}

// Rebuild reconstructs the component against a new theme, preserving as
// much in-flight state as the component exposes accessors for.
func (c *Component) Rebuild(th theme.Theme) {
	switch c.Kind {
	case KList:
		cursor, value := c.List.Cursor(), c.List.Value()
		m := buildList(c.Cfg, th)
		if value != "" {
			m.SetValue(value)
		}
		m.SetCursor(cursor)
		*c.List = m
	case KTable:
		cursor := c.Table.Cursor()
		value := c.Table.Value()
		sortCol, sortDesc := c.Table.SortColumn(), c.Table.SortDescending()
		m := buildTable(c.Cfg, th)
		m.SetValue(value)
		m.SetCursor(cursor)
		m.SetSort(sortCol, sortDesc)
		*c.Table = m
	case KLogview:
		lines := append([]string(nil), c.Logview.Lines()...)
		query := c.Logview.Query()
		filterMode := c.Logview.FilterMode()
		m := buildLogview(c.Cfg, th)
		m.Clear()
		m.AppendLines(lines)
		if query != "" {
			m.SetQuery(query)
		}
		m.SetFilterMode(filterMode)
		*c.Logview = m
	case KTree:
		cursor := c.Tree.Cursor()
		query := c.Tree.Query()
		filterMode := c.Tree.FilterMode()
		m := buildTree(c.Cfg, th)
		if query != "" {
			m.SetQuery(query)
		}
		m.SetFilterMode(filterMode)
		m.SetCursor(cursor)
		*c.Tree = m
	case KInspector:
		cursor := c.Inspector.Cursor()
		query := c.Inspector.Query()
		filterMode := c.Inspector.FilterMode()
		m := buildInspector(c.Cfg, th)
		if query != "" {
			m.SetQuery(query)
		}
		m.SetFilterMode(filterMode)
		m.SetCursor(cursor)
		*c.Inspector = m
	}
}

// ---------------------------------------------------------------- list ---

func buildList(c *cfg.Component, th theme.Theme) list.Model {
	opts := th.List()
	opts.Title = c.Title
	if c.Source == "" {
		opts.Items = make([]string, len(c.Items))
		for i, item := range c.Items {
			opts.Items[i] = applyColorRules(item, c.ColorRules, th)
		}
	}
	opts.Filterable = c.Filterable
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
			opts.SelectedColor = v
		}
		if v := parseColor(cs.Spinner, th); v != nil {
			opts.SpinnerStyle = opts.SpinnerStyle.Foreground(v)
		}
	}
	m := list.New(opts)
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
	opts := th.Table()
	opts.Title = c.Title
	opts.Filterable = c.Filterable
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
		}
	}
	if c.Source == "" {
		opts.Rows = make([]table.Row, len(c.Rows))
		for i, row := range c.Rows {
			cells := make([]string, len(row))
			for j, v := range row {
				// Bare-string cells go through column color_rules so
				// hand-authored static tables can use the same rule
				// language as data-bound ones. Mapping cells
				// ({value, color} or {label, url}) keep their explicit
				// styling — they're already declaring intent per cell.
				if s, ok := v.(string); ok && j < len(c.Columns) && len(c.Columns[j].ColorRules) > 0 {
					cells[j] = applyColorRules(s, c.Columns[j].ColorRules, th)
				} else {
					cells[j] = renderCell(v)
				}
			}
			opts.Rows[i] = table.Row(cells)
		}
	}
	m := table.New(opts)
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
	return m
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
	}
	m := logview.New(opts)
	if len(c.Lines) > 0 {
		lines := c.Lines
		if len(c.ColorRules) > 0 {
			lines = make([]string, len(c.Lines))
			for i, ln := range c.Lines {
				lines[i] = applyColorRules(ln, c.ColorRules, th)
			}
		}
		m.AppendLines(lines)
	}
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
}

func (n *yamlNode) Label() string      { return n.label }
func (n *yamlNode) Children() []tree.Node { return n.children }

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
	opts.Title = c.Title
	opts.Searchable = c.Searchable
	opts.InitialDepth = c.InitialDepth
	opts.Root = convertTree(c.Root)
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
	m := tree.New(opts)
	if c.InitialQuery != "" {
		m.SetQuery(c.InitialQuery)
	}
	if c.InitialCursor > 0 {
		m.SetCursor(c.InitialCursor)
	}
	return m
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
