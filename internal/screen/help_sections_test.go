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

// Config-authored verbs get their own heading, defaulting to "Actions".
func TestActionsGetTheirOwnSection(t *testing.T) {
	m := helpModel(t, []cfg.ActionBinding{
		{Key: "d", Action: "describe", Label: "describe"},
	})
	got := titles(t, m)
	if !has(got, "Actions") {
		t.Fatalf("actions should land under their own heading, got %v", got)
	}
	// And under it, the action's own label.
	for _, s := range m.HelpSections() {
		if s.Title != "Actions" {
			continue
		}
		if len(s.Bindings) != 1 || s.Bindings[0].Help().Desc != "describe" {
			t.Errorf("Actions section = %+v, want one binding labelled describe", s.Bindings)
		}
	}
}

// `section:` overrides the default heading, so a config can file a verb
// next to the component keys it belongs with.
func TestActionSectionOverridesTheDefault(t *testing.T) {
	m := helpModel(t, []cfg.ActionBinding{
		{Key: "d", Action: "describe", Label: "describe", Section: "Inspect"},
	})
	got := titles(t, m)
	if !has(got, "Inspect") {
		t.Errorf("custom section: not applied, got %v", got)
	}
	if has(got, "Actions") {
		t.Errorf("default heading should be replaced, not added to: %v", got)
	}
}

// Two verbs naming the same section share one heading, in the order the
// config lists them.
func TestActionsShareASection(t *testing.T) {
	m := helpModel(t, []cfg.ActionBinding{
		{Key: "d", Action: "describe", Label: "describe", Section: "Inspect"},
		{Key: "l", Action: "logs", Label: "logs", Section: "Inspect"},
	})
	for _, s := range m.HelpSections() {
		if s.Title != "Inspect" {
			continue
		}
		if len(s.Bindings) != 2 {
			t.Fatalf("want both verbs under one heading, got %d", len(s.Bindings))
		}
		if s.Bindings[0].Help().Desc != "describe" || s.Bindings[1].Help().Desc != "logs" {
			t.Errorf("config order not preserved: %+v", s.Bindings)
		}
		return
	}
	t.Fatal("no Inspect section")
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
