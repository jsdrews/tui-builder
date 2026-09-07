package build

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func markableTable(t *testing.T) *Component {
	t.Helper()
	c, err := NewComponent(&cfg.Component{
		Type:       "table",
		Source:     "pods",
		Markable:   true,
		Filterable: true,
		MarkKey:    cfg.Path{"uid"},
		Columns: []cfg.Column{
			{Title: "Name", Value: cfg.Path{"name"}},
			{Title: "Status", Value: cfg.Path{"status"}},
		},
	}, theme.Nord())
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}
	return c
}

func pods(entries ...[3]string) []any {
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = map[string]any{"uid": e[0], "name": e[1], "status": e[2]}
	}
	return out
}

// The reason marks are held by key at all: a poll that reorders rows
// must not slide the selection onto whatever now occupies that index.
func TestMarksSurviveRowReorder(t *testing.T) {
	c := markableTable(t)
	th := theme.Nord()
	ApplyData(c, pods(
		[3]string{"u1", "web", "Running"},
		[3]string{"u2", "api", "Running"},
		[3]string{"u3", "db", "Running"},
	), th)

	c.Table.SetCursor(1) // api
	c.Table.ToggleMark()
	if got := c.Table.Marks(); len(got) != 1 || got[0] != "u2" {
		t.Fatalf("marks after toggle = %v, want [u2]", got)
	}

	// Next poll returns the same pods in a different order, and one of
	// them has changed status — the volatile-column case.
	ApplyData(c, pods(
		[3]string{"u3", "db", "Running"},
		[3]string{"u2", "api", "CrashLoopBackOff"},
		[3]string{"u1", "web", "Running"},
	), th)

	got := c.Table.Marks()
	if len(got) != 1 || got[0] != "u2" {
		t.Fatalf("marks after reorder = %v, want [u2] — the mark followed an index, not a key", got)
	}
	sels := MarkedSelections(c)
	if len(sels) != 1 || sels[0].String != "api" {
		t.Fatalf("selection after reorder = %+v, want the api row", sels)
	}
	// And it resolves to the row's CURRENT cells, not the ones it had
	// when it was marked.
	if len(sels[0].Cells) != 2 || sels[0].Cells[1] != "CrashLoopBackOff" {
		t.Errorf("selection cells = %v, want the refreshed status", sels[0].Cells)
	}
}

// A key doesn't care whether its row is on screen, so a mark survives
// being filtered away. Genuinely surprising, and worth pinning.
func TestMarksSurviveFilter(t *testing.T) {
	c := markableTable(t)
	ApplyData(c, pods(
		[3]string{"u1", "web", "Running"},
		[3]string{"u2", "api", "Running"},
	), theme.Nord())

	c.Table.SetCursor(1)
	c.Table.ToggleMark()
	c.Table.SetValue("web") // filters the marked row away

	if got := c.Table.MarkCount(); got != 1 {
		t.Fatalf("MarkCount under a filter = %d, want 1", got)
	}
	sels := MarkedSelections(c)
	if len(sels) != 1 || sels[0].String != "api" {
		t.Fatalf("filtered-away mark should still resolve, got %+v", sels)
	}
}

// tuilib rule 4: a theme swap rebuilds the component, so anything the
// user put there has to be carried across by hand.
//
// Rebuild does not re-deliver a source-bound table's rows — they came
// from a fetch it can't repeat — so the rebuilt table is momentarily
// empty and Marks(), which reports only keys the table still holds,
// returns nothing. MarkCount is the honest assertion here: it counts the
// stored key set, which is what has to survive.
func TestMarksSurviveThemeRebuild(t *testing.T) {
	c := markableTable(t)
	ApplyData(c, pods(
		[3]string{"u1", "web", "Running"},
		[3]string{"u2", "api", "Running"},
	), theme.Nord())
	c.Table.SetCursor(0)
	c.Table.ToggleMark()

	c.Rebuild(theme.Dark())

	if got := c.Table.MarkCount(); got != 1 {
		t.Fatalf("MarkCount after rebuild = %d, want 1 — the key set was dropped", got)
	}
}

// Marks are a key set, so they outlive the rows and reattach when the
// next fetch reinstalls the keys they name.
func TestMarksReattachAfterRebuildAndRefetch(t *testing.T) {
	c := markableTable(t)
	th := theme.Nord()
	ApplyData(c, pods([3]string{"u1", "web", "Running"}), th)
	c.Table.ToggleMark()

	c.Rebuild(theme.Dark())
	ApplyData(c, pods(
		[3]string{"u0", "cache", "Running"},
		[3]string{"u1", "web", "Running"},
	), theme.Dark())

	sels := MarkedSelections(c)
	if len(sels) != 1 || sels[0].String != "web" {
		t.Fatalf("mark should reattach to u1 after refetch, got %+v", sels)
	}
}

// tuilib's Selection() contract: marked rows if any, else the cursor
// row. Mirroring it is what stops a verb acting on one row when the
// user marked six.
func TestMarkedSelectionsFallsBackToCursor(t *testing.T) {
	c := markableTable(t)
	ApplyData(c, pods(
		[3]string{"u1", "web", "Running"},
		[3]string{"u2", "api", "Running"},
	), theme.Nord())

	c.Table.SetCursor(1)
	sels := MarkedSelections(c)
	if len(sels) != 1 || sels[0].String != "api" {
		t.Fatalf("with nothing marked, want the cursor row, got %+v", sels)
	}
	if got := MarkedCount(c); got != 1 {
		t.Errorf("MarkedCount = %d, want 1", got)
	}

	c.Table.SetCursor(0)
	c.Table.ToggleMark()
	c.Table.SetCursor(1)
	c.Table.ToggleMark()
	if got := MarkedCount(c); got != 2 {
		t.Fatalf("MarkedCount with two marks = %d, want 2", got)
	}
	sels = MarkedSelections(c)
	if len(sels) != 2 || sels[0].String != "web" || sels[1].String != "api" {
		t.Errorf("marked selections = %+v, want web then api in row order", sels)
	}
}

