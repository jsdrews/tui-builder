// Package config defines the YAML schema for a tui-builder TUI: an app block,
// a top-level components map keyed by name, and a screen whose layout tree
// references components by name. Each layout node is a tagged union —
// exactly one of vstack / hstack / zstack / component must be set. Items
// inside vstack/hstack carry a sizing hint (flex or fixed) and inline the
// same node fields.
package config

// Config is the top-level document. Layered into three blocks that
// match the architectural boundary enforced in code:
//
//   - `app:`  — config-wide metadata (title, etc.).
//   - `data:` — the data layer. Sources + pipelines live here. This
//     block is completely independent of any TUI — wrangl
//     reads only `app:` + `data:` and never touches `tui:`.
//   - `tui:`  — the presentation layer. Components and screens live
//     here. References data by name but doesn't define it.
//
// Either Tui.Screen (single-screen) or Tui.Screens + Tui.Initial
// (multi-screen) must be set, not both.
type Config struct {
	App  App       `yaml:"app"`
	Data DataBlock `yaml:"data,omitempty"`
	TUI  TUIBlock  `yaml:"tui,omitempty"`
}

// DataBlock holds the data-layer definitions. Every entry under
// `data.sources:` is a *Source whose `type:` field picks its kind.
// Both leaf kinds (http / exec / file / websocket / static / merge)
// and operator kinds (passthrough / filter / project / derive / sort
// / union / compose / join / cache) share the single map and are
// addressable by name from `tui.components` and from wrangl.
type DataBlock struct {
	// Sources is the unified data map. Every entry is a *Source
	// whose `type:` field discriminates its kind (one of the leaf
	// kinds — http / exec / file / websocket / static / merge — or
	// operator kinds — passthrough / filter / project / derive / sort
	// / union / compose / join / cache). Each kind reads only the
	// fields it cares about; the others are silently ignored.
	Sources map[string]*Source `yaml:"sources,omitempty"`
}

// TUIBlock holds the presentation-layer definitions: components and
// screens. Components reference data by name (source or pipeline)
// but never define data themselves — this is the data-layer / TUI-
// layer boundary expressed in the YAML schema.
type TUIBlock struct {
	Components map[string]*Component `yaml:"components,omitempty"`
	// Screen is the single-screen shorthand. Mutually exclusive with Screens.
	Screen Screen `yaml:"screen,omitempty"`
	// Screens is the multi-screen map keyed by name. Mutually exclusive
	// with Screen. Initial picks the root.
	Screens map[string]*Screen `yaml:"screens,omitempty"`
	// Initial names the root screen when Screens is used. Required iff
	// Screens is non-empty.
	Initial string `yaml:"initial,omitempty"`
}






// JoinDriver names the iterable whose rows seed the join.
type JoinDriver struct {
	// From references a source or pipeline that returns an iterable.
	From string `yaml:"from"`
}

// JoinLookup names a per-row fetch and how to derive its params from
// the driver row.
type JoinLookup struct {
	// From references a data source (NOT a pipeline) whose
	// `parameters:` block enumerates what it needs to run.
	From string `yaml:"from"`
	// On maps the lookup source's parameter names → expressions
	// evaluated against the driver row. Expressions use the same
	// language as filter / derive / sort / project. Common forms:
	//
	//   on:
	//     namespace: metadata.namespace
	//     name:      metadata.name
	//     port:      "1000 + spec.containerPort"
	On map[string]string `yaml:"on"`
}




