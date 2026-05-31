// Package config defines the YAML schema for a tui-builder TUI: an app block,
// a top-level components map keyed by name, and a screen whose layout tree
// references components by name. Each layout node is a tagged union —
// exactly one of vstack / hstack / zstack / component must be set. Items
// inside vstack/hstack carry a sizing hint (flex or fixed) and inline the
// same node fields.
package config

// Config is the top-level document. Either Screen (single-screen) or
// Screens + Initial (multi-screen) must be set, not both.
type Config struct {
	App         App                    `yaml:"app"`
	DataSources map[string]*DataSource `yaml:"data_sources,omitempty"`
	Components  map[string]*Component  `yaml:"components"`
	// Screen is the single-screen shorthand. Mutually exclusive with Screens.
	Screen Screen `yaml:"screen,omitempty"`
	// Screens is the multi-screen map keyed by name. Mutually exclusive
	// with Screen. Initial picks the root.
	Screens map[string]*Screen `yaml:"screens,omitempty"`
	// Initial names the root screen when Screens is used. Required iff
	// Screens is non-empty.
	Initial string `yaml:"initial,omitempty"`
}

// DataSource fetches data that one or more components bind to. v1 supports
// http only. ${selection.foo} tokens in URL / Headers / Body are
// substituted at push time (same machinery as static fields).
type DataSource struct {
	// Type selects the fetch mechanism. v1: "http".
	Type string `yaml:"type"`
	// URL is the request URL (http).
	URL string `yaml:"url,omitempty"`
	// Method defaults to GET (http).
	Method string `yaml:"method,omitempty"`
	// Headers are sent with the request (http).
	Headers map[string]string `yaml:"headers,omitempty"`
	// Body is the request body, sent as-is (http).
	Body string `yaml:"body,omitempty"`
	// Root is a dot-path into the response selecting the iterable root
	// for list/table bindings. Empty = response itself.
	Root string `yaml:"root,omitempty"`
	// Format selects how the response body is parsed:
	//   "" / "json"   parse as JSON, hand the typed value to bindings (default)
	//   "text"        keep the body as a raw string — required for logview
	//                 bindings against plain-text endpoints (e.g. kube pod logs)
	Format string `yaml:"format,omitempty"`
	// Refresh is the polling interval (e.g. "30s", "1m"). Empty = fetch
	// once on screen activate.
	Refresh string `yaml:"refresh,omitempty"`
	// Timeout overrides the default 10s request timeout.
	Timeout string `yaml:"timeout,omitempty"`
}

// App configures the surrounding tuilib app shell.
type App struct {
	// Title prefixes the breadcrumb (the screen title appears after it).
	Title string `yaml:"title,omitempty"`
	// Version renders on the right side of the statusbar.
	Version string `yaml:"version,omitempty"`
	// Theme names a built-in theme.Theme.Name to use as the initial palette.
	// Unknown names fall through to the first theme.
	Theme string `yaml:"theme,omitempty"`
	// HelpVerbose restores the legacy footer that tight-packs bindings
	// inline. Default (false) is minimal mode — the footer shows "? help"
	// and `?` opens the expanded panel.
	HelpVerbose bool `yaml:"help_verbose,omitempty"`
}

// Screen describes one screen — its breadcrumb title, its layout tree,
// and any on_enter bindings that push other screens.
type Screen struct {
	// Title shows in the breadcrumb. May contain ${selection} tokens
	// when this screen is reachable via an on_enter push.
	Title string `yaml:"title,omitempty"`
	// Layout is the root of the layout tree. Required.
	Layout Node `yaml:"layout"`
	// OnEnter declares which components, when enter is pressed on them
	// (and they're focused), push another screen. Multi-screen only.
	OnEnter []OnEnterBinding `yaml:"on_enter,omitempty"`
	// Actions hand a key off to a subprocess (kubectl exec, $EDITOR, open,
	// etc.) with the focused row's selection substituted into the argv.
	Actions []Action `yaml:"actions,omitempty"`
}