// A ${selection.*} substitution must see the logical value, never the
// color_rules escapes wrapping it in the view.
func TestMarkedSelectionsStripAnsi(t *testing.T) {
	c, err := NewComponent(&cfg.Component{
		Type: "table", Source: "pods", Markable: true, MarkKey: cfg.Path{"uid"},
		Columns: []cfg.Column{{
			Title: "Status", Value: cfg.Path{"status"},
			ColorRules: []cfg.ColorRule{{When: `value == "Running"`, Color: "green"}},
		}},
	}, theme.Nord())
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}
	ApplyData(c, pods([3]string{"u1", "web", "Running"}), theme.Nord())

	sels := MarkedSelections(c)
	if len(sels) != 1 {
		t.Fatalf("want one selection, got %+v", sels)
	}
	if sels[0].String != "Running" {
		t.Errorf("selection = %q, want the unstyled value", sels[0].String)
	}
}

// Marking off must leave the old path exactly as it was.
func TestNonMarkableTableKeepsPlainRows(t *testing.T) {
	c, err := NewComponent(&cfg.Component{
		Type: "table", Source: "pods",
		Columns: []cfg.Column{{Title: "Name", Value: cfg.Path{"name"}}},
	}, theme.Nord())
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}
	ApplyData(c, pods([3]string{"u1", "web", "Running"}), theme.Nord())

	if c.RowsByKey != nil {
		t.Error("a non-markable table should not build a key map")
	}
	if c.Table.Markable() {
		t.Error("table reports markable without the config asking")
	}
	// Without keys there is nothing for a key to resolve to, which is
	// exactly why marking is off: the existing single-row selection
	// path (screen.selectionFrom) serves these components instead.
	if got := MarkedSelections(c); len(got) != 0 {
		t.Errorf("a keyless table should resolve no selections, got %+v", got)
	}
}

// Options.Items seeds display strings but no keys, so a static markable
// list has to go back through the keyed setter or its gutter is inert.
func TestStaticListIsMarkable(t *testing.T) {
	c, err := NewComponent(&cfg.Component{
		Type: "list", Markable: true,
		Items: []string{"alpha", "beta", "gamma"},
	}, theme.Nord())
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}
	c.List.SetCursor(1)
	c.List.ToggleMark()
	if got := c.List.Marks(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("static list marks = %v, want [beta]", got)
	}
	sels := MarkedSelections(c)
	if len(sels) != 1 || sels[0].String != "beta" {
		t.Errorf("static list selection = %+v, want beta", sels)
	}
}

// Same for a static table, keyed on position — the rows are fixed at
// load, so nothing can reorder them out from under a mark.
func TestStaticTableIsMarkable(t *testing.T) {
	c, err := NewComponent(&cfg.Component{
		Type: "table", Markable: true,
		Columns: []cfg.Column{{Title: "Name"}},
		Rows:    [][]any{{"alpha"}, {"beta"}},
	}, theme.Nord())
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}
	c.List = nil
	c.Table.SetCursor(1)
	c.Table.ToggleMark()
	if got := c.Table.Marks(); len(got) != 1 || got[0] != "1" {
		t.Fatalf("static table marks = %v, want [1]", got)
	}
}

// A source-bound markable list keys on its item string.
func TestSourceListIsMarkable(t *testing.T) {
	c, err := NewComponent(&cfg.Component{
		Type: "list", Source: "pods", Item: "name", Markable: true,
	}, theme.Nord())
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}
	ApplyData(c, pods(
		[3]string{"u1", "web", "Running"},
		[3]string{"u2", "api", "Running"},
	), theme.Nord())

	c.List.SetCursor(1)
	c.List.ToggleMark()
	// A poll reorders them; the mark tracks the name, not the slot.
	ApplyData(c, pods(
		[3]string{"u2", "api", "Running"},
		[3]string{"u1", "web", "Running"},
	), theme.Nord())
	if got := c.List.Marks(); len(got) != 1 || got[0] != "api" {
		t.Fatalf("list marks after reorder = %v, want [api]", got)
	}
}

// The tree keys on a node's path, which it already maintains for
// expansion state — so marking there needs nothing from us but the flag.
func TestTreeIsMarkable(t *testing.T) {
	c, err := NewComponent(&cfg.Component{
		Type: "tree", Markable: true, InitialDepth: 2,
		Root: &cfg.TreeNode{Label: "root", Children: []*cfg.TreeNode{
			{Label: "a"}, {Label: "b"},
		}},
	}, theme.Nord())
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}
	if !c.Tree.Markable() {
		t.Fatal("tree should report markable")
	}
	c.Tree.SetCursor(1)
	c.Tree.ToggleMark()
	if got := c.Tree.Marks(); len(got) != 1 {
		t.Fatalf("tree marks = %v, want one", got)
	}
	marks := c.Tree.Marks()
	c.Rebuild(theme.Dark())
	if got := c.Tree.Marks(); len(got) != len(marks) {
		t.Errorf("tree marks lost across rebuild: %v -> %v", marks, got)
	}
}
