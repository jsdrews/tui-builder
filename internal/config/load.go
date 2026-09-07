package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Load reads a YAML file at path and returns a validated Config.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Desugar `pipe:` chains before validation runs.
	if err := ExpandSourcesPipe(c.Data.Sources); err != nil {
		return nil, fmt.Errorf("expand %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	// Env-var check runs after Validate so schema errors surface
	// first. Applies defaults, errors on missing required vars,
	// warns on undeclared-and-unset references.
	if err := checkEnv(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Validate walks the config and returns the first structural error found.
// Per-entry checks delegate to each Source's Validate method; cross-
// entry checks (upstream resolution, cycle detection, join-lookup
// constraints, component bindings) walk the unified Sources map.
func (c *Config) Validate() error {
	// 0. Boot-time prompts. Same shape as an action's, and until now the
	//    only list of prompts nothing checked.
	if err := validatePrompts(c.App.Prompts, "app.prompts"); err != nil {
		return err
	}
	// 0b. App chrome. Presentation only, but both halves fail quietly
	//     when they're wrong — see validateChrome.
	if err := validateChrome(&c.App); err != nil {
		return err
	}
	// 1. Per-entry structural validation.
	for name, s := range c.Data.Sources {
		if s == nil {
			return fmt.Errorf("data.sources.%s: empty entry", name)
		}
		if err := s.Validate("data.sources." + name); err != nil {
			return err
		}
	}
	// 2. Upstream resolution + cycle detection across the source graph.
	if err := validateSourceUpstreams(c.Data.Sources); err != nil {
		return err
	}
	// 3. Join lookups must reference leaf entries that declare parameters
	//    (so per-row BindParams has something to bind to).
	if err := validateJoinLookups(c.Data.Sources); err != nil {
		return err
	}
	// 4. Component bindings: `source:` must name an entry.
	for name, comp := range c.TUI.Components {
		if comp == nil {
			return fmt.Errorf("tui.components.%s: empty definition", name)
		}
		if err := comp.validate("tui.components." + name); err != nil {
			return err
		}
		if ref := comp.Source; ref != "" {
			entry, ok := c.Data.Sources[ref]
			if !ok {
				return fmt.Errorf("tui.components.%s: source %q not defined in data.sources", name, ref)
			}
			if err := bindWindowed(name, comp, ref, entry); err != nil {
				return err
			}
		}
		// After bindWindowed: the windowed check below needs the flag
		// it sets.
		if err := validateMarkable(comp, "tui.components."+name); err != nil {
			return err
		}
	}
	// 5. Windowed sources can't be fed through operators — see
	//    validateWindowedUpstreams.
	if err := validateWindowedUpstreams(c.Data.Sources); err != nil {
		return err
	}

	// Mode check: exactly one of screen / screens.
	hasSingle := c.TUI.Screen.Layout.set() > 0
	hasMulti := len(c.TUI.Screens) > 0
	switch {
	case hasSingle && hasMulti:
		return fmt.Errorf("config: set either `tui.screen:` (single) or `tui.screens:` (multi) — not both")
	case !hasSingle && !hasMulti:
		return fmt.Errorf("config: must set `tui.screen:` (single) or `tui.screens:` + `tui.initial:` (multi)")
	}

	if hasSingle {
		refs := map[string]int{}
		if err := c.TUI.Screen.Layout.validate("tui.screen.layout", c.TUI.Components, refs); err != nil {
			return err
		}
		for name, n := range refs {
			if n > 1 {
				return fmt.Errorf("tui.components.%s: referenced %d times in tui.screen.layout — each component may be placed only once per screen", name, n)
			}
		}
		if err := validateActions(c.TUI.Screen.Actions, refs, c.TUI.Components, "tui.screen", c.App.OutputConsoleKey()); err != nil {
			return err
		}
		if err := validateOnCursor(refs, c.TUI.Components, "tui.screen"); err != nil {
			return err
		}
		return nil
	}

	// Multi-screen.
	if c.TUI.Initial == "" {
		return fmt.Errorf("config: `tui.initial:` is required when `tui.screens:` is set")
	}
	if _, ok := c.TUI.Screens[c.TUI.Initial]; !ok {
		return fmt.Errorf("config: initial screen %q not defined in tui.screens map", c.TUI.Initial)
	}
	for name, s := range c.TUI.Screens {
		if s == nil {
			return fmt.Errorf("tui.screens.%s: empty definition", name)
		}
		refs := map[string]int{}
		if err := s.Layout.validate("tui.screens."+name+".layout", c.TUI.Components, refs); err != nil {
			return err
		}
		for cname, n := range refs {
			if n > 1 {
				return fmt.Errorf("tui.screens.%s: components.%s referenced %d times — each component may be placed only once per screen", name, cname, n)
			}
		}
		// Track (source, key) → binding index so a screen can't wire two
		// different pushes onto the same keystroke — the first-match
		// behavior of the dispatch loop would silently pick one.
		seenKeys := map[string]int{}
		for i, b := range s.OnKey {
			if b.Source == "" || b.Push == "" {
				return fmt.Errorf("tui.screens.%s.on_key[%d]: source and push are required", name, i)
			}
			if b.Key == "" {
				return fmt.Errorf("tui.screens.%s.on_key[%d]: `key:` is required (spell out `key: enter` for the classic drilldown)", name, i)
			}
			if refs[b.Source] == 0 {
				return fmt.Errorf("tui.screens.%s.on_key[%d]: source %q not used in this screen's layout", name, i, b.Source)
			}
			if _, ok := c.TUI.Screens[b.Push]; !ok {
				return fmt.Errorf("tui.screens.%s.on_key[%d]: push %q not defined in screens map", name, i, b.Push)
			}
			src := c.TUI.Components[b.Source]
			if src.Type != "list" && src.Type != "table" {
				return fmt.Errorf("tui.screens.%s.on_key[%d]: source %q must be a list or table (got %s)", name, i, b.Source, src.Type)
			}
			dedupKey := b.Source + "\x00" + b.Key
			if prev, ok := seenKeys[dedupKey]; ok {
				return fmt.Errorf("tui.screens.%s.on_key[%d]: source %q + key %q already bound at on_key[%d]", name, i, b.Source, b.Key, prev)
			}
			seenKeys[dedupKey] = i
		}
		if err := validateActions(s.Actions, refs, c.TUI.Components, fmt.Sprintf("tui.screens.%s", name), c.App.OutputConsoleKey()); err != nil {
			return err
		}
		if err := validateOnCursor(refs, c.TUI.Components, fmt.Sprintf("tui.screens.%s", name)); err != nil {
			return err
		}
	}
	return nil
}

// validateOnCursor checks every layout-participating component's
// OnCursor binding. Rules:
//   - Source names another component in the same screen's layout.
//   - Driver must be a table, list, or tree (the three tuilib
//     components that emit focus-change messages: RowFocusedMsg from
//     v0.16.0 for table; SelectedChangedMsg from v0.17.0 for list
//     and tree).
//   - Target component must itself be source-bound: the bind: block's
//     job is to feed the target's source's parameters, and a
//     component with no source has nowhere for those params to land.
//   - Target's source must declare `parameters:` covering every bind
//     key; extraneous bind entries error so users notice typos.
func validateOnCursor(refs map[string]int, components map[string]*Component, path string) error {
	for name, comp := range components {
		if comp == nil || comp.OnCursor == nil {
			continue
		}
		if refs[name] == 0 {
			// Component defined but not in this screen's layout —
			// on_cursor doesn't apply here. Skip; each layout-relevant
			// screen validates its own participants.
			continue
		}
		oc := comp.OnCursor
		if oc.Source == "" {
			return fmt.Errorf("%s.components.%s.on_cursor: `source:` is required", path, name)
		}
		if refs[oc.Source] == 0 {
			return fmt.Errorf("%s.components.%s.on_cursor: source %q not used in this screen's layout", path, name, oc.Source)
		}
		driver := components[oc.Source]
		switch driver.Type {
		case "table", "list", "tree":
			// ok — all three emit tuilib focus-change messages
		default:
			return fmt.Errorf("%s.components.%s.on_cursor: source %q must be a table, list, or tree (got %s)", path, name, oc.Source, driver.Type)
		}
		if comp.Source == "" {
			return fmt.Errorf("%s.components.%s.on_cursor: target component has no `source:` — nothing to rebind on cursor moves", path, name)
		}
	}
	return nil
}

func validateActions(actions []Action, refs map[string]int, components map[string]*Component, path, outputKey string) error {
	for i, a := range actions {
		if a.Key == "" {
			return fmt.Errorf("%s.actions[%d]: key is required", path, i)
		}
		// The console key is claimed by the app shell, so a component
		// never sees it. Binding an action to it would look right in the
		// config and do nothing at runtime.
		if outputKey != "" && a.Key == outputKey {
			return fmt.Errorf("%s.actions[%d]: key %q is the output console key (app.output_key) — pick another, or set app.output_key to disable the console", path, i, a.Key)
		}
		if a.Source == "" {
			return fmt.Errorf("%s.actions[%d]: source is required", path, i)
		}
		if len(a.Run) == 0 {
			return fmt.Errorf("%s.actions[%d]: run is required (non-empty argv)", path, i)
		}
		if refs[a.Source] == 0 {
			return fmt.Errorf("%s.actions[%d]: source %q not used in this screen's layout", path, i, a.Source)
		}
		src := components[a.Source]
		if src.Type != "list" && src.Type != "table" {
			return fmt.Errorf("%s.actions[%d]: source %q must be a list or table (got %s)", path, i, a.Source, src.Type)
		}
		if err := validatePrompts(a.Prompts, fmt.Sprintf("%s.actions[%d].prompts", path, i)); err != nil {
			return err
		}
	}
	return nil
}

// validatePrompts checks one list of form prompts — app.prompts and an
// action's prompts: share the shape, so they share the rules.
//
// An unknown type is an error rather than a fall-through to text. It used
// to be harmless (a typo'd select rendered as a text box), but `password`
// makes it dangerous: `passwrod:` silently renders the token in the clear,
// which is the one thing the field exists to prevent.
func validatePrompts(prompts []Prompt, path string) error {
	for i, p := range prompts {
		if p.Key == "" {
			return fmt.Errorf("%s[%d]: key is required", path, i)
		}
		switch p.Type {
		case "", "text", "password", "select", "confirm":
		default:
			return fmt.Errorf("%s[%d]: unknown type %q (want text|password|select|confirm)", path, i, p.Type)
		}
		if p.Type == "select" && len(p.Options) == 0 {
			return fmt.Errorf("%s[%d]: select prompt needs options", path, i)
		}
	}
	return nil
}

// validateMarkable checks `markable:` and `mark_key:`.
//
// Every rule here exists because the alternative is an affordance that
// renders and silently does nothing — a mark gutter you can move a
// cursor through, press x on, and watch not respond. tuilib holds marks
// by key, so a component that can't supply keys can't mark, and the
// only honest place to say so is load time.
//
// Runs after bindWindowed, which is what sets Windowed.
func validateMarkable(c *Component, path string) error {
	if !c.Markable {
		if len(c.MarkKey) > 0 {
			return fmt.Errorf("%s: `mark_key:` set without `markable: true` — it does nothing on its own", path)
		}
		return nil
	}
	switch c.Type {
	case "list", "table", "tree":
	default:
		return fmt.Errorf("%s: `markable: true` is not supported on %s (want list|table|tree) — no verb acts on a set of its rows and tuilib draws no mark gutter there", path, c.Type)
	}
	if c.Type != "table" {
		if len(c.MarkKey) > 0 {
			return fmt.Errorf("%s: `mark_key:` is not accepted on %s — a list keys on its item string and a tree on a node's path", path, c.Type)
		}
		return nil
	}
	// Tables from here down.
	if c.Windowed {
		return fmt.Errorf("%s: `markable: true` and a windowed source are mutually exclusive — a window holds one page of rows without keys, so marking there is inert", path)
	}
	if c.Source == "" {
		if len(c.MarkKey) > 0 {
			return fmt.Errorf("%s: `mark_key:` is not accepted on a table with static `rows:` — the rows are fixed at load, so their position is their identity", path)
		}
		return nil
	}
	if len(c.MarkKey) == 0 || c.MarkKey[0] == "" {
		return fmt.Errorf("%s: `markable: true` on a source-bound table needs `mark_key:` (dot-path to a stable per-row identity, e.g. metadata.uid). It is not defaulted to the first column on purpose: a non-unique one collapses two rows onto one mark, and a volatile one (AGE, STATUS) loses the marks on the next poll", path)
	}
	return nil
}

// validateChrome checks app.glyphs and app.borders.
//
// Both are pure presentation, and both fail *quietly* when they're
// wrong, which is why they're load errors rather than best-effort. An
// unrecognised border name would keep the default shape, so the config
// change simply appears not to have worked. An over-long glyph renders
// fine on its own but shifts every row it's drawn on, so it reads as a
// layout bug somewhere else entirely.
func validateChrome(a *App) error {
	for _, g := range []struct{ field, value string }{
		{"cursor", a.Glyphs.Cursor},
		{"mark", a.Glyphs.Mark},
		{"expand_open", a.Glyphs.ExpandOpen},
		{"expand_closed", a.Glyphs.ExpandClosed},
		{"rule", a.Glyphs.Rule},
		{"scroll_thumb", a.Glyphs.ScrollThumb},
		{"scroll_track", a.Glyphs.ScrollTrack},
		{"h_scroll_thumb", a.Glyphs.HScrollThumb},
		{"h_scroll_track", a.Glyphs.HScrollTrack},
		{"sort_asc", a.Glyphs.SortAsc},
		{"sort_desc", a.Glyphs.SortDesc},
		{"column_sep", a.Glyphs.ColumnSep},
		{"placeholder", a.Glyphs.Placeholder},
	} {
		// Empty means "keep the default", not "draw nothing".
		if g.value == "" {
			continue
		}
		if utf8.RuneCountInString(g.value) != 1 {
			return fmt.Errorf("app.glyphs.%s: %q must be a single character (it is drawn in a one-cell slot)", g.field, g.value)
		}
	}
	for _, b := range []struct{ field, value string }{
		{"active", a.Borders.Active},
		{"inactive", a.Borders.Inactive},
		{"overlay", a.Borders.Overlay},
	} {
		if b.value == "" {
			continue
		}
		if !slices.Contains(BorderShapeNames, b.value) {
			return fmt.Errorf("app.borders.%s: unknown border %q (want %s)", b.field, b.value, strings.Join(BorderShapeNames, "|"))
		}
	}
	if v := a.Borders.SlotBrackets; v != "" && !slices.Contains(SlotBracketNames, v) {
		return fmt.Errorf("app.borders.slot_brackets: unknown style %q (want %s)", v, strings.Join(SlotBracketNames, "|"))
	}
	return nil
}

// set returns the number of tagged-union fields set on a Node — used by
// the validator to detect empty / over-set nodes.
func (n *Node) set() int {
	set := 0
	if n.VStack != nil {
		set++
	}
	if n.HStack != nil {
		set++
	}
	if n.ZStack != nil {
		set++
	}
	if n.Component != "" {
		set++
	}
	return set
}

func (n *Node) validate(path string, components map[string]*Component, refs map[string]int) error {
	switch n.set() {
	case 0:
		return fmt.Errorf("%s: layout node must set one of vstack/hstack/zstack/component", path)
	case 1:
	default:
		return fmt.Errorf("%s: layout node must set exactly one of vstack/hstack/zstack/component", path)
	}

	switch {
	case n.VStack != nil:
		for i, it := range n.VStack {
			if err := it.Node.validate(fmt.Sprintf("%s.vstack[%d]", path, i), components, refs); err != nil {
				return err
			}
		}
	case n.HStack != nil:
		for i, it := range n.HStack {
			if err := it.Node.validate(fmt.Sprintf("%s.hstack[%d]", path, i), components, refs); err != nil {
				return err
			}
		}
	case n.ZStack != nil:
		if err := n.ZStack.Base.validate(path+".zstack.base", components, refs); err != nil {
			return err
		}
		if err := n.ZStack.Overlay.validate(path+".zstack.overlay", components, refs); err != nil {
			return err
		}
	case n.Component != "":
		if _, ok := components[n.Component]; !ok {
			return fmt.Errorf("%s: component %q not defined in components map", path, n.Component)
		}
		refs[n.Component]++
	}
	return nil
}

func (c *Component) validate(path string) error {
	switch c.Type {
	case "list", "table", "logview", "tree", "inspector", "textview":
	case "":
		return fmt.Errorf("%s: missing type", path)
	default:
		return fmt.Errorf("%s: unknown component type %q (want list|table|logview|tree|inspector|textview)", path, c.Type)
	}
	if c.Type == "table" {
		if len(c.Columns) == 0 {
			return fmt.Errorf("%s: table needs columns", path)
		}
		for i, col := range c.Columns {
			switch col.Sort {
			case "", "string", "number", "si":
			default:
				return fmt.Errorf("%s.columns[%d]: unknown sort %q (want string|number|si)", path, i, col.Sort)
			}
			for j, r := range col.ColorRules {
				if r.Color == "" {
					return fmt.Errorf("%s.columns[%d].color_rules[%d]: color is required", path, i, j)
				}
			}
		}
	}
	if c.Type == "tree" && c.Root == nil && c.Source == "" {
		return fmt.Errorf("%s: tree needs root", path)
	}
	if c.Auto {
		if c.Type != "inspector" {
			return fmt.Errorf("%s: `auto: true` is only valid on inspector components (got %q)", path, c.Type)
		}
		if c.Source == "" {
			return fmt.Errorf("%s: inspector `auto: true` needs a `source:` — nothing to derive fields from otherwise", path)
		}
	}
	// Data-source-bound components: enforce per-shape mapping fields.
	if c.Source != "" {
		switch c.Type {
		case "list":
			if c.Item == "" {
				return fmt.Errorf("%s: list bound to source %q needs `item:` (dot-path to display string)", path, c.Source)
			}
		case "table":
			for i, col := range c.Columns {
				if len(col.Value) == 0 || col.Value[0] == "" {
					return fmt.Errorf("%s.columns[%d]: table bound to source %q needs `value:` (dot-path) on every column", path, i, c.Source)
				}
			}
		case "inspector":
			// Path fields validated lazily — empty path keeps the static Value.
			// Auto and Fields are mutex: user is either declaring the record
			// shape or asking for auto-derive from whatever comes back.
			if c.Auto && len(c.Fields) > 0 {
				return fmt.Errorf("%s: inspector `auto: true` and declared `fields:` are mutually exclusive — pick one", path)
			}
		case "logview":
			// logview takes whatever the source returns: format:text bodies
			// are split on \n; JSON []string is used as-is. No per-line
			// mapping field needed.
		case "textview":
			// textview takes whatever the source returns as a single string.
			// format:text bodies pass through verbatim; JSON values fall
			// back to a formatted string representation. No mapping field.
		case "tree":
			if len(c.Label) == 0 || c.Label[0] == "" {
				return fmt.Errorf("%s: tree bound to source %q needs `label:` (dot-path to each leaf's display label)", path, c.Source)
			}
			if len(c.Children) > 0 && len(c.GroupBy) > 0 {
				return fmt.Errorf("%s: tree `children:` (recursive walk) and `group_by:` (flat + bucket) are mutually exclusive", path)
			}
		}
	}
	return nil
}

// bindWindowed marks a component as windowed when the entry it binds to
// declares `window:`, rejecting the bindings that can't hold a sparse
// window. Only tuilib's table has SetWindow — a list or inspector fed a
// window would silently show page 1 and call it the whole set, which is
// worse than refusing at load.
func bindWindowed(name string, comp *Component, ref string, entry *Source) error {
	if entry == nil || entry.Window == nil {
		return nil
	}
	if comp.Type != "table" {
		return fmt.Errorf("tui.components.%s: source %q declares `window:` but this is a %s component — only `type: table` can hold a windowed source (it's the one component that renders a sparse slice of a larger set)", name, ref, comp.Type)
	}
	// Sorting is answered by the server under a window, so a sortable
	// column with no `sort_param:` is a control that can't do anything.
	if entry.Window.SortParam == "" {
		for i, col := range comp.Columns {
			if col.Sortable {
				return fmt.Errorf("tui.components.%s: columns[%d] (%q) is sortable but source %q declares no `window.sort_param:` — a windowed table sorts remotely, so there's nowhere to send the request", name, i, col.Title, ref)
			}
		}
	}
	comp.Windowed = true
	return nil
}

// validateWindowedUpstreams rejects an operator whose upstream declares
// `window:`. Operators consume a source through Fetch, which for a
// windowed source yields only its first page — so `filter` over a
// windowed source would filter 100 rows and present the result as if it
// had filtered all 30,000. Failing at load beats shipping that answer.
//
// The fix for a user hitting this is to push the work into the request:
// `window.filters:` scopes on the server, which is the whole point.
func validateWindowedUpstreams(sources map[string]*Source) error {
	for name, s := range sources {
		if s == nil || s.Window != nil {
			continue
		}
		for _, up := range s.Upstreams() {
			u, ok := sources[up]
			if !ok || u == nil || u.Window == nil {
				continue
			}
			return fmt.Errorf("data.sources.%s: upstream %q declares `window:` — a windowed source can only be bound directly to a table, not consumed by the %q operator (an operator sees one page and would present the result as if it had seen every row). Push the work into the request via `window.filters:` / `window.sort_param:`, or drop `window:` and use `paginate:` if you need the whole set in memory", name, up, s.Type)
		}
	}
	return nil
}

// validateSourceUpstreams walks every source's Upstreams() and
// checks each reference resolves to a defined entry, then runs a DFS
// cycle detector across the unified graph. Join lookup references
// count as edges too — they don't form a build-time dep (joinSource
// re-fetches per row), but they DO form a name-resolution dep, and
// since lookups must be leaves (validateJoinLookups enforces) the
// cycle walk terminates correctly without false positives.
func validateSourceUpstreams(sources map[string]*Source) error {
	for name, s := range sources {
		for _, up := range s.Upstreams() {
			if _, ok := sources[up]; !ok {
				return fmt.Errorf("data.sources.%s: upstream %q not defined in data.sources", name, up)
			}
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(sources))
	var dfs func(name string, stack []string) error
	dfs = func(name string, stack []string) error {
		switch color[name] {
		case gray:
			return fmt.Errorf("data.sources: cyclic reference: %v -> %s", stack, name)
		case black:
			return nil
		}
		color[name] = gray
		stack = append(stack, name)
		if s := sources[name]; s != nil {
			for _, up := range s.Upstreams() {
				if err := dfs(up, stack); err != nil {
					return err
				}
			}
		}
		color[name] = black
		return nil
	}
	for name := range sources {
		if err := dfs(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// validateJoinLookups enforces the constraint that every join's
// lookup references a leaf entry with declared parameters. Joins
// re-invoke lookups per driver row via BindParams; that pattern only
// works against leaf sources (operators don't carry templated
// fields) and requires the lookup to declare what params it accepts.
func validateJoinLookups(sources map[string]*Source) error {
	for name, s := range sources {
		if s.Type != "join" {
			continue
		}
		for lname, look := range s.Lookups {
			target, ok := sources[look.From]
			if !ok {
				return fmt.Errorf("data.sources.%s.join.lookups.%s: `from: %q` is not a defined entry", name, lname, look.From)
			}
			if !target.IsLeaf() {
				return fmt.Errorf("data.sources.%s.join.lookups.%s: `from: %q` is not a leaf source (pipelines aren't supported as lookups yet)", name, lname, look.From)
			}
			if len(target.Parameters) == 0 {
				return fmt.Errorf("data.sources.%s.join.lookups.%s: source %q must declare `parameters:` so the join can bind per-row values to it", name, lname, look.From)
			}
			for paramName := range look.On {
				if _, ok := target.Parameters[paramName]; !ok {
					return fmt.Errorf("data.sources.%s.join.lookups.%s.on.%s: source %q has no parameter %q", name, lname, paramName, look.From, paramName)
				}
			}
		}
	}
	return nil
}

// validateParametersMap is the shared schema validator for any
// parameter declaration block (DataSource, Pipeline). Mutual
// exclusion rules (required + default) and the type whitelist are
// the same for every consumer; extracting the body keeps the rules
// in one place.
func validateParametersMap(params map[string]*Parameter, path string) error {
	for name, p := range params {
		if p == nil {
			return fmt.Errorf("%s.parameters.%s: empty definition", path, name)
		}
		switch p.Type {
		case "", "string", "int", "bool", "duration":
		default:
			return fmt.Errorf("%s.parameters.%s: unknown type %q (want string|int|bool|duration)", path, name, p.Type)
		}
		if p.Required && p.Default != "" {
			return fmt.Errorf("%s.parameters.%s: `required: true` and `default:` are mutually exclusive — defaults imply optional", path, name)
		}
	}
	return nil
}

// ResolveParams applies the standard resolution rules — declared
// default → caller value → error if required — and returns a
// resolved name → value map. Extra keys in `supplied` that aren't in
// `declared` are rejected so silent typos don't hide bugs.
//
// Used by cfg.BindLeafParams (which then substitutes ${params.X}
// into leaf-source URL/Command/etc. templates) and the pipeline
// layer (which makes the resolved values available as `params.X` in
// operator
// expressions). One resolver, two consumers.
func ResolveParams(declared map[string]*Parameter, supplied map[string]string) (map[string]string, error) {
	for k := range supplied {
		if _, ok := declared[k]; !ok {
			return nil, fmt.Errorf("parameter %q not declared", k)
		}
	}
	resolved := make(map[string]string, len(declared))
	for name, spec := range declared {
		if v, ok := supplied[name]; ok {
			resolved[name] = v
			continue
		}
		if spec.Default != "" {
			resolved[name] = spec.Default
			continue
		}
		if spec.Required {
			return nil, fmt.Errorf("required parameter %q not provided", name)
		}
		resolved[name] = ""
	}
	return resolved, nil
}

// BindParams resolves caller-supplied parameter values against the
// source's declared parameter schema (via ResolveParams) and
// substitutes ${params.<name>} tokens in every templated string
// field (URL, Body, Headers, Command, Env, Path, InitialMessages).
//
// Mutates the receiver in place; callers that want to reuse the
// undecorated source should pass a copy.

// substituteParams replaces ${params.NAME} tokens with their resolved
// values. Tokens for params not in the map are left as-is so the
// source author can spot the typo at fetch time (404 / connection
// failure) rather than silently turning into an empty string.
func substituteParams(s string, params map[string]string) string {
	for name, val := range params {
		s = replaceAll(s, "${params."+name+"}", val)
	}
	return s
}

// replaceAll is a tiny strings.ReplaceAll alias kept local so the
// package's dependency surface stays narrow. (strings is already
// imported via fmt; keeping this here avoids a one-shot import.)
func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func paramKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