// Action binds a key to a subprocess executed via tuilib's pkg/runner.
// While the subprocess runs the TUI is suspended and the terminal is
// handed over to the subprocess (so kubectl exec, ssh, $EDITOR, etc.
// work as expected). Run argv elements support ${selection.*} (resolved
// against the focused list/table row at fire time) and ${env.*} (always).
type Action struct {
	// Key is the dispatch key. Common choices: "x", "d", "o", "e". Don't
	// collide with reserved keys (q, t, ?, tab, esc, enter, /, j, k, r).
	Key string `yaml:"key"`
	// Label appears in the help strip / panel.
	Label string `yaml:"label,omitempty"`
	// Source is the list or table component whose focused row's selection
	// is substituted into Run.
	Source string `yaml:"source"`
	// Run is the argv. Must be non-empty.
	Run []string `yaml:"run"`
	// Notice, when non-empty, is printed once after the TUI suspends and
	// before the subprocess starts — useful for slow handoffs ("connecting…").
	Notice string `yaml:"notice,omitempty"`
	// Confirm, when non-empty, shows a yes/no modal with this message
	// before dispatching the subprocess. ${selection.*}/${env.*} resolve
	// in the message the same way they do in Run. Yes runs the action,
	// No (or esc) dismisses the modal.
	Confirm string `yaml:"confirm,omitempty"`
	// Interactive controls how the subprocess is launched.
	//   true  (default): hand the TTY off via pkg/runner — needed for
	//                     vim, kubectl exec, ssh, htop, anything that
	//                     wants raw input or full-screen redraws.
	//                     The alt-screen suspends + resumes around the
	//                     subprocess (visible as a brief flicker).
	//   false: cmd.Run() in a goroutine, capture stdout+stderr, never
	//          suspend the alt-screen — right for non-interactive
	//          actions (`kubectl scale`, `kubectl delete`, `open URL`,
	//          one-shot scripts). Subprocess output appears in an alert
	//          on error, statusbar on success.
	Interactive *bool `yaml:"interactive,omitempty"`
}

// InteractiveDefault reports whether an action with no explicit
// Interactive field should run via pkg/runner. The default is true so
// the most common case (drop into a shell, edit a file) Just Works.
func (a Action) InteractiveDefault() bool {
	if a.Interactive == nil {
		return true
	}
	return *a.Interactive
}

// OnEnterBinding wires "enter on Source pushes Push." The source must be
// a list or table component referenced in this screen's layout; Push
// names a screen in Config.Screens. The source component's current
// selection becomes the ${selection} token in the pushed screen.
type OnEnterBinding struct {
	Source string `yaml:"source"`
	Push   string `yaml:"push"`
}

// Node is a tagged-union layout node. Exactly one of VStack / HStack /
// ZStack / Component must be non-empty. Component is the name of a
// component defined in Config.Components.
type Node struct {
	VStack    []Item  `yaml:"vstack,omitempty"`
	HStack    []Item  `yaml:"hstack,omitempty"`
	ZStack    *ZStack `yaml:"zstack,omitempty"`
	Component string  `yaml:"component,omitempty"`
}

// Item is a child of a vstack or hstack. It carries a sizing hint (flex or
// fixed) plus an inlined Node. Exactly one of Flex / Fixed should be set;
// when both are zero, Flex=1 is assumed.
type Item struct {
	Flex  int `yaml:"flex,omitempty"`
	Fixed int `yaml:"fixed,omitempty"`
	Node  `yaml:",inline"`
}

// ZStack overlays Overlay on top of Base. Both fill the parent rect.
type ZStack struct {
	Base    Node `yaml:"base"`
	Overlay Node `yaml:"overlay"`
}

// Component is a leaf node — one of the supported tuilib components: list,
// table, logview, tree, inspector. Most fields are kind-specific; the
// validator rejects mismatched combinations.
type Component struct {
	// Type selects the component kind: list | table | logview | tree |
	// inspector.
	Type string `yaml:"type"`
	// Title sits on the component's pane border.
	Title string `yaml:"title,omitempty"`
	// Filterable enables the embedded '/' filter on list/table/inspector.
	// (For logview/tree, use Searchable.)
	Filterable bool `yaml:"filterable,omitempty"`
	// FilterPlaceholder is the empty-state hint inside the filter input.
	FilterPlaceholder string `yaml:"filter_placeholder,omitempty"`
	// InitialFilter pre-populates the filter value (and applies it).
	InitialFilter string `yaml:"initial_filter,omitempty"`
	// InitialCursor places the cursor at a specific row index on startup.
	InitialCursor int `yaml:"initial_cursor,omitempty"`

	// Source names a data source this component is bound to. When set,
	// the static Items/Rows/Fields are ignored and the component is
	// populated by the source's response after each fetch. Use Item
	// (list), per-column Value (table), or per-field Path (inspector) to
	// map from response shape to component shape.
	Source string `yaml:"source,omitempty"`
	// Item is the dot-path used by a list bound to a data source to pluck
	// the display string for each element of the iterable root.
	Item string `yaml:"item,omitempty"`

	// Colors overrides individual theme tokens on this component. Unset
	// fields fall through to the theme. Color values accept named colors
	// (red, green, gray, bright_red, ...), 0-255 palette indices ("160"),
	// or hex strings ("#ff8800").
	Colors *Colors `yaml:"colors,omitempty"`

	// list fields
	Items []string `yaml:"items,omitempty"`

	// table fields
	Columns     []Column `yaml:"columns,omitempty"`
	Rows        [][]any  `yaml:"rows,omitempty"`
	InitialSort *Sort    `yaml:"initial_sort,omitempty"`

	// logview fields
	Lines      []string `yaml:"lines,omitempty"`
	Searchable bool     `yaml:"searchable,omitempty"`
	MaxLines   int      `yaml:"max_lines,omitempty"`
	FilterMode bool     `yaml:"filter_mode,omitempty"`
	// InitialQuery pre-populates the search query on logview / tree.
	InitialQuery string `yaml:"initial_query,omitempty"`

	// tree fields
	Root *TreeNode `yaml:"root,omitempty"`

	// inspector fields
	Fields []InspectorField `yaml:"fields,omitempty"`

	// shared hierarchical fields (tree, inspector). InitialDepth pre-
	// expands every node whose depth is < InitialDepth: 0 = root only,
	// 1 = root expanded, 2 = root + first level, …
	InitialDepth int `yaml:"initial_depth,omitempty"`
}

