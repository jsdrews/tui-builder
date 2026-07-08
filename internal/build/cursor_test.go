package build

import "testing"

// Tests for the ${cursor.*} template token — feature D of the on-hover
// stack. Mirrors ${selection.*}'s resolver but reads from a live cursor
// Selection and reports unresolved lookups as "" instead of leaving
// the literal token in the URL (which would 404 on every cursor move).

func TestSubstituteCursorByColumnTitle(t *testing.T) {
	sel := Selection{
		Cells:   []string{"default", "nginx-abc"},
		Columns: []string{"Namespace", "Name"},
	}
	got := SubstituteCursor("/api/v1/namespaces/${cursor.Namespace}/pods/${cursor.Name}", sel)
	want := "/api/v1/namespaces/default/pods/nginx-abc"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestSubstituteCursorBareIsFirstCell(t *testing.T) {
	sel := Selection{
		String:  "first",
		Cells:   []string{"first", "second"},
		Columns: []string{"A", "B"},
	}
	if got := SubstituteCursor("${cursor}", sel); got != "first" {
		t.Errorf("want first, got %q", got)
	}
}

func TestSubstituteCursorNumericIndex(t *testing.T) {
	sel := Selection{
		Cells:   []string{"first", "second"},
		Columns: []string{"A", "B"},
	}
	if got := SubstituteCursor("${cursor.2}", sel); got != "second" {
		t.Errorf("want second, got %q", got)
	}
}

// TestSubstituteCursorUnresolvedIsEmpty pins the safety property:
// referencing a nonexistent column produces "" rather than leaving the
// literal token in place. Cursor bindings fire on every keystroke; a
// stale literal in a URL would generate a 404 storm.
func TestSubstituteCursorUnresolvedIsEmpty(t *testing.T) {
	sel := Selection{Cells: []string{"x"}, Columns: []string{"A"}}
	if got := SubstituteCursor("${cursor.Missing}", sel); got != "" {
		t.Errorf("want empty, got %q", got)
	}
	if got := SubstituteCursor("${cursor.99}", sel); got != "" {
		t.Errorf("want empty for out-of-range, got %q", got)
	}
}

// TestSubstituteCursorLeavesSelectionAlone confirms the runtime path
// doesn't accidentally resolve ${selection.*} against an empty
// Selection — those should pass through as literals so the URL still
// makes sense post-substitution.
func TestSubstituteCursorLeavesSelectionAlone(t *testing.T) {
	sel := Selection{Cells: []string{"nginx"}, Columns: []string{"Name"}}
	got := SubstituteCursor("${cursor.Name}?tag=${selection.Env}", sel)
	if got != "nginx?tag=${selection.Env}" {
		t.Errorf("want cursor resolved + selection preserved; got %q", got)
	}
}

// TestSubstituteCursorComposesWithEnv pins that ${cursor.X} + ${env.Y}
// in the same template both resolve — the common "kubectl-style
// cluster URL + pod name" pattern.
func TestSubstituteCursorComposesWithEnv(t *testing.T) {
	t.Setenv("TEST_CLUSTER", "prod")
	sel := Selection{Cells: []string{"nginx"}, Columns: []string{"Name"}}
	got := SubstituteCursor("${env.TEST_CLUSTER}/${cursor.Name}", sel)
	if got != "prod/nginx" {
		t.Errorf("want prod/nginx, got %q", got)
	}
}

// TestSubstituteCursorLabelAlias covers ${cursor.label} — an explicit
// alias for bare ${cursor}. Useful for tree drivers where "label"
// reads more naturally than the bare form.
func TestSubstituteCursorLabelAlias(t *testing.T) {
	sel := Selection{String: "nginx-abc"}
	if got := SubstituteCursor("${cursor.label}", sel); got != "nginx-abc" {
		t.Errorf("want nginx-abc, got %q", got)
	}
}

// TestSubstituteCursorDepth covers the tree-driver depth key —
// returns the number of path elements as a string.
func TestSubstituteCursorDepth(t *testing.T) {
	// Tree cursor: Cells IS the path.
	sel := Selection{
		String: "leaf",
		Cells:  []string{"root", "level1", "leaf"},
	}
	if got := SubstituteCursor("${cursor.depth}", sel); got != "3" {
		t.Errorf("want 3, got %q", got)
	}
	// Empty cursor → depth 0.
	if got := SubstituteCursor("${cursor.depth}", Selection{}); got != "0" {
		t.Errorf("want 0 for empty selection, got %q", got)
	}
}

// TestSubstituteCursorTreePathIndex confirms numeric indexing works
// as expected for tree paths (uniform with tables and lists — Cells
// carries the path).
func TestSubstituteCursorTreePathIndex(t *testing.T) {
	sel := Selection{
		String: "leaf",
		Cells:  []string{"root", "namespace", "pod-name"},
	}
	cases := []struct {
		tmpl, want string
	}{
		{"${cursor.1}", "root"},
		{"${cursor.2}", "namespace"},
		{"${cursor.3}", "pod-name"},
		{"${cursor.4}", ""}, // out-of-range → empty
	}
	for _, tc := range cases {
		if got := SubstituteCursor(tc.tmpl, sel); got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.tmpl, tc.want, got)
		}
	}
}

// TestSubstituteCursorListItem — for a list driver Columns is
// ["item"], so ${cursor.item} resolves. Confirms the list wiring
// exposes item-by-name access alongside the bare form.
func TestSubstituteCursorListItem(t *testing.T) {
	sel := Selection{
		String:  "default",
		Cells:   []string{"default"},
		Columns: []string{"item"},
	}
	if got := SubstituteCursor("${cursor.item}", sel); got != "default" {
		t.Errorf("want default, got %q", got)
	}
	if got := SubstituteCursor("${cursor}", sel); got != "default" {
		t.Errorf("bare form: want default, got %q", got)
	}
}
