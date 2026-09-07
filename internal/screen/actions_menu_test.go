package screen

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

// markableModel builds a screen whose list carries marks, so a
// multi-selection is expressible.
func markableModel(t *testing.T, bindings []cfg.ActionBinding, reg map[string]*cfg.Action) *Model {
	t.Helper()
	comps := map[string]*cfg.Component{
		"pods": {Type: "list", Source: "items", Item: "name", Markable: true},
	}
	sources := map[string]*cfg.Source{
		"items": cfg.NewEntry(&cfg.Source{Type: "static", Data: []any{}}),
	}
	sc := &cfg.Screen{Title: "Pods", Layout: cfg.Node{Component: "pods"}, Actions: bindings}
	m, err := New(sc, comps, sources, reg, theme.Nord())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	build.ApplyData(m.tree.All()[0], []any{
		map[string]any{"name": "web"},
		map[string]any{"name": "api"},
		map[string]any{"name": "db"},
	}, theme.Nord())
	return m
}

func markRows(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	m.tree.All()[0].List.SetMarks(keys)
}

func restartBindings() ([]cfg.ActionBinding, map[string]*cfg.Action) {
	return []cfg.ActionBinding{{
			Key: "r2", Action: "restart", Label: "restart", From: "pods",
			Bind: map[string]string{"name": "${selection}"},
		}}, map[string]*cfg.Action{
			"restart": {
				Multi:  true,
				Run:    []string{"echo", "${inputs.name}"},
				Inputs: map[string]*cfg.Parameter{"name": {}},
			},
		}
}

// Set.Count is what the menu uses to decide whether a non-Multi verb is
// available, and what titles the menu.
func TestMultiSelectionDrivesTargetAndCount(t *testing.T) {
	bs, reg := restartBindings()
	m := markableModel(t, bs, reg)
	markRows(t, m, "web", "api")

	set := m.Actions()
	if set.Count != 2 {
		t.Errorf("Count = %d, want 2", set.Count)
	}
	if set.Target != "2 items" {
		t.Errorf("Target = %q, want \"2 items\"", set.Target)
	}
}

// Multi is the action's own declaration; the menu disables a non-Multi
// verb under a multi-selection on its own.
func TestMultiFlagIsCarriedToTheMenu(t *testing.T) {
	bs, reg := restartBindings()
	m := markableModel(t, bs, reg)
	markRows(t, m, "web", "api")
	a, ok := find(m.Actions(), "restart")
	if !ok {
		t.Fatal("restart missing")
	}
	if !a.Multi {
		t.Error("Multi should reach the menu; without it the shell disables the verb")
	}

	reg["restart"].Multi = false
	a, _ = find(m.Actions(), "restart")
	if a.Multi {
		t.Error("Multi must default off — the safe way round")
	}
}

// A multi-selection fans out to Do (N runs); a single selection stays on
// Run, which is what the shell can attribute and cancel on its own.
func TestFanOutUsesDoOnlyForMoreThanOne(t *testing.T) {
	bs, reg := restartBindings()
	m := markableModel(t, bs, reg)

	markRows(t, m, "web")
	a, _ := find(m.Actions(), "restart")
	if a.Run == nil || a.Do != nil {
		t.Error("one target should stay on Run")
	}

	markRows(t, m, "web", "api", "db")
	a, _ = find(m.Actions(), "restart")
	if a.Do == nil || a.Run != nil {
		t.Error("a multi-selection should fan out via Do")
	}
}