// Sort declares an initial table sort. Column may be a column title
// (case-insensitive prefix match) or a 1-based column number; the column
// must be Sortable.
type Sort struct {
	Column string `yaml:"column"`
	Desc   bool   `yaml:"desc,omitempty"`
}

// Column declares one table column. Width sizing modes mirror table.Column:
// Width>0 fixed, Width==0 content-auto, Flex>0 expands.
type Column struct {
	Title    string `yaml:"title"`
	Width    int    `yaml:"width,omitempty"`
	Flex     int    `yaml:"flex,omitempty"`
	MaxWidth int    `yaml:"max_width,omitempty"`
	Align    string `yaml:"align,omitempty"` // left | right | center
	Sortable bool   `yaml:"sortable,omitempty"`
	// Sort picks the comparator when Sortable is true.
	//   "" / "string"  case-insensitive lex on the ANSI-stripped cell (default)
	//   "number"       strconv.ParseFloat after stripping commas
	//   "si"           number + K/M/B/G/T suffix (1K=1e3, 1M=1e6, …)
	Sort string `yaml:"sort,omitempty"`
	// Value is the dot-path used by a data-source-bound table to pluck
	// the cell from each iterable-root element (e.g. "name.common").
	Value string `yaml:"value,omitempty"`
}

// TreeNode is a node in a tree component's root. Children may be empty
// for leaves. Cycle detection is not performed — define a DAG only.
type TreeNode struct {
	Label    string      `yaml:"label"`
	Children []*TreeNode `yaml:"children,omitempty"`
}

// InspectorField is one entry in an inspector component — a label / value
// pair with optional nested children that render as an expandable subtree.
// Pass an empty Value when the field is just a header for its Children.
// When the inspector is data-source-bound, Path overrides Value: the
// resolved dot-path into the source response becomes the displayed value.
type InspectorField struct {
	Label    string           `yaml:"label"`
	Value    string           `yaml:"value,omitempty"`
	Path     string           `yaml:"path,omitempty"`
	Children []InspectorField `yaml:"children,omitempty"`
}

// Colors is the per-component palette override. Each field maps to one
// tuilib component Options field; only fields the component understands
// are consulted (e.g. Header/SelectedBG apply to tables, Label/Value
// apply to inspectors). Unset fields fall through to the theme.
type Colors struct {
	// All panes:
	BorderActive   string `yaml:"border_active,omitempty"`
	BorderInactive string `yaml:"border_inactive,omitempty"`
	Spinner        string `yaml:"spinner,omitempty"`

	// list:
	Selected string `yaml:"selected,omitempty"`

	// table:
	SelectedFG      string `yaml:"selected_fg,omitempty"`
	SelectedBG      string `yaml:"selected_bg,omitempty"`
	Header          string `yaml:"header,omitempty"`
	Cell            string `yaml:"cell,omitempty"`
	ColumnSeparator string `yaml:"column_separator,omitempty"`
	HeaderRule      string `yaml:"header_rule,omitempty"`

	// inspector:
	Label string `yaml:"label,omitempty"`
	Value string `yaml:"value,omitempty"`

	// inspector / logview / tree:
	Match         string `yaml:"match,omitempty"`
	CurrentLineBG string `yaml:"current_line_bg,omitempty"`
}