// PaginateConfig controls multi-page walking on an http source. Only
// meaningful when the source's `type:` is `http` and Format is JSON
// (not text). Set via `paginate:` in YAML.
//
// Strategy semantics:
//
//   - "link": the response body carries a URL for the next page at
//     dot-path NextPath. Django REST Framework does this — every
//     response has `{next: "https://...?page=2", ...}`. AWX, GitLab
//     (some endpoints), and many other REST APIs use this shape.
//     Walk: fetch → apply Root: to get items → follow NextPath →
//     repeat until next is null / missing / empty string.
//
// Additional strategies (link_header for GitHub, cursor for Stripe,
// offset for manual paging) can layer on later behind the same
// discriminator without breaking configs.
type PaginateConfig struct {
	// Strategy picks the page-walking method. Required; must be one
	// of the values enumerated above.
	Strategy string `yaml:"strategy"`
	// NextPath is the dot-path into the RAW response (pre-Root
	// slicing) that carries the next page's URL. Required for
	// strategy "link". Common values: "next", "links.next",
	// "meta.pagination.next".
	NextPath string `yaml:"next_path,omitempty"`
	// MaxPages caps the walk. Default 20. Zero means unlimited (not
	// recommended — one runaway API can OOM the process).
	MaxPages int `yaml:"max_pages,omitempty"`
	// OnPageError chooses what happens when a mid-walk page fails:
	//   "" / "fail" (default) — abort, return the error
	//   "skip"                — return the accumulated pages so far
	//                            (subsequent pages simply omitted)
	OnPageError string `yaml:"on_page_error,omitempty"`
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
	// Prompts collected at boot, before any screen renders. Each
	// prompt's Key becomes an env var (set via os.Setenv) whose value
	// is whatever the user typed / picked, so the existing
	// ${env.<KEY>} substitution covers BOTH OS env vars and these
	// boot-time params. Cancel from the form aborts the program.
	//
	// Use for: which symbols to watch (URL param), which cluster to
	// hit (URL host), which flags to pass to a CLI source (exec
	// argv). Anything that's "configure at startup, then constant."
	//
	// Pre-populating: if the env var named by a prompt's Key is
	// already set, the prompt's input is pre-filled with that value —
	// so `SYMBOLS=btcusdt,ethusdt tui-builder ...` lets you skip the
	// modal entirely.
	Prompts []Prompt `yaml:"prompts,omitempty"`

	// Env declares environment variables the config depends on.
	// Load-time behavior:
	//   - `required: true` + unset (and no default) → hard error at
	//     Load with a message naming every missing var at once so
	//     the user fixes them in one edit rather than one-at-a-time.
	//   - `default:` + unset → os.Setenv applied so downstream
	//     ${env.X} substitution picks up the default value. Same
	//     semantics as app.prompts defaults.
	//   - Referenced-but-undeclared `${env.X}` in a URL / Command /
	//     Header / Body / Action.Run / etc. → stderr warning at
	//     Load. Not a hard error because empty-string substitution
	//     is a legitimate pattern for some fields (optional
	//     headers, feature-flag env vars).
	//
	// Purpose: catch "I forgot to export AWX_TOKEN" at load time
	// with a clear message, rather than at first fetch with a
	// cryptic 401 or a double-slash URL.
	Env []EnvSpec `yaml:"env,omitempty"`
}

// EnvSpec declares one environment variable dependency. Same shape
// as Parameter (required + default + description) but scoped to
// process-level env rather than per-source parameters.
type EnvSpec struct {
	// Name is the env var name (e.g. AWX_TOKEN). Required.
	Name string `yaml:"name"`
	// Required, when true, makes Load fail hard if the var is unset
	// in the environment AND no default is given. Mutually exclusive
	// with Default (default implies optional).
	Required bool `yaml:"required,omitempty"`
	// Default is applied via os.Setenv when the var is unset in the
	// environment at Load time. Mutually exclusive with Required.
	Default string `yaml:"default,omitempty"`
	// Description surfaces in the missing-required error message so
	// the user knows what to set the var to. Optional but strongly
	// recommended — a good description turns a cryptic failure into
	// a self-serve fix.
	Description string `yaml:"description,omitempty"`
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
	// Prompts collects user input before the action dispatches. Each
	// prompt becomes a form field; on submit, values are exposed as
	// ${prompt.<key>} tokens substituted into Run argv, Confirm
	// message, and Notice. Order: prompts → confirm (with substituted
	// preview) → dispatch. Cancel from the form aborts the action.
	Prompts []Prompt `yaml:"prompts,omitempty"`
}

