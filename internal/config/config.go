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
	// Actions is the write side: named units of work bound to keys by
	// `tui.screens.*.actions`. Peer to `data.sources:` rather than an
	// entry in it — see action.go for why a mutation must never live in
	// the polled source graph.
	//
	// Inline action declarations are hoisted into this map at load time,
	// so post-Load it holds every action the config can perform
	// regardless of how it was written.
	Actions map[string]*Action `yaml:"actions,omitempty"`

	// envSubstituted records that SubstituteEnv has already run, so a
	// second call is a no-op rather than a second pass over values that
	// are now data rather than templates. See SubstituteEnv.
	envSubstituted bool
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

// WindowConfig opts an http source into *windowed* paging — the lazy
// counterpart to PaginateConfig. Where `paginate:` walks every page up
// front and hands back one flat list, `window:` fetches only the rows
// the user is looking at, and re-fetches when they scroll or filter.
//
// The difference matters at scale: `paginate:` over a 30,000-row endpoint
// means 300 requests before the first frame. `window:` means one request
// for the first 100 rows, and one more each time the user scrolls past
// what's loaded. It also means the *server* answers the filter, so
// "author:tolkien" searches all 30,000 rows rather than the 100 that
// happen to be resident.
//
// Supported on `type: http` and `type: exec`. The two carry the request
// differently, and the fields split accordingly:
//
//   - http templates it into the query string, so it names query
//     parameters: OffsetParam / LimitParam / SearchParam / SortParam,
//     with Filters mapping a column title to a parameter.
//   - exec templates it into the argv via ${window.offset},
//     ${window.limit}, ${window.search}, ${window.sort},
//     ${window.sort_dir}, and ${window.filters.<name>} — so the *Param
//     fields don't apply, and Filters maps a column title to the token
//     suffix instead.
//
// PageSize, Prefetch, TotalPath, and Filters are shared; validation
// rejects the fields that can't apply to the kind in use rather than
// ignoring them, since a silently-ignored offset_param means the
// command returns page one forever.
//
// A source with `window:` set may only be bound to a `type: table`
// component — table is the only component that can hold a sparse window
// (tuilib's table.SetWindow). Binding it elsewhere is a config error.
//
// The bound table switches to remote filtering and sorting
// automatically: the filter the user types is reported to this source
// rather than applied to the rows on screen, because filtering one page
// of a larger set isn't filtering.
type WindowConfig struct {
	// PageSize is how many rows one request asks for. Default
	// DefaultWindowPageSize.
	PageSize int `yaml:"page_size,omitempty"`
	// Prefetch is how many extra pages to pull beyond the rows actually
	// on screen. 0 (default) fetches only what's needed — the user sees
	// placeholder rows briefly at each page boundary. 1 usually hides
	// that, at the cost of an extra request per boundary.
	Prefetch int `yaml:"prefetch,omitempty"`

	// OffsetParam is the query parameter carrying the first row wanted
	// (e.g. "offset", "_start", "skip"). Required on http; rejected on
	// exec, which uses ${window.offset} in the argv instead.
	OffsetParam string `yaml:"offset_param"`
	// LimitParam is the query parameter carrying the page size (e.g.
	// "limit", "_limit", "per_page"). Required on http; rejected on
	// exec, which uses ${window.limit} in the argv instead.
	LimitParam string `yaml:"limit_param"`
	// TotalPath is the dot-path into the RAW response (pre-`root:`
	// slicing) holding the total row count — "numFound", "count",
	// "meta.total". Optional: without it the table can't show a
	// scrollbar proportion or a row count, and treats the end of what
	// has loaded as the end of the set.
	TotalPath string `yaml:"total_path,omitempty"`

	// SearchParam is the query parameter that answers *bare* filter
	// terms — the words the user types with no "column:" prefix. All
	// bare terms are joined with spaces into one value. Without it,
	// bare terms are dropped (and validation requires that a source
	// declaring no SearchParam declares at least one entry in Filters,
	// so a filterable table always has some way to filter).
	SearchParam string `yaml:"search_param,omitempty"`
	// Filters maps a column *title* to the destination that answers a
	// scoped term. On http the destination is a query parameter, so a
	// "author:tolkien" term becomes "?author=tolkien"; on exec it is a
	// token suffix, so the same term reaches the command as
	// ${window.filters.author}. Titles match the bound table's
	// `columns[].title` — the user can type any unambiguous prefix of
	// one, and it arrives here resolved to the full title.
	// A scoped term whose column isn't listed here degrades to a bare
	// term (and so lands in SearchParam), which is what the user meant
	// often enough to beat dropping it.
	Filters map[string]string `yaml:"filters,omitempty"`

	// SortParam is the query parameter carrying the sort field. Without
	// it, a sortable column on the bound table asks for a sort the
	// source can't answer, so validation rejects that combination.
	SortParam string `yaml:"sort_param,omitempty"`
	// Sorts maps a column title to the sort field name the API wants,
	// for APIs whose sort tokens aren't the column titles ("Year" →
	// "first_publish_year"). Unlisted columns send their title as-is.
	Sorts map[string]string `yaml:"sorts,omitempty"`
	// SortDescPrefix is prepended to the sort field for a descending
	// sort — "-" covers Django REST (`ordering=-created`) and most of
	// what follows it. Empty means the API has no descending form, so
	// both directions send the same value.
	SortDescPrefix string `yaml:"sort_desc_prefix,omitempty"`
}

