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

// DataSource fetches data that one or more components bind to. Supported
// `type:` values: http, exec, file, merge. ${selection.*} and ${env.*}
// tokens substitute at push time across most string fields.
//
// Per-type field reference:
//
//	http   url, method, headers, body, format, root, refresh, timeout
//	exec   command, env, format, root, refresh, timeout
//	file   path, format, root, refresh
//	merge  sources, tag_field, on_error, refresh
type DataSource struct {
	// Type selects the fetch mechanism.
	Type string `yaml:"type"`

	// Shared by http / exec / file / merge.
	// Root is a dot-path into the response selecting the iterable root
	// for list/table bindings. Empty = response itself.
	Root string `yaml:"root,omitempty"`
	// Refresh is the polling interval (e.g. "30s", "1m"). Empty = fetch
	// once on screen activate. Driven by tea.Tick.
	Refresh string `yaml:"refresh,omitempty"`
	// Timeout caps per-fetch latency. Default 10s for http/exec, n/a
	// for file (synchronous read) and merge (defers to children).
	Timeout string `yaml:"timeout,omitempty"`
	// Format selects how the response body is parsed:
	//   "" / "json"   parse as JSON, hand the typed value to bindings (default)
	//   "text"        keep the body as a raw string — required for logview
	//                 bindings against plain-text endpoints (e.g. kube pod logs)
	Format string `yaml:"format,omitempty"`

	// http fields.
	URL     string            `yaml:"url,omitempty"`
	Method  string            `yaml:"method,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty"`

	// websocket fields.
	// InitialMessages are text frames sent immediately after the
	// connection upgrades — useful for protocols (bitstamp, Kraken,
	// Coinbase, many custom buses) that require a subscribe handshake
	// before the server starts emitting. ${selection.*} / ${env.*}
	// substitute per entry. Sent in order, fire-and-forget; failures
	// don't terminate the stream but do appear as one error event.
	InitialMessages []string `yaml:"initial_messages,omitempty"`

	// exec fields.
	// Command is the argv ([cmd, arg, arg, ...]). The first element is
	// looked up in $PATH; subsequent elements are passed as-is.
	// ${selection.*} and ${env.*} substitute per element.
	Command []string `yaml:"command,omitempty"`
	// Env adds (or overrides) environment variables on top of the
	// process's own environment. ${env.*} can reference outer env;
	// ${selection.*} substitutes in values.
	Env map[string]string `yaml:"env,omitempty"`
	// Follow turns exec into a streaming source: the subprocess is
	// started (not waited on), its stdout is read line-by-line, and
	// each line is delivered as an Event to a bound logview. Use for
	// `kubectl logs -f`, `tail -f`, `journalctl -f`, anything that
	// emits a continuous line stream. Refresh is ignored when Follow
	// is true (the stream is the refresh).
	Follow bool `yaml:"follow,omitempty"`

	// file fields.
	// Path is the file to read. ${selection.*} / ${env.*} substitute.
	Path string `yaml:"path,omitempty"`

	// merge fields.
	// Sources is the list of source names whose results are unioned. The
	// referenced sources are built independently; merge resolves and
	// fetches them concurrently each refresh.
	Sources []string `yaml:"sources,omitempty"`
	// TagField, when set, injects {<TagField>: <child-source-name>}
	// into every map-shaped item from each child so a downstream column
	// or list_item path can identify which source the item came from.
	// Non-map items pass through untouched.
	TagField string `yaml:"tag_field,omitempty"`
	// OnError chooses what happens when a child source errors during a
	// merge fetch:
	//   "" / "fail" (default) — any child error aborts the merge
	//   "skip"                — drop the failed child, return the rest
	//                            (only errors if EVERY child fails)
	OnError string `yaml:"on_error,omitempty"`
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

	// ColorRules drive data-aware coloring for components with a single
	// value stream — list items and logview lines. (For table, rules
	// live per Column; for inspector, per InspectorField.) Rules
	// evaluate in order, first match wraps the rendered text with
	// ansi.CellColor.
	ColorRules []ColorRule `yaml:"color_rules,omitempty"`

	// list fields
	Items []string `yaml:"items,omitempty"`

	// table fields
	Columns     []Column `yaml:"columns,omitempty"`
	Rows        [][]any  `yaml:"rows,omitempty"`
	InitialSort *Sort    `yaml:"initial_sort,omitempty"`
	// MaxRows caps the table when bound to a streaming source — each
	// arriving JSON frame is prepended as a new row, oldest rows
	// dropped past this size. 0 (default) implies 100 for streaming
	// tables (unbounded growth would eat memory); ignored for
	// non-streaming bindings and for keyed-upsert mode (see RowKey).
	// Set explicitly to override or to -1 for truly unbounded.
	MaxRows int `yaml:"max_rows,omitempty"`
	// RowKey turns a streaming-bound table into a keyed-upsert view —
	// the L1 order-book / status-table / "one row per X" pattern. The
	// dot-path picks a key out of each event; when an event arrives
	// whose key matches an existing row, that row is updated in
	// place (cursor stays put). When the key is new, the row appends.
	// Without RowKey, streaming events prepend to a ring buffer
	// (live-tape pattern). MaxRows is ignored in keyed mode — row
	// count is naturally bounded by the number of distinct keys.
	RowKey Path `yaml:"row_key,omitempty"`

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
	// Value is the dot-path (or fallback chain of dot-paths) used by a
	// data-source-bound table to pluck the cell from each iterable-root
	// element. A scalar string ("name.common") is the common case; a
	// list of strings is tried in order and the first non-empty result
	// wins — useful for kube-style computed fields where the
	// authoritative value lives under different keys depending on
	// state (e.g. container waiting reason → terminated reason →
	// pod phase).
	Value Path `yaml:"value,omitempty"`
	// ColorRules apply data-driven coloring per cell in this column.
	// Rules are evaluated in order; the first match wraps the cell value
	// with ansi.CellColor (preserves the selected-row background). When
	// no rule matches the cell is rendered plain. See ColorRule for the
	// `when:` syntax.
	ColorRules []ColorRule `yaml:"color_rules,omitempty"`
}

// ColorRule pairs a `when:` matcher with a `color:`. Recognised `when:`
// syntax: exact case-insensitive string, "~regex", numeric comparison
// ("> 5", "<= 10", "== 0", "!= 0", with optional K/M/B/G/T suffix on
// the right-hand side), or empty (always — useful as a terminal default
// rule).
type ColorRule struct {
	When  string `yaml:"when,omitempty"`
	Color string `yaml:"color"`
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
	Label string `yaml:"label"`
	Value string `yaml:"value,omitempty"`
	Path  string `yaml:"path,omitempty"`
	// ColorRules wrap this field's rendered value with ansi.CellColor
	// when a rule matches. Same syntax as Column.ColorRules.
	ColorRules []ColorRule      `yaml:"color_rules,omitempty"`
	Children   []InspectorField `yaml:"children,omitempty"`
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
