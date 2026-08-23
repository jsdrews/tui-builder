package build

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jsdrews/tuilib/pkg/inspector"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// defaultStreamMaxRows caps the ring buffer for streaming-table
// bindings when Cfg.MaxRows is unset. Without a cap an unbounded stream
// would grow memory without limit; 100 is enough recent history for
// the "live ticker" pattern while staying cheap.
const defaultStreamMaxRows = 100

// ApplyStreamLine handles one line from a streaming source bound to a
// non-logview component. Logview-bound streams are handled directly in
// screen.handleStream (just Append the line); this function exists for
// the cases that need parsing + projection.
//
// Two table modes, decided by whether Cfg.RowKey is set:
//
//   - Ring buffer (no RowKey): prepend each event as a new row,
//     trimmed to Cfg.MaxRows. The live-tape pattern — chat, log
//     overlays, recent-trades feeds.
//   - Keyed upsert (RowKey set): each event identifies the row it
//     updates via Cfg.RowKey. Matching key → update in place;
//     unseen key → append. The L1 order-book / status-grid pattern.
//
// In both modes, lines that don't parse as JSON or that project to
// all-empty cells are skipped silently — that's how diagnostic /
// handshake frames ("(connecting to …)", an empty
// subscription_succeeded confirmation) drop out of the live data view.
// Color rules apply per cell exactly as they do on a fetch-driven
// table.
func ApplyStreamLine(c *Component, line string, th theme.Theme) {
	if c == nil || c.Kind != KTable {
		return
	}
	var item any
	if err := json.Unmarshal([]byte(line), &item); err != nil {
		return
	}
	// Either insert mode: short-circuit if every projected cell is
	// empty (a JSON frame that doesn't carry the fields we care
	// about — typically a subscribe-confirmation or heartbeat).
	if !hasAnyContent(item, c.Cfg.Columns) {
		return
	}
	if len(c.Cfg.RowKey) > 0 {
		upsertByKey(c, item)
	} else {
		prependRing(c, item)
	}
	c.Table.SetRows(projectAllRows(c.StreamRows, c.Cfg.Columns, th))
}

// hasAnyContent is the "is this frame interesting?" predicate — true
// iff at least one column's value-path resolves to a non-empty string.
func hasAnyContent(item any, cols []cfg.Column) bool {
	for _, col := range cols {
		if ds.FirstString(item, col.Value) != "" {
			return true
		}
	}
	return false
}

// upsertByKey is the L1 update: deep-merge the incoming event into the
// existing row with the same RowKey, or append the event as a new row
// when the key is new. Cursor stays put because the row at index i in
// the previous render is at index i in the new render when its key
// matches.
//
// Deep merge (not replace) matters when multiple sources contribute to
// the same row — e.g. bookTicker streams bid/ask while aggTrade streams
// last-price + qty for the same symbol. With replace, each frame would
// erase whatever fields the other source had populated; with deep
// merge, each source's fields stay alive and only get updated when the
// arriving frame actually carries them.
func upsertByKey(c *Component, item any) {
	newKey := ds.FirstString(item, c.Cfg.RowKey)
	if newKey == "" {
		return
	}
	for i, existing := range c.StreamRows {
		if ds.FirstString(existing, c.Cfg.RowKey) == newKey {
			c.StreamRows[i] = deepMerge(existing, item)
			return
		}
	}
	c.StreamRows = append(c.StreamRows, item)
}

// deepMerge overlays one parsed-JSON value on top of another. Maps
// recurse — keys that appear in `overlay` win, keys only in `base`
// survive. Anything else (slices, scalars, mixed types) is replaced
// outright by overlay. The result is a new map; the inputs are not
// mutated, so concurrent fetches against the same row don't race.
func deepMerge(base, overlay any) any {
	baseMap, bok := base.(map[string]any)
	overlayMap, ook := overlay.(map[string]any)
	if !bok || !ook {
		return overlay
	}
	out := make(map[string]any, len(baseMap)+len(overlayMap))
	for k, v := range baseMap {
		out[k] = v
	}
	for k, v := range overlayMap {
		if prev, ok := out[k]; ok {
			out[k] = deepMerge(prev, v)
		} else {
			out[k] = v
		}
	}
	return out
}

