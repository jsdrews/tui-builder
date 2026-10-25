package build

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"

	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// withAppChrome sets the app-wide settings for one test and puts the
// zero value (tuilib's defaults) back afterwards.
func withAppChrome(t *testing.T, a *cfg.App) {
	t.Helper()
	SetAppChrome(a)
	t.Cleanup(func() { SetAppChrome(&cfg.App{}) })
}

func TestAppKeysRemapEveryListTableAndTree(t *testing.T) {
	withAppChrome(t, &cfg.App{Keys: map[string]cfg.KeyList{
		"filter":   {"F"},
		"sort_dir": {"S"},
	}})
	th := theme.New(palette.Nord())

	tbl := tableOptions(&cfg.Component{Type: "table"}, th)
	if got := tbl.Keys.Filter.Keys(); len(got) != 1 || got[0] != "F" {
		t.Errorf("table filter keys = %v, want [F]", got)
	}
	if got := tbl.Keys.SortDir.Keys(); len(got) != 1 || got[0] != "S" {
		t.Errorf("table sort_dir keys = %v, want [S]", got)
	}
	if got := tbl.Keys.Filter.Help().Desc; got == "" {
		t.Error("a remap should keep the binding's help description")
	}
	// Bindings the config doesn't name keep tuilib's keys.
	if got := tbl.Keys.Up.Keys(); len(got) != 2 || got[1] != "k" {
		t.Errorf("table up keys = %v, want tuilib's [up k]", got)
	}

	lst := buildList(&cfg.Component{Type: "list", Filterable: true}, th)
	if got := lst.Help(); !hasKey(got, "F") {
		t.Error("list help should list the remapped filter key F")
	}
	tr := buildTree(&cfg.Component{Type: "tree", Searchable: true}, th)
	if got := tr.Help(); !hasKey(got, "F") {
		t.Error("tree help should list F for its search (a tree's `/`)")
	}
}

// hasKey reports whether any binding in a component's help fires on k.
func hasKey(bindings []key.Binding, k string) bool {
	for _, b := range bindings {
		for _, bk := range b.Keys() {
			if bk == k {
				return true
			}
		}
	}
	return false
}

func TestFormOptionsAndMaskFollowTheApp(t *testing.T) {
	th := theme.New(palette.Nord())
	if FormOptions(th).FieldSpacing != nil || MaskChar() != 0 {
		t.Fatal("unset app settings should leave tuilib's defaults")
	}
	withAppChrome(t, &cfg.App{FormSpacing: 2, Glyphs: cfg.Glyphs{Mask: "•"}})
	if sp := FormOptions(th).FieldSpacing; sp == nil || *sp != 2 {
		t.Errorf("FieldSpacing = %v, want 2", sp)
	}
	if got := MaskChar(); got != '•' {
		t.Errorf("MaskChar = %q, want •", got)
	}
}

func TestActivityGlyphsReachEveryTheme(t *testing.T) {
	a := &cfg.App{Glyphs: cfg.Glyphs{ActivityOK: "+", ActivityFail: "!", ActivityChanged: "~"}}
	for _, p := range palette.All() {
		g := chromeTheme(t, p, a).Glyphs
		if g.ActivityOK != "+" || g.ActivityFail != "!" || g.ActivityChanged != "~" {
			t.Errorf("%s: activity glyphs = %q %q %q", p.Name, g.ActivityOK, g.ActivityFail, g.ActivityChanged)
		}
	}
}