// MergeChild names a child source plus the tags merge should inject
// into every row that originated from that child. Tags are key/value
// strings written at the top level of each map-shaped item — same
// substrate as the legacy TagField but with arbitrary keys and values
// instead of one literal source-name.
//
// Order in the parent `children:` list determines child fetch order
// (mirrors the legacy `sources:` order), so deterministic UIs that
// depend on row order get the same shape under both forms.
type MergeChild struct {
	// Source is the name of a data source defined elsewhere in
	// the config. Required.
	Source string `yaml:"source"`
	// Tags are injected into every map-shaped row produced by this
	// child. Existing keys on the row survive — tagging is purely
	// additive. Non-map rows (scalars, arrays) pass through
	// untouched, same as TagField.
	Tags map[string]string `yaml:"tags,omitempty"`
}

// Parameter declares one typed input slot on a data source. Callers
// bind values; the source references them with ${params.<name>}.
//
// Today's POC schema is minimal — `type` is informational (`string`
// covers all current uses); `required` + `default` are mutually
// exclusive (a default makes a param effectively optional). Validation
// rules, complex types, and computed defaults are deferred until a
// concrete need surfaces.
type Parameter struct {
	// Type is informational today: string (default), int, bool, duration.
	// Future use: form widget selection at TUI binding sites, basic
	// validation in wrangl (--param port=abc against type:int rejects).
	Type string `yaml:"type,omitempty"`
	// Required means callers MUST bind a value before the source can
	// run. Mutually exclusive with Default.
	Required bool `yaml:"required,omitempty"`
	// Default is the value used when no caller supplies one. Setting
	// Default implies the param is optional.
	Default string `yaml:"default,omitempty"`
	// Description shows up in --list / launcher prompts / future
	// --help output. One-line summary.
	Description string `yaml:"description,omitempty"`
}

// Prompt is one field in an action's input form. Types map 1:1 to
// tuilib pkg/form field kinds:
//
//	text    (default) — single-line text input
//	select  — pick one of `options`
//	confirm — yes/no toggle (value is "true" / "false")
type Prompt struct {
	Key         string   `yaml:"key"`
	Label       string   `yaml:"label,omitempty"`
	Type        string   `yaml:"type,omitempty"`          // text | select | confirm
	Placeholder string   `yaml:"placeholder,omitempty"`   // text only
	Initial     string   `yaml:"initial,omitempty"`       // text default value
	Options     []string `yaml:"options,omitempty"`       // select choices
	InitialIdx  int      `yaml:"initial_index,omitempty"` // select default
	InitialBool bool     `yaml:"initial_bool,omitempty"`  // confirm default
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
//
// Bind maps destination-screen parameter names to templates evaluated
// against the focused row's Selection. Use this when the destination
// screen has data sources that declare `parameters:` — the values
// resolve at push time and feed into each parameterized source's
// BindParams call. Without Bind, parameterized sources on the
// destination won't have their required params filled and will error
// at fetch (or push, depending on how strict we make it).
//
//	bind:
//	  namespace: ${selection.Namespace}
//	  name:      ${selection.Name}
//
// Values support the same ${selection.*} / ${env.*} / ${prompt.*}
// substitutions as everywhere else.
type OnEnterBinding struct {
	Source string            `yaml:"source"`
	Push   string            `yaml:"push"`
	Bind   map[string]string `yaml:"bind,omitempty"`
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

	// Source names the data entry this component is bound to. The
	// entry can be any kind (leaf source or pipeline operator) —
	// after the sources/pipelines unification, components don't
	// distinguish between them at the schema level. When Source is
	// set, the static Items/Rows/Fields are ignored and the
	// component is populated by the entry's response after each
	// fetch. Use Item (list), per-column Value (table), or per-field
	// Path (inspector) to map from response shape to component
	// shape.
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