// prependRing is the live-tape update: newest first, trimmed to
// MaxRows (defaulted from defaultStreamMaxRows when zero).
func prependRing(c *Component, item any) {
	c.StreamRows = append([]any{item}, c.StreamRows...)
	max := c.Cfg.MaxRows
	if max == 0 {
		max = defaultStreamMaxRows
	}
	if max > 0 && len(c.StreamRows) > max {
		c.StreamRows = c.StreamRows[:max]
	}
}

// projectAllRows rebuilds the visible row set from the StreamRows
// buffer. SetRows is cheap and idempotent, so re-projecting on every
// event also keeps cells consistent with any mid-stream theme change.
func projectAllRows(items []any, cols []cfg.Column, th theme.Theme) []table.Row {
	out := make([]table.Row, len(items))
	for i, it := range items {
		cells := make([]string, len(cols))
		for j, col := range cols {
			cells[j] = applyColorRules(ds.FirstString(it, col.Value), col.ColorRules, th)
		}
		out[i] = table.Row(cells)
	}
	return out
}

// ApplyData maps a data source response onto a bound component. Component
// shape and binding fields decide how the response is consumed:
//
//   - list      Cfg.Item       (dot-path to display string)
//   - table     Column.Value   (dot-path per column)
//   - inspector field.Path     (dot-path per field; static Value preserved when Path is empty)
//
// The iterable root is selected via Cfg.Source's root path (already
// applied in screen.Model before this is called) so data is the
// already-rooted value. All three updates happen in place; tuilib's
// SetItems/SetRows/SetFields preserve cursor + filter + InitialDepth
// pre-expansion across refreshes.
func ApplyData(c *Component, data any, th theme.Theme) {
	if c == nil {
		return
	}
	switch c.Kind {
	case KList:
		applyList(c, data, th)
	case KTable:
		applyTable(c, data, th)
	case KInspector:
		applyInspector(c, data, th)
	case KLogview:
		applyLogview(c, data, th)
	case KTree:
		if c.Cfg.Source != "" {
			applyTree(c, data, th)
		}
	case KTextview:
		applyTextview(c, data)
	}
}

// applyTextview coerces a source's response into a single string and
// replaces the textview buffer via SetContent. Handles the shapes tui-
// builder sources emit:
//
//   - string       — format:text bodies (kubectl describe / help pages
//                    / raw markdown) pass through verbatim.
//   - []byte       — same as string.
//   - []any strings — joined with '\n' (matches logview's []any-of-string
//                    convention so a source can back either).
//   - anything else — fmt.Sprint fallback (rare; declared JSON should
//                    prefer type: inspector auto).
//
// tuilib's textview handles its own word-wrap + search + scroll, so we
// don't preprocess the string beyond turning it into a stringified
// form. JSON marshalling with an indent falls out cleanly for maps
// and mixed slices; string/[]byte sources short-circuit past that
// path so a `format: text` exec keeps its raw output.
func applyTextview(c *Component, data any) {
	var s string
	switch x := data.(type) {
	case string:
		s = x
	case []byte:
		s = string(x)
	case []any:
		if allStrings := allStringSlice(x); allStrings != nil {
			// Matches logview's []any-of-strings convention so a source
			// can back either component. Each element becomes a line.
			s = strings.Join(allStrings, "\n")
			break
		}
		s = jsonPretty(data)
	default:
		if data != nil {
			s = jsonPretty(data)
		}
	}
	c.Textview.SetContent(s)
}

// allStringSlice returns items as []string when every element is
// already a string, or nil otherwise. Lets applyTextview keep the
// "each string is a line" fast path without falsely stringifying
// mixed-type slices.
func allStringSlice(items []any) []string {
	out := make([]string, len(items))
	for i, v := range items {
		s, ok := v.(string)
		if !ok {
			return nil
		}
		out[i] = s
	}
	return out
}

