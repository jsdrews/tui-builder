package screen

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// helpModel builds a one-table screen with the given action bindings,
// each resolved against a one-entry registry.
func helpModel(t *testing.T, bindings []cfg.ActionBinding) *Model {
	t.Helper()
	comps := map[string]*cfg.Component{
		"pods": {
			Type: "table", Source: "pods", Filterable: true,
			Markable: true, MarkKey: cfg.Path{"uid"},
			Columns: []cfg.Column{{Title: "Name", Value: cfg.Path{"name"}, Sortable: true}},
		},
	}
	sources := map[string]*cfg.Source{
		"pods": cfg.NewEntry(&cfg.Source{Type: "static", Data: []any{}}),
	}
	registry := map[string]*cfg.Action{}
	for _, b := range bindings {
		registry[b.Action] = &cfg.Action{Run: []string{"echo", b.Action}}
	}
	sc := &cfg.Screen{Title: "Pods", Layout: cfg.Node{Component: "pods"}, Actions: bindings}
	m, err := New(sc, comps, sources, registry, theme.Nord())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func titles(t *testing.T, m *Model) []string {
	t.Helper()
	var out []string
	for _, s := range m.HelpSections() {
		out = append(out, s.Title)
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The bug this fixes: with no HelpSections the shell wrapped Help() in
// one unnamed group and titled it with the owner, so the screen's name
// sat above every binding — "Pods" over a table's scroll keys.
func TestHelpSectionsAreFunctionalNotOwnerNamed(t *testing.T) {
	got := titles(t, helpModel(t, nil))
	if len(got) == 0 {
		t.Fatal("no sections — the overlay would fall back to one owner-named group")
	}
	if has(got, "Pods") {
		t.Errorf("a section is named after the screen, not what its keys do: %v", got)
	}
	for _, want := range []string{"Navigate", "Filter", "Sort", "Select"} {
		if !has(got, want) {
			t.Errorf("missing the %q group; got %v", want, got)
		}
	}
}

// Marking is on, so the component contributes Select. Turning it off
// must drop the heading rather than leave an empty one.
func TestHelpSectionsOmitUnconfiguredGroups(t *testing.T) {
	comps := map[string]*cfg.Component{
		"pods": {
			Type: "table", Source: "pods",
			Columns: []cfg.Column{{Title: "Name", Value: cfg.Path{"name"}}},
		},
	}
	sources := map[string]*cfg.Source{
		"pods": cfg.NewEntry(&cfg.Source{Type: "static", Data: []any{}}),
	}
	m, err := New(&cfg.Screen{Title: "Pods", Layout: cfg.Node{Component: "pods"}}, comps, sources, nil, theme.Nord())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := titles(t, m)
	for _, unwanted := range []string{"Select", "Filter", "Sort"} {
		if has(got, unwanted) {
			t.Errorf("%q should not appear on an unmarkable, unfilterable, unsortable table: %v", unwanted, got)
		}
	}
}

// Actions are menu verbs now, so they are deliberately absent from the
// overlay — tuilib keeps a menu shortcut out of Help() because moving
// discovery off the footer and into the menu is most of the point, and a
// footer listing nine verbs it can no longer fire would be worse than
// listing none.
func TestMenuOnlyActionsAreNotInTheOverlay(t *testing.T) {
	m := helpModel(t, []cfg.ActionBinding{
		{Key: "d", Action: "describe", Label: "describe"},
	})
	got := titles(t, m)
	if has(got, "Actions") || has(got, "describe") {
		t.Errorf("a menu-only verb should not appear in the key overlay: %v", got)
	}
}

// `enter` is the exception: it is still a real direct key, so it stays
// discoverable, under its own heading and with the binding's label.
func TestEnterVerbIsInTheOverlay(t *testing.T) {
	m := helpModel(t, []cfg.ActionBinding{
		{Key: "enter", Action: "open", Label: "open detail"},
	})
	if !has(titles(t, m), "Open") {
		t.Fatalf("the enter verb should be listed: %v", titles(t, m))
	}
	for _, sec := range m.HelpSections() {
		if sec.Title != "Open" {
			continue
		}
		if len(sec.Bindings) != 1 || sec.Bindings[0].Help().Desc != "open detail" {
			t.Errorf("Open section = %+v, want the binding's label", sec.Bindings)
		}
		return
	}
}

// The flat strip and the grouped overlay must agree about which keys
// exist — they share verbSections precisely so they can't drift.
func TestHelpAndHelpSectionsAgree(t *testing.T) {
	m := helpModel(t, []cfg.ActionBinding{
		{Key: "d", Action: "describe", Label: "describe"},
	})
	flat := len(m.Help())
	grouped := 0
	for _, s := range m.HelpSections() {
		grouped += len(s.Bindings)
	}
	if flat != grouped {
		t.Errorf("Help() has %d bindings, HelpSections() has %d", flat, grouped)
	}
}