// DefaultWindowPageSize is the window size used when WindowConfig.PageSize
// is unset. Matches tuilib's source.DefaultPageSize — big enough that a
// full screen is one request, small enough that the first frame is cheap.
const DefaultWindowPageSize = 100

// App configures the surrounding tuilib app shell.
type App struct {
	// Title prefixes the breadcrumb (the screen title appears after it).
	Title string `yaml:"title,omitempty"`
	// Version renders on the right side of the statusbar.
	Version string `yaml:"version,omitempty"`
	// Theme names a built-in theme.Theme.Name to use as the initial palette.
	// Unknown names fall through to the first theme.
	Theme string `yaml:"theme,omitempty"`
	// Glyphs overrides the marks components draw — row cursors, expand
	// arrows, scrollbar thumbs, sort indicators. Unset fields keep the
	// library's own mark, so overriding one arrow doesn't blank the
	// other twelve.
	Glyphs Glyphs `yaml:"glyphs,omitempty"`
	// Borders picks the border shapes and how a pane's title meets the
	// border line. Same story as Glyphs: an unset field keeps tuilib's
	// default (normal for components, thick for overlays).
	//
	// Both blocks apply to every palette rather than to Theme alone.
	// A theme is a choice of color; glyphs and border shapes are a
	// choice of vocabulary, and cycling themes at runtime (`t`) should
	// not change the vocabulary halfway through.
	Borders Borders `yaml:"borders,omitempty"`
	// ThemeKey cycles the palette, live, through every built-in theme
	// — the same list Theme picks the initial one from.
	//
	// Defaults to "t"; set it to "-" to pin the app to one palette. It
	// is a key the shell claims globally, so no action may bind it;
	// the validator rejects the collision rather than letting one
	// silently shadow the other.
	ThemeKey string `yaml:"theme_key,omitempty"`
	// OutputKey opens tuilib's output console — the scrollback that
	// collects every statusbar message and everything a subprocess
	// streams, with a statusbar badge counting events and a picker for
	// killing what's still running.
	//
	// Defaults to "o"; set it to "-" to turn the console off. It is a
	// key the shell claims globally, so no action may bind it — the
	// validator rejects that rather than letting one silently shadow
	// the other.
	OutputKey string `yaml:"output_key,omitempty"`
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

// Glyphs overrides tuilib's glyph vocabulary — the single-character
// marks components draw. Every field is optional; an empty one keeps
// whatever the palette already had, which is what lets a config change
// the cursor without restating the twelve marks it doesn't care about.
//
// Each value must be exactly one character. A two-character cursor
// shifts every list row by a column and pushes every table cell out of
// line with its header — which reads as a rendering bug rather than as
// the config that caused it, so the validator rejects it.
type Glyphs struct {
	// Cursor marks the focused row in list, logview and the action menu.
	Cursor string `yaml:"cursor,omitempty"`
	// Mark marks a selected row where multi-select is enabled.
	Mark string `yaml:"mark,omitempty"`
	// ExpandOpen / ExpandClosed are the disclosure arrows in tree and
	// inspector.
	ExpandOpen   string `yaml:"expand_open,omitempty"`
	ExpandClosed string `yaml:"expand_closed,omitempty"`
	// Rule is the horizontal line under an inline filter, drawn
	// identically by every filterable component.
	Rule string `yaml:"rule,omitempty"`
	// ScrollThumb / ScrollTrack are the vertical scrollbar;
	// HScrollThumb / HScrollTrack the horizontal one.
	ScrollThumb  string `yaml:"scroll_thumb,omitempty"`
	ScrollTrack  string `yaml:"scroll_track,omitempty"`
	HScrollThumb string `yaml:"h_scroll_thumb,omitempty"`
	HScrollTrack string `yaml:"h_scroll_track,omitempty"`
	// SortAsc / SortDesc follow the active column's title in a table.
	SortAsc  string `yaml:"sort_asc,omitempty"`
	SortDesc string `yaml:"sort_desc,omitempty"`
	// ColumnSep divides table columns.
	ColumnSep string `yaml:"column_sep,omitempty"`
	// Placeholder fills a row a windowed table hasn't received yet.
	Placeholder string `yaml:"placeholder,omitempty"`
}

// Borders picks the border shapes a theme draws with. Values come from
// BorderShapeNames; an unset field leaves the palette's own shape in
// place, which for every shipped palette means tuilib's default.
type Borders struct {
	// Active / Inactive are the shapes for ordinary components, focused
	// and unfocused. tuilib defaults both to normal on purpose: focus
	// is signalled by border color, and a component that changed weight
	// on focus would move the eye for a reason the user didn't ask
	// about. Set them differently only if that is what you want.
	Active   string `yaml:"active,omitempty"`
	Inactive string `yaml:"inactive,omitempty"`
	// Overlay is for what floats above content — confirm, alert, the
	// output console, the action menu. Defaults to thick, because a
	// heavier line is what separates an overlay from the pane it covers.
	Overlay string `yaml:"overlay,omitempty"`
	// SlotBrackets controls how a pane's title meets the border line.
	// One of SlotBracketNames:
	//
	//	none     ── title ──     (default)
	//	corners  ┐ title ┌       reads as a labelled tab
	//	tees    ─┤ title ├─
	SlotBrackets string `yaml:"slot_brackets,omitempty"`
}

// BorderShapeNames are the shapes `app.borders.{active,inactive,overlay}`
// accept. internal/build maps each one to its lipgloss constructor; a
// test there walks this list so the two can't drift apart.
var BorderShapeNames = []string{
	"normal", "rounded", "thick", "double", "hidden", "block", "ascii",
}

// SlotBracketNames are the values `app.borders.slot_brackets` accepts.
var SlotBracketNames = []string{"none", "corners", "tees"}

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
// and any on_key bindings that push other screens.
type Screen struct {
	// Title shows in the breadcrumb. May contain ${selection} tokens
	// when this screen is reachable via an on_key push.
	Title string `yaml:"title,omitempty"`
	// Layout is the root of the layout tree. Required.
	Layout Node `yaml:"layout"`
	// OnKey declares which components — when the given key is pressed
	// on them and they're focused — push another screen. Multi-screen
	// only. Each binding must spell out its key explicitly (`key:
	// enter`, `key: d`, `key: ctrl+r`); there is no implicit default.
	OnKey []OnKeyBinding `yaml:"on_key,omitempty"`
	// Actions bind keys to entries in the top-level `actions:`
	// registry, or declare one inline. See ActionBinding.
	Actions []ActionBinding `yaml:"actions,omitempty"`
}

// ThemeCycleKey returns the theme-cycle key with the default applied,
// or "" when the config pinned the palette with "-". Same shape as
// OutputConsoleKey, and for the same reason: one place owns the default.
func (a *App) ThemeCycleKey() string {
	switch a.ThemeKey {
	case "":
		return "t"
	case "-":
		return ""
	}
	return a.ThemeKey
}

// OutputConsoleKey returns the console key with the default applied, or
// "" when the config disabled it with "-". Callers use this rather than
// reading OutputKey so the default lives in one place.
func (a *App) OutputConsoleKey() string {
	switch a.OutputKey {
	case "":
		return "o"
	case "-":
		return ""
	}
	return a.OutputKey
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
	// Type is the value's data type: string (default), int, bool,
	// duration. It also PICKS THE FORM WIDGET when this parameter has
	// to be collected from the user (an action input nobody bound):
	// bool renders a yes/no toggle, everything else a text input,
	// unless Options is set — then it's a select regardless of type.
	//
	// Note this is the data type, not the widget name. There is
	// deliberately no `type: select`: "one of these strings" is a
	// string that happens to have Options, and conflating the two axes
	// is what made the old Prompt schema need both a Type and an
	// InitialIdx.
	Type string `yaml:"type,omitempty"`
	// Required means callers MUST supply a value before the source or
	// action can run. Mutually exclusive with Default. For an action
	// input, "supply" means either a bind: entry at the call site or a
	// value typed into the generated form.
	Required bool `yaml:"required,omitempty"`
	// Default is the value used when no caller supplies one, and the
	// value a generated form field starts on. Setting Default implies
	// the param is optional. For type: bool use "true" / "false"; for
	// a param with Options, one of the option strings.
	Default string `yaml:"default,omitempty"`
	// Description shows up in --list / --describe / --list-actions
	// output. One-line summary.
	Description string `yaml:"description,omitempty"`

	// The remaining fields matter only when this parameter is rendered
	// as a form field. They're inert for wrangl --param binding.

	// Label is the form field's caption. Defaults to the param name.
	Label string `yaml:"label,omitempty"`
	// Placeholder is the empty-state hint inside a text input.
	Placeholder string `yaml:"placeholder,omitempty"`
	// Options turns the field into a select over these choices. Legal
	// for any Type; the chosen option is the value.
	Options []string `yaml:"options,omitempty"`
	// Mask renders typed characters as bullets. Display only: the value
	// substitutes, binds and os.Setenv's as the real string everywhere
	// else. It exists so an API token isn't typed in the clear on a
	// screen someone may be sharing — not as a secret-storage mechanism.
	//
	// A field rather than a Type, because Type is the DATA type and
	// masking is a rendering choice on a string. `type: password` would
	// put a widget name on the axis that otherwise holds string / int /
	// bool / duration, which is the conflation the Parameter schema
	// exists to avoid.
	Mask bool `yaml:"mask,omitempty"`
	// Order sorts fields in a generated form. Inputs live in a map, and
	// map iteration has no order — without this, a two-field form would
	// render its fields in a different sequence run to run. Ties break
	// alphabetically, so leaving Order unset everywhere is stable, just
	// alphabetical.
	Order int `yaml:"order,omitempty"`
}

// Prompt is one field in the boot-time form (`app.prompts:`), which
// runs before any screen renders and os.Setenv's each value under its
// Key so ${env.<KEY>} resolves downstream.
//
// It is an ordered list rather than a map because a form has a reading
// order and boot prompts are usually a short deliberate sequence. The
// field vocabulary is Parameter's, inlined — one widget schema for boot
// prompts and action inputs alike.
//
// Action inputs are NOT prompts. An action declares typed `inputs:`;
// anything the call site doesn't bind is collected in a generated form
// built from those same Parameter fields. There is no separate
// ${prompt.*} namespace.
type Prompt struct {
	// Key names the env var the collected value is written to.
	Key       string `yaml:"key"`
	Parameter `yaml:",inline"`
}

// OnKeyBinding wires "pressing Key on Source pushes Push." The source
// must be a list or table component referenced in this screen's layout;
// Push names a screen in Config.Screens. The source component's current
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
type OnKeyBinding struct {
	Source string            `yaml:"source"`
	Push   string            `yaml:"push"`
	Bind   map[string]string `yaml:"bind,omitempty"`
	// Key is the trigger. Required — spell out `key: enter` for the
	// classic drilldown, `key: d` for describe, `key: l` for logs,
	// `key: ctrl+r` for a resource reload push. Any tea.KeyMsg.String()
	// name works. Multiple bindings on the same source are allowed as
	// long as their (source, key) pairs are distinct.
	Key string `yaml:"key"`
	// Label is an optional custom label for the help strip. When empty,
	// the strip shows the key + "open". Handy for kubectl-shape UIs
	// that want "d → describe", "l → logs", etc.
	Label string `yaml:"label,omitempty"`
	// Section is the heading this binding sits under in the key overlay
	// (`?`). Defaults to "Open".
	//
	// Headings name what the keys DO, never what holds them — that is
	// tuilib's rule and the reason the overlay is worth opening. The
	// component's own keys arrive already grouped that way (Navigate,
	// Scroll, Filter, Search, Select, Sort, Expand, View); reuse one of
	// those names to file a push alongside them, or invent one for a
	// group of your own. Bindings sharing a name share a heading, in
	// first-appearance order.
	Section string `yaml:"section,omitempty"`
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

	// Windowed is derived, not authored: Config.Validate sets it on a
	// table whose `source:` names an entry declaring `window:`. It rides
	// on the component (rather than being looked up at build time)
	// because buildTable only ever sees the component — and because a
	// pushed screen's cloned component needs to stay windowed without
	// re-deriving anything.
	//
	// A windowed table holds a sparse slice of a larger set, so it
	// filters and sorts remotely and paints unloaded rows as
	// placeholders. See WindowConfig.
	Windowed bool `yaml:"-"`

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

	// Markable turns on multi-select: the component grows a mark
	// gutter and binds x / X / A / D (toggle, range-extend from the
	// last mark, all, none). Supported on list, table and tree; a
	// load error anywhere else, because tuilib ships no marking there
	// and a mark key that quietly did nothing would read as broken.
	//
	// Marks are held by KEY, never by index, so a poll that reorders
	// rows between marking and acting can't slide the selection onto
	// neighbours. Where that key comes from depends on the kind — see
	// MarkKey.
	Markable bool `yaml:"markable,omitempty"`
	// MarkKey is the dot-path to a stable per-row identity, and is
	// REQUIRED on a source-bound markable table. It is read from the
	// original source item, not the rendered cells, so the identity
	// can be a field the table never displays (`metadata.uid`, `id`,
	// a self-link) — usually the right one.
	//
	// It is deliberately not defaulted to the first column. That
	// fails two ways, both silent and data-dependent: a non-unique
	// first column (pods named the same across namespaces) collapses
	// two rows onto one key, so marking one marks both; and a
	// volatile one (AGE, STATUS, RESTARTS) changes on the next poll,
	// so the user's marks evaporate. tuilib protects against index
	// drift; nothing can protect against a key that isn't stable.
	// One load error the author fixes once beats a selection that
	// goes wrong at 2am.
	//
	// Not accepted on the kinds that already have an identity: a
	// list keys on its item string, a tree on a node's path (the
	// same path it uses for expansion state), and a static table on
	// the row's position, which cannot drift because nothing
	// repolls it. Setting it there means the author expected it to
	// do something.
	//
	// Distinct from RowKey below, which changes how a STREAMING
	// table inserts rows. They can differ, and setting one must not
	// silently do the other's job.
	MarkKey Path `yaml:"mark_key,omitempty"`

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
	// InitialQuery pre-populates the search query on logview / tree /
	// textview.
	InitialQuery string `yaml:"initial_query,omitempty"`

	// textview fields
	// Content seeds the initial body. Overridden by SetContent when the
	// component is source-bound. Static-content mode is handy for help
	// panes, licence text, or any doc you want available in-app without
	// a fetch.
	Content string `yaml:"content,omitempty"`
	// Wrap toggles word-wrap for textview. Default off matches tuilib's
	// zero-value default; wrap is also runtime-toggleable via `w`.
	Wrap bool `yaml:"wrap,omitempty"`

	// tree fields
	Root *TreeNode `yaml:"root,omitempty"`
	// Label is the dot-path (or fallback chain) picking each leaf's
	// display label from a source-bound tree's items. Required when
	// `type: tree` binds a `source:`; ignored for static-root trees.
	// Same semantics as list.Item and table.Column.Value.
	Label Path `yaml:"label,omitempty"`
	// GroupBy is the dot-path bucketing a flat iterable into named
	// parent nodes — useful for kubectl-shape data where a single
	// list of resources should render categorized by kind. Buckets
	// preserve first-appearance order; each item lands under the
	// parent whose label equals its GroupBy value (stringified).
	// Optional — when empty, all items become direct children of
	// the root. Mutually exclusive with Children.
	GroupBy Path `yaml:"group_by,omitempty"`
	// Children is the dot-path on each node pointing to its list of
	// child nodes. Enables recursive walking of a nested source
	// response — filesystem trees, org charts, k8s owner-reference
	// graphs, any structure where each record already knows its
	// descendants. When Children is set, GroupBy is ignored. The
	// source's response can be either a single root node (map) or
	// a list of top-level nodes; nodes with a missing / empty
	// Children path are leaves.
	Children Path `yaml:"children,omitempty"`
	// RootLabel is the display label for the root node of a source-
	// bound tree. Supports ${selection.*} / ${env.*} / ${prompt.*}
	// substitution. Defaults to the component's Title when empty;
	// falls back to the source name if both are empty. Kept stable
	// across data refreshes so tuilib.tree.SetRoot's expanded-state
	// preservation actually hits — the label is the key.
	RootLabel string `yaml:"root_label,omitempty"`

	// inspector fields
	Fields []InspectorField `yaml:"fields,omitempty"`
	// Auto opts a source-bound inspector into deriving its field tree
	// from the fetched data via inspector.FromAny. Fields is ignored
	// when Auto is true — the user is either declaring the record
	// shape or asking for whatever-comes-back, not both. Handles
	// nested maps and arrays natively (feature C from the tui-builder
	// integration batch), so `map[string]any` / `[]any` no longer
	// stringify to `map[...]` under a scalar field.
	Auto bool `yaml:"auto,omitempty"`

	// shared hierarchical fields (tree, inspector). InitialDepth pre-
	// expands every node whose depth is < InitialDepth: 0 = root only,
	// 1 = root expanded, 2 = root + first level, …
	InitialDepth int `yaml:"initial_depth,omitempty"`

	// OnCursor makes this component reactive to another component's
	// cursor. The driver's RowFocusedMsg triggers a re-bind of the
	// target's source parameters via the Bind map (templates evaluated
	// against the driver's current focused row via ${cursor.*}), then
	// a re-fetch through the source's param cache. Used to build
	// "table on top, detail below" interfaces where scrolling the top
	// pane refreshes the bottom.
	OnCursor *OnCursor `yaml:"on_cursor,omitempty"`
}

// OnCursor wires a component to another component's cursor state.
// Driver may be a table, list, or tree — all three emit tuilib
// focus-change messages (table.RowFocusedMsg from v0.16.0;
// list.SelectedChangedMsg and tree.SelectedChangedMsg from v0.17.0).
// The pattern:
//
//	inspector:
//	  type: inspector
//	  source: pod_detail
//	  auto: true
//	  on_cursor:
//	    source: pods_table            # driver component name
//	    bind:
//	      name:      ${cursor.Name}   # driver row cells feed the target
//	      namespace: ${cursor.Namespace} # source's params
//
// Bind templates can reference ${cursor.*} (the driver's current row)
// alongside ${env.*} — same substitution as everywhere else, minus
// selection/prompt which don't apply mid-screen.
type OnCursor struct {
	// Source names the driver component in the same screen's layout.
	Source string `yaml:"source"`
	// Bind maps destination-source parameter names to templates
	// evaluated against the driver's current cursor. Values support
	// ${cursor.*} and ${env.*}. Every declared param in the target
	// source's Parameters map should have an entry; the fetcher
	// substitutes an empty string for unresolved cells so the URL
	// stays well-formed even when the cursor lands on a row missing
	// a referenced column.
	Bind map[string]string `yaml:"bind,omitempty"`
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
	// Hidden opts the column out of rendering while keeping it in the
	// row payload — it still participates in filter matching AND still
	// shows up in Selected() / RowFocusedMsg cells. This is the
	// "identity column" pattern: bind ${cursor.Namespace} against a
	// hidden Namespace column so a drilldown gets the value without
	// giving up screen real estate. Passed through to tuilib's
	// table.Column.Hidden (shipped in v0.16.0).
	Hidden bool `yaml:"hidden,omitempty"`
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
