package build

import (
	"strconv"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/list"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// Marking. tuilib holds marks by key rather than by index, so the whole
// job on this side is supplying a key per row that means the same thing
// before and after a poll. Where it comes from depends on the kind:
//
//   - tree    a node's path, which tuilib already uses for expansion
//             state. Nothing to do here.
//   - list    the item's display string. Duplicates collapse onto one
//             mark, which is the right answer for a list of names.
//   - table   `mark_key:` read from the ORIGINAL source item, so the
//             identity can be a field the table never shows.
//
// Static rows are the exception: they are fixed at load, so their
// position cannot drift and the index is a legitimate key.

// keyedItems keys a list on its own display strings.
func keyedItems(items []string) []list.KeyedItem {
	out := make([]list.KeyedItem, len(items))
	for i, s := range items {
		// Strip before keying: color_rules wrap the display text in
		// escapes, and a key carrying them would change the moment a
		// rule's condition flipped, silently dropping the mark.
		out[i] = list.KeyedItem{Key: xansi.Strip(s), Display: s}
	}
	return out
}

// indexKeyedRows keys static table rows on their position.
func indexKeyedRows(rows []table.Row) []table.KeyedRow {
	out := make([]table.KeyedRow, len(rows))
	for i, r := range rows {
		out[i] = table.KeyedRow{Key: strconv.Itoa(i), Cells: r}
	}
	return out
}

// KeyedTableRows projects items exactly as TableRows does, then pairs
// each row with the identity at Cfg.MarkKey and the Selection that key
// resolves back to.
//
// The map is what makes a mark usable: tuilib hands back keys, and
// every consumer downstream wants the row. Building it in the same pass
// that renders the cells is what keeps the two from disagreeing.
func KeyedTableRows(c *Component, items []any, th theme.Theme) ([]table.KeyedRow, map[string]Selection) {
	rows := TableRows(c, items, th)
	titles := make([]string, 0, len(c.Cfg.Columns))
	for _, col := range c.Cfg.Columns {
		titles = append(titles, col.Title)
	}

	keyed := make([]table.KeyedRow, len(rows))
	byKey := make(map[string]Selection, len(rows))
	for i, r := range rows {
		key := ds.FirstString(items[i], c.Cfg.MarkKey)
		keyed[i] = table.KeyedRow{Key: key, Cells: r}
		byKey[key] = selectionFromCells(r, titles)
	}
	return keyed, byKey
}

// selectionFromCells builds the Selection a ${selection.*} substitution
// expects. Cells are ANSI-stripped for the same reason the screen's own
// selection path strips them: color_rules styling lives in the view,
// never in a value a URL or argv is built from.
func selectionFromCells(row table.Row, titles []string) Selection {
	cells := make([]string, len(row))
	for i, raw := range row {
		cells[i] = xansi.Strip(raw)
	}
	first := ""
	if len(cells) > 0 {
		first = cells[0]
	}
	return Selection{String: first, Cells: cells, Columns: titles}
}

// MarkedSelections resolves a component's live selection into rows.
//
// It follows tuilib's Selection() contract rather than reading marks
// directly: marked rows when there are any, otherwise the cursor row.
// That is deliberate — a hand-written "if marked, else cursor" branch
// is how a verb quietly acts on one row when the user marked six.
//
// Returns nil for kinds that carry no row identity, and for a markable
// table whose keys have not been installed yet (no fetch has landed).
func MarkedSelections(c *Component) []Selection {
	if c == nil {
		return nil
	}
	switch c.Kind {
	case KList:
		keys := c.List.Selection()
		out := make([]Selection, 0, len(keys))
		for _, k := range keys {
			out = append(out, Selection{String: k})
		}
		return out
	case KTable:
		keys := c.Table.Selection()
		out := make([]Selection, 0, len(keys))
		for _, k := range keys {
			if sel, ok := c.RowsByKey[k]; ok {
				out = append(out, sel)
			}
		}
		return out
	}
	return nil
}

// MarkedCount is how many rows a verb would act on, for a confirm
// string or a menu title. Mirrors MarkedSelections, so the number the
// user is shown and the set that runs cannot disagree.
func MarkedCount(c *Component) int { return len(MarkedSelections(c)) }

// markable reports whether the config asked for marks on a kind that
// can carry them. The validator has already rejected the combinations
// that can't, so this is a cheap guard rather than a second opinion.
func markable(c *cfg.Component) bool { return c != nil && c.Markable }
