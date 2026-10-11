package build

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// A theme swap rebuilds every component (tuilib rule 4). Data a source
// delivered has to come through it: a one-shot source never fetches again
// on its own, so anything dropped here stays blank until the user thinks
// to press r.
func TestFetchedDataSurvivesThemeRebuild(t *testing.T) {
	th := theme.Nord()

	tbl := markableTable(t)
	ApplyData(tbl, pods(
		[3]string{"u1", "web", "Running"},
		[3]string{"u2", "api", "Pending"},
	), th)
	tbl.Table.SetValue("api")
	tbl.Table.SetCursor(0)
	tbl.Table.ToggleMark()

	lst, err := NewComponent(&cfg.Component{Type: "list", Source: "pods", Item: "name"}, th)
	if err != nil {
		t.Fatal(err)
	}
	ApplyData(lst, pods([3]string{"u1", "web", ""}, [3]string{"u2", "api", ""}), th)
	lst.List.SetCursor(1)

	ins, err := NewComponent(&cfg.Component{Type: "inspector", Source: "pod", Auto: true}, th)
	if err != nil {
		t.Fatal(err)
	}
	ApplyData(ins, map[string]any{"name": "web", "phase": "Running"}, th)

	txt, err := NewComponent(&cfg.Component{Type: "textview", Source: "doc"}, th)
	if err != nil {
		t.Fatal(err)
	}
	ApplyData(txt, "line one\nline two", th)

	for _, c := range []*Component{tbl, lst, ins, txt} {
		c.Rebuild(theme.Dark())
	}

	if got := len(tbl.Table.Rows()); got != 2 {
		t.Errorf("table holds %d rows after rebuild, want 2", got)
	}
	if got := len(tbl.Table.Visible()); got != 1 {
		t.Errorf("table shows %d rows after rebuild, want the 1 matching its filter", got)
	}
	if got := tbl.Table.Value(); got != "api" {
		t.Errorf("table filter = %q after rebuild, want %q", got, "api")
	}
	if sels := MarkedSelections(tbl); len(sels) != 1 || sels[0].String != "api" {
		t.Errorf("table marks after rebuild = %+v, want the api row", sels)
	}
	if got := len(lst.List.Items()); got != 2 {
		t.Errorf("list holds %d items after rebuild, want 2", got)
	}
	if got := lst.List.Cursor(); got != 1 {
		t.Errorf("list cursor = %d after rebuild, want 1", got)
	}
	if f, ok := ins.Inspector.Selected(); !ok || f.Label == "" {
		t.Error("inspector lost its fields across rebuild")
	}
	if got := txt.Textview.Content(); got != "line one\nline two" {
		t.Errorf("textview content = %q after rebuild", got)
	}
}

// A windowed table holds one page of a larger set, and nothing re-delivers
// it after a rebuild: the source only refetches on a scroll or a filter.
func TestWindowSurvivesThemeRebuild(t *testing.T) {
	th := theme.Nord()
	c, err := NewComponent(&cfg.Component{
		Type:     "table",
		Source:   "books",
		Windowed: true,
		Columns:  []cfg.Column{{Title: "Title", Value: cfg.Path{"title"}}},
	}, th)
	if err != nil {
		t.Fatal(err)
	}
	items := []any{map[string]any{"title": "Dune"}, map[string]any{"title": "Emma"}}
	answered := table.Answer{Raw: "author:herbert"}
	ApplyWindow(c, items, 40, 900, answered, th)

	c.Rebuild(theme.Dark())

	off, n, total := c.Table.Window()
	if off != 40 || n != 2 || total != 900 {
		t.Errorf("window after rebuild = (%d, %d, %d), want (40, 2, 900)", off, n, total)
	}
	if got, ok := c.Table.Answered(); !ok || got.Raw != answered.Raw {
		t.Errorf("answered query after rebuild = %+v, %v; want %q", got, ok, answered.Raw)
	}
}