// The point of N runs rather than one joined argv: each is tagged with
// its own target, so exclusivity and cancellation are per-row.
func TestFanOutTagsEachRunWithItsOwnTarget(t *testing.T) {
	bs, reg := restartBindings()
	m := markableModel(t, bs, reg)
	markRows(t, m, "web", "api")

	a, _ := find(m.Actions(), "restart")
	sels := build.MarkedSelections(m.tree.All()[0])
	if len(sels) != 2 {
		t.Fatalf("want 2 marked rows, got %d", len(sels))
	}
	if got := taction.RunKey(a, sels[0].String); got == taction.RunKey(a, sels[1].String) {
		t.Fatal("two targets produced the same RunKey — exclusivity would be shared")
	}

	// The fan-out itself batches one command per row.
	cmd := m.fanOut(bs[0], reg["restart"], a, sels)
	if cmd == nil {
		t.Fatal("fanOut produced no command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("want a batch of runs, got %T", msg)
	}
	if len(batch) != 2 {
		t.Errorf("got %d runs, want one per marked row", len(batch))
	}
}

// Each row resolves its own bind: templates, so the argv is that row's.
func TestFanOutResolvesPerRow(t *testing.T) {
	bs, reg := restartBindings()
	m := markableModel(t, bs, reg)
	markRows(t, m, "web", "api")

	sels := build.MarkedSelections(m.tree.All()[0])
	seen := map[string]bool{}
	for _, sel := range sels {
		in := resolveBinds(bs[0], reg["restart"], sel)
		seen[in["name"]] = true
	}
	if !seen["web"] || !seen["api"] {
		t.Errorf("each row should resolve its own name, got %v", seen)
	}
}

// A confirm template resolves against one row, so on its own it would
// say "Delete web?" while deleting three. The count is the part the
// author couldn't have written.
func TestFanOutConfirmStatesTheArity(t *testing.T) {
	bs, reg := restartBindings()
	bs[0].Confirm = "Restart ${selection}?"
	m := markableModel(t, bs, reg)

	markRows(t, m, "web")
	a, _ := find(m.Actions(), "restart")
	if strings.Contains(a.Confirm, "rows)") {
		t.Errorf("a single target needs no arity note, got %q", a.Confirm)
	}

	markRows(t, m, "web", "api", "db")
	a, _ = find(m.Actions(), "restart")
	if !strings.Contains(a.Confirm, "Restart ") {
		t.Errorf("the author's wording should survive, got %q", a.Confirm)
	}
	if !strings.Contains(a.Confirm, "(3 rows)") {
		t.Errorf("confirm should state the arity, got %q", a.Confirm)
	}
}

// The shell centres its confirm at a fixed 52x7 and does not wrap, so a
// long message loses its tail — and the tail of a confirm is where
// "cannot be undone" lives.
func TestShellConfirmIsWrappedToFit(t *testing.T) {
	long := "Delete pod nginx-abc-12345 in namespace production? This cannot be undone."
	got := fitShellConfirm(long)
	for _, ln := range strings.Split(got, "\n") {
		if len(ln) > shellConfirmWidth {
			t.Errorf("line wider than the modal: %q", ln)
		}
	}
	if !strings.Contains(got, "cannot be undone") {
		t.Errorf("the tail is the part that matters; it was lost: %q", got)
	}
	if n := len(strings.Split(got, "\n")); n > shellConfirmLines {
		t.Errorf("%d lines, modal fits %d", n, shellConfirmLines)
	}
}

// Something genuinely too long says so rather than stopping mid-word.
func TestOverlongConfirmSaysItWasTruncated(t *testing.T) {
	got := fitShellConfirm(strings.Repeat("word ", 80))
	if n := len(strings.Split(got, "\n")); n > shellConfirmLines {
		t.Errorf("%d lines, modal fits %d", n, shellConfirmLines)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("should admit the truncation, got %q", got)
	}
}

// The real kube.yaml delete confirm, at its longest realistic
// substitution, must survive the shell modal intact.
func TestKubeDeleteConfirmFits(t *testing.T) {
	msg := "Delete pod nginx-deployment-7c64f5d in kube-system? This cannot be undone. (3 rows)"
	got := fitShellConfirm(msg)
	t.Logf("rendered:\n%s", got)
	if strings.Contains(got, "truncated") {
		t.Errorf("kube's own confirm does not fit the shell modal:\n%s", got)
	}
}
