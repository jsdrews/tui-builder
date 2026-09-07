package screen

import (
	"strings"
	"testing"

	taction "github.com/jsdrews/tuilib/pkg/action"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// menuModel builds a two-pane screen so `from:` scoping is observable,
// with the given bindings and registry.
func menuModel(t *testing.T, bindings []cfg.ActionBinding, reg map[string]*cfg.Action) *Model {
	t.Helper()
	comps := map[string]*cfg.Component{
		"pods":  {Type: "list", Source: "items", Item: "name"},
		"nodes": {Type: "list", Source: "items", Item: "name"},
	}
	sources := map[string]*cfg.Source{
		"items": cfg.NewEntry(&cfg.Source{Type: "static", Data: []any{
			map[string]any{"name": "web"},
			map[string]any{"name": "api"},
		}}),
	}
	sc := &cfg.Screen{
		Title: "Pods",
		Layout: cfg.Node{HStack: []cfg.Item{
			{Node: cfg.Node{Component: "pods"}},
			{Node: cfg.Node{Component: "nodes"}},
		}},
		Actions: bindings,
	}
	m, err := New(sc, comps, sources, reg, theme.Nord())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func find(s taction.Set, label string) (taction.Action, bool) {
	for _, a := range s.Actions {
		if a.Label == label {
			return a, true
		}
	}
	return taction.Action{}, false
}

// Every binding appears, labelled, with the registry's description as
// the gloss.
func TestActionsListsEveryBinding(t *testing.T) {
	m := menuModel(t,
		[]cfg.ActionBinding{
			{Key: "d", Action: "describe", Label: "describe"},
			{Key: "l", Action: "logs"},
		},
		map[string]*cfg.Action{
			"describe": {Run: []string{"echo", "d"}, Description: "Describe it"},
			"logs":     {Run: []string{"echo", "l"}},
		})

	set := m.Actions()
	if len(set.Actions) != 2 {
		t.Fatalf("got %d actions, want 2: %+v", len(set.Actions), set.Actions)
	}
	a, ok := find(set, "describe")
	if !ok {
		t.Fatal("describe missing")
	}
	if a.Desc != "Describe it" {
		t.Errorf("Desc = %q, want the registry description", a.Desc)
	}
	// An unlabelled binding falls back to the action name.
	if _, ok := find(set, "logs"); !ok {
		t.Errorf("an unlabelled binding should fall back to its action name: %+v", set.Actions)
	}
}

// Showing an unavailable verb with a reason beats hiding it: hidden, the
// user learns the verb doesn't exist.
func TestActionScopedToAnotherPaneIsDisabledNotHidden(t *testing.T) {
	m := menuModel(t,
		[]cfg.ActionBinding{{
			Key: "d", Action: "describe", Label: "describe",
			From: "nodes", Bind: map[string]string{"n": "${selection}"},
		}},
		map[string]*cfg.Action{
			"describe": {Run: []string{"echo", "${inputs.n}"},
				Inputs: map[string]*cfg.Parameter{"n": {}}},
		})

	// Focus starts on the first pane, "pods".
	a, ok := find(m.Actions(), "describe")
	if !ok {
		t.Fatal("a verb scoped to another pane should still be listed")
	}
	if a.Disabled == "" {
		t.Fatal("it should be disabled while its pane is unfocused")
	}
	if !strings.Contains(a.Disabled, "nodes") {
		t.Errorf("the reason should name the pane, got %q", a.Disabled)
	}

	// Move focus to its pane and it becomes available.
	m.cycleFocus(+1)
	a, _ = find(m.Actions(), "describe")
	if a.Disabled != "" {
		t.Errorf("should be enabled once nodes has focus, got %q", a.Disabled)
	}
}

// Run is background work the shell can attribute, cancel and report on,
// so everything that can be Run is. Do is only for what can't.
func TestBoundActionUsesRunAndUnboundUsesDo(t *testing.T) {
	m := menuModel(t,
		[]cfg.ActionBinding{
			{Key: "b", Action: "bound", Label: "bound"},
			{Key: "u", Action: "unbound", Label: "unbound"},
		},
		map[string]*cfg.Action{
			"bound": {Run: []string{"echo", "hi"}},
			"unbound": {Run: []string{"echo", "${inputs.amount}"},
				Inputs: map[string]*cfg.Parameter{"amount": {}}},
		})

	set := m.Actions()
	b, _ := find(set, "bound")
	if b.Run == nil || b.Do != nil {
		t.Error("a fully-bound action should be Run, not Do — Run is what the console, badge and cancellation attach to")
	}
	u, _ := find(set, "unbound")
	if u.Do == nil || u.Run != nil {
		t.Error("an action with unbound inputs must be Do: the shell runs Run itself and never forwards ChosenMsg, so there'd be nowhere to ask")
	}
}

// The confirm previews resolved values, so an input carrying a default
// shows what will actually run rather than an empty string.
func TestConfirmIsSubstitutedForTheMenu(t *testing.T) {
	m := menuModel(t,
		[]cfg.ActionBinding{{
			Key: "d", Action: "del", Label: "delete",
			Confirm: "Delete ${inputs.name}?",
		}},
		map[string]*cfg.Action{
			"del": {Run: []string{"echo", "${inputs.name}"},
				Inputs: map[string]*cfg.Parameter{"name": {Default: "web"}}},
		})

	a, ok := find(m.Actions(), "delete")
	if !ok {
		t.Fatal("delete missing")
	}
	if a.Confirm != "Delete web?" {
		t.Errorf("Confirm = %q, want the resolved default substituted", a.Confirm)
	}
}

// Set.Target is the last surface that can say whether a verb is about to
// hit one row or twelve.
func TestSetTargetNamesTheSelection(t *testing.T) {
	m := menuModel(t,
		[]cfg.ActionBinding{{Key: "d", Action: "d", Label: "d"}},
		map[string]*cfg.Action{"d": {Run: []string{"echo"}}})
	// New() builds the components; rows arrive on the first fetch, so
	// without this the list is empty and there is nothing to target.
	build.ApplyData(m.tree.All()[0], []any{
		map[string]any{"name": "web"},
		map[string]any{"name": "api"},
	}, theme.Nord())

	set := m.Actions()
	if set.Count != 1 {
		t.Errorf("Count = %d, want 1 for a single focused row", set.Count)
	}
	if set.Target != "web" {
		t.Errorf("Target = %q, want the focused row", set.Target)
	}
}

// action.Validate exists to be called from a test: a duplicate shortcut
// silently resolves to whichever action is listed first, and a duplicate
// identity makes one Exclusive action disable an unrelated one. Both
// fail far from their cause.
func TestGeneratedSetIsValid(t *testing.T) {
	m := menuModel(t,
		[]cfg.ActionBinding{
			{Key: "d", Action: "describe", Label: "describe"},
			{Key: "l", Action: "logs", Label: "logs"},
			{Key: "x", Action: "del", Label: "delete", Confirm: "Sure?"},
		},
		map[string]*cfg.Action{
			"describe": {Run: []string{"echo", "d"}},
			"logs":     {Run: []string{"echo", "l"}},
			"del":      {Run: []string{"echo", "x"}},
		})

	for _, err := range taction.Validate(m.Actions()) {
		t.Errorf("generated set is invalid: %v", err)
	}
}

// A screen with no bindings must produce an empty set, so the shell
// leaves the menu affordance off entirely.
func TestNoBindingsMeansNoMenu(t *testing.T) {
	m := menuModel(t, nil, nil)
	if !m.Actions().Empty() {
		t.Error("a screen with no action bindings should have no menu")
	}
}