// jsonPretty marshals a value with 2-space indent for readable text-
// view rendering. Falls back to fmt.Sprint on marshal failure (rare —
// map[string]any / []any are always marshalable in practice). Used
// for pipeline results whose shape is inherently structured.
func jsonPretty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// applyLogview replaces the logview buffer with the data. Plain strings
// (from format:text sources) are split on \n; a []string from a JSON
// source is used as-is. ColorRules wrap each line individually so
// log-level coloring (`~ERROR` → red, etc.) works regardless of how the
// source delivered the payload.
func applyLogview(c *Component, data any, th theme.Theme) {
	c.Logview.Clear()
	rules := c.Cfg.ColorRules
	var lines []string
	switch x := data.(type) {
	case string:
		lines = strings.Split(strings.TrimRight(x, "\n"), "\n")
	case []any:
		lines = make([]string, 0, len(x))
		for _, v := range x {
			if s, ok := v.(string); ok {
				lines = append(lines, s)
			}
		}
	}
	if len(rules) > 0 {
		for i, ln := range lines {
			lines[i] = applyColorRules(ln, rules, th)
		}
	}
	c.Logview.AppendLines(lines)
}

func applyList(c *Component, data any, th theme.Theme) {
	items := ds.Iter(data)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, applyColorRules(ds.String(it, c.Cfg.Item), c.Cfg.ColorRules, th))
	}
	c.List.SetItems(out)
}

func applyTable(c *Component, data any, th theme.Theme) {
	c.Table.SetRows(TableRows(c, ds.Iter(data), th))
}

// TableRows projects items through a table component's columns —
// dot-path pluck per column, then color rules. Shared by the whole-set
// path (applyTable) and the windowed path (ApplyWindow) so a window's
// rows are built exactly like any other row.
func TableRows(c *Component, items []any, th theme.Theme) []table.Row {
	rows := make([]table.Row, 0, len(items))
	for _, it := range items {
		cells := make([]string, len(c.Cfg.Columns))
		for i, col := range c.Cfg.Columns {
			cells[i] = applyColorRules(ds.FirstString(it, col.Value), col.ColorRules, th)
		}
		rows = append(rows, table.Row(cells))
	}
	return rows
}

// ApplyWindow installs one window of a larger remote set: items are the
// logical rows [offset, offset+len(items)) of a set total rows long, with
// total < 0 meaning the source can't say.
//
// This is the windowed counterpart to ApplyData, and it is deliberately
// separate rather than a branch inside it. SetRows means "these are all
// the rows there are" — it clears any window and lets the table filter
// and sort what it holds. Routing a window through it would quietly turn
// a 30,000-row set into a 100-row one.
func ApplyWindow(c *Component, items []any, offset, total int, th theme.Theme) {
	if c.Kind != KTable {
		return
	}
	c.Table.SetWindow(TableRows(c, items, th), offset, total)
}

