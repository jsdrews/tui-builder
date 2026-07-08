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