// applyTree turns a source's response into a live tree via
// tuilib.tree.SetRoot. Feature A from the tui-builder integration
// batch — the "namespace → resource kind → resource name" pattern.
//
// Modes:
//   - Cfg.Children set  → recursively walk each node's children by
//     dot-path; produces a nested tree matching the source's own
//     shape (filesystem walks, org charts, k8s ownerReferences).
//   - Cfg.GroupBy empty → every item becomes a direct child of the
//     root, labeled via Cfg.Label.
//   - Cfg.GroupBy set   → items bucket by their GroupBy value; each
//     bucket becomes a parent node whose label IS the bucket value,
//     items land under it labeled via Cfg.Label. Bucket order is
//     first-appearance so a stable API response yields a stable tree.
//
// The root's label matches sourceTreeRootLabel(cfg) — the same string
// buildTree seeded so tuilib's expand state carries across refreshes.
func applyTree(c *Component, data any, th theme.Theme) {
	rules := c.Cfg.ColorRules
	label := func(it any) string {
		return applyColorRules(ds.FirstString(it, c.Cfg.Label), rules, th)
	}
	root := &yamlNode{label: sourceTreeRootLabel(c.Cfg)}
	// Recursive-children mode. Two shapes:
	//   - single map (one top-level node)  → use it AS root; the
	//     synthetic wrapper would double up on the path (root "./" +
	//     response "./cmd" → "././cmd"). ${cursor.path} needs the
	//     labels to concatenate to a real string.
	//   - []any of nodes (a forest)        → wrap in a synthetic root
	//     labeled sourceTreeRootLabel(cfg) because a tree Node can't
	//     have multiple roots.
	if len(c.Cfg.Children) != 0 {
		switch d := data.(type) {
		case []any:
			for _, top := range d {
				if child := walkTreeNode(top, c.Cfg.Label, c.Cfg.Children, label); child != nil {
					root.children = append(root.children, child)
				}
			}
			c.Tree.SetRoot(root)
		default:
			single := walkTreeNode(data, c.Cfg.Label, c.Cfg.Children, label)
			if single == nil {
				single = root
			}
			c.Tree.SetRoot(single)
		}
		return
	}
	items := ds.Iter(data)
	if len(c.Cfg.GroupBy) == 0 {
		for _, it := range items {
			root.children = append(root.children, &yamlNode{label: label(it)})
		}
		c.Tree.SetRoot(root)
		return
	}
	// Bucket by GroupBy value. Preserve first-appearance order via a
	// parallel slice; the map is just for lookup during the walk.
	type bucket struct {
		node *yamlNode
	}
	buckets := map[string]*bucket{}
	var order []string
	for _, it := range items {
		key := ds.FirstString(it, c.Cfg.GroupBy)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{node: &yamlNode{label: key}}
			buckets[key] = b
			order = append(order, key)
		}
		b.node.children = append(b.node.children, &yamlNode{label: label(it)})
	}
	for _, k := range order {
		root.children = append(root.children, buckets[k].node)
	}
	c.Tree.SetRoot(root)
}

// walkTreeNode recurses through a source-supplied nested structure,
// building a yamlNode tree. label pulls the visible label off each
// node (already color-wrapped by the caller); childrenPath picks the
// child slice via the standard fallback-chain semantics. A node with
// a missing or empty children value is a leaf. Returns nil on
// nil / non-map input so callers can safely accumulate.
func walkTreeNode(v any, labelPath, childrenPath cfg.Path, label func(any) string) *yamlNode {
	if v == nil {
		return nil
	}
	node := &yamlNode{label: label(v)}
	var kids any
	for _, p := range childrenPath {
		if got := ds.Get(v, p); got != nil {
			kids = got
			break
		}
	}
	for _, ch := range ds.Iter(kids) {
		if sub := walkTreeNode(ch, labelPath, childrenPath, label); sub != nil {
			node.children = append(node.children, sub)
		}
	}
	return node
}

func applyInspector(c *Component, data any, th theme.Theme) {
	c.Inspector.SetFields(deriveInspectorFields(c.Cfg, data, th))
}

// deriveInspectorFields picks between the two field-generation modes.
// Auto walks the fetched value via inspector.FromAny (feature B — dynamic
// shape, C — nested rendering with no map[...] stringification). Declared
// mode goes through buildInspectorFields which honors config-declared
// labels + paths + color rules.
func deriveInspectorFields(cfg *cfg.Component, data any, th theme.Theme) []inspector.Field {
	if cfg.Auto {
		return inspector.FromAny(data)
	}
	return buildInspectorFields(cfg.Fields, data, th)
}

// buildInspectorFields walks the config field tree, plucking each field's
// Path from data when Path is set; otherwise it keeps the static Value.
// Per-field ColorRules wrap the rendered value with ansi.CellColor.
func buildInspectorFields(in []cfg.InspectorField, data any, th theme.Theme) []inspector.Field {
	if len(in) == 0 {
		return nil
	}
	out := make([]inspector.Field, len(in))
	for i, f := range in {
		v := f.Value
		if f.Path != "" {
			v = ds.String(data, f.Path)
		}
		v = applyColorRules(v, f.ColorRules, th)
		out[i] = inspector.Field{
			Label:    f.Label,
			Value:    v,
			Children: buildInspectorFields(f.Children, data, th),
		}
	}
	return out
}
