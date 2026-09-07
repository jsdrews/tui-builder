package config

import (
	"strings"
	"testing"
)

// TestSubstituteEnvCoversEveryTemplatedField walks one config with a
// token in every place a template can legally appear and asserts none
// survive. The failure this guards against is a *coverage* one: a field
// that build.cloneComponent substitutes but the data layer doesn't (or
// the reverse) reads as "env substitution is broken" to whoever is
// using that field, even though it works everywhere else.
func TestSubstituteEnvCoversEveryTemplatedField(t *testing.T) {
	t.Setenv("TQ_TEST", "X")

	tok := "${env.TQ_TEST}"
	c := &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"http_src": {
				Type: "http", URL: tok, Body: tok, Method: tok,
				Headers: map[string]string{"Authorization": tok},
			},
			"exec_src": {
				Type: "exec", Command: []string{"sh", "-c", tok},
				Env: map[string]string{"K": tok},
			},
			"file_src": {Type: "file", Path: tok},
			"ws_src":   {Type: "websocket", URL: tok, InitialMessages: []string{tok}},
		}},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"c": {
					Type: "table", Title: tok,
					FilterPlaceholder: tok, InitialFilter: tok, InitialQuery: tok,
					RootLabel: tok,
					Items:     []string{tok},
					Lines:     []string{tok},
					Columns:   []Column{{Title: tok}},
					Rows: [][]any{{
						tok,
						map[string]any{"value": tok, "color": "red"},
						42, // non-string cells must survive untouched
					}},
					Root:   &TreeNode{Label: tok, Children: []*TreeNode{{Label: tok}}},
					Fields: []InspectorField{{Label: tok, Value: tok, Children: []InspectorField{{Label: tok}}}},
				},
			},
			Screen: Screen{
				Title: tok,
				Actions: []ActionBinding{{
					Key: "x", Action: "run", Confirm: tok, Notice: tok,
					Bind: map[string]string{"arg": tok},
				}},
			},
			Screens: map[string]*Screen{
				"other": {Title: tok, Actions: []ActionBinding{{Key: "y", Action: "post", Notice: tok}}},
			},
		},
		// The registry: an action's argv, URL, headers and body are the
		// values that actually reach the outside world, and visitActions
		// is the only thing that walks them.
		Actions: map[string]*Action{
			"run":  {Run: []string{"echo", tok}},
			"post": {Type: "http", Method: tok, URL: tok, Body: tok, Headers: map[string]string{"Authorization": tok}},
		},
	}

	c.SubstituteEnv()

	var found []string
	walkTemplates(c, func(s *string) {
		if strings.Contains(*s, "${env.") {
			found = append(found, *s)
		}
	})
	if len(found) > 0 {
		t.Errorf("%d templated field(s) still hold an env token after SubstituteEnv: %v", len(found), found)
	}

	// Spot-check a few that the walker itself could be lying about, plus
	// the non-string cell that must have been left alone.
	if got := c.Data.Sources["http_src"].Headers["Authorization"]; got != "X" {
		t.Errorf("header = %q, want %q", got, "X")
	}
	if got := c.TUI.Components["c"].Columns[0].Title; got != "X" {
		t.Errorf("column title = %q, want %q", got, "X")
	}
	if got := c.TUI.Components["c"].Root.Children[0].Label; got != "X" {
		t.Errorf("nested tree label = %q, want %q", got, "X")
	}
	if got := c.TUI.Components["c"].Fields[0].Children[0].Label; got != "X" {
		t.Errorf("nested inspector label = %q, want %q", got, "X")
	}
	if got := c.TUI.Components["c"].Rows[0][1].(map[string]any)["value"]; got != "X" {
		t.Errorf("styled cell value = %q, want %q", got, "X")
	}
	if got := c.TUI.Components["c"].Rows[0][2]; got != 42 {
		t.Errorf("non-string cell = %v, want it untouched", got)
	}
	if got := c.TUI.Screens["other"].Title; got != "X" {
		t.Errorf("multi-screen title = %q, want %q", got, "X")
	}
}

// An unset var resolves to empty, not to a literal token. checkEnv has
// already warned (or failed) for the ones that matter, so by here the
// empty string is the intended answer.
func TestSubstituteEnvUnsetBecomesEmpty(t *testing.T) {
	c := &Config{Data: DataBlock{Sources: map[string]*Source{
		"s": {Type: "http", URL: "https://host/${env.TQ_DEFINITELY_UNSET}/x"},
	}}}
	c.SubstituteEnv()
	if got := c.Data.Sources["s"].URL; got != "https://host//x" {
		t.Errorf("URL = %q, want the token replaced by an empty string", got)
	}
}

// Both entry points may call this, and the TUI substitutes again later
// through internal/build. A second pass must not corrupt a resolved
// string — notably one whose *value* looks like a token.
func TestSubstituteEnvIsIdempotent(t *testing.T) {
	t.Setenv("TQ_A", "${env.TQ_B}")
	t.Setenv("TQ_B", "second-pass")

	c := &Config{Data: DataBlock{Sources: map[string]*Source{
		"s": {Type: "http", URL: "${env.TQ_A}"},
	}}}
	c.SubstituteEnv()
	first := c.Data.Sources["s"].URL
	if first != "${env.TQ_B}" {
		t.Fatalf("URL = %q, want the literal value of TQ_A", first)
	}
	// The value TQ_A held happens to look like a token. Re-running must
	// not expand it — a substituted value is data, not a template, and
	// treating it as one is how an env var's contents become an
	// injection vector.
	c.SubstituteEnv()
	if got := c.Data.Sources["s"].URL; got != first {
		t.Errorf("second pass rewrote a substituted value: %q → %q", first, got)
	}
}

// Fields that name data rather than carry display text must be left
// alone — substituting a dot-path or an expression would silently
// change which data a config reads.
func TestSubstituteEnvSkipsPathsAndExpressions(t *testing.T) {
	t.Setenv("TQ_TEST", "X")

	c := &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"leaf": {Type: "http", URL: "https://host", Root: "${env.TQ_TEST}"},
			"op":   {Type: "filter", From: "leaf", Where: `name == "${env.TQ_TEST}"`},
		}},
		TUI: TUIBlock{Components: map[string]*Component{
			"c": {Type: "table", Columns: []Column{{Title: "T", Value: Path{"${env.TQ_TEST}"}}}},
		}},
	}
	c.SubstituteEnv()

	if got := c.Data.Sources["leaf"].Root; got != "${env.TQ_TEST}" {
		t.Errorf("root: was substituted (%q) — it's a dot-path, not display text", got)
	}
	if got := c.Data.Sources["op"].Where; !strings.Contains(got, "${env.TQ_TEST}") {
		t.Errorf("where: was substituted (%q) — expressions are evaluated, not templated", got)
	}
	if got := c.TUI.Components["c"].Columns[0].Value[0]; got != "${env.TQ_TEST}" {
		t.Errorf("column value: was substituted (%q) — it's a dot-path", got)
	}
}

// Only the env namespace is touched. Selection and prompt tokens are
// resolved later (at push time and at dispatch time respectively), so
// consuming them here would break both.
func TestSubstituteEnvLeavesOtherTokenFamilies(t *testing.T) {
	c := &Config{Data: DataBlock{Sources: map[string]*Source{
		"s": {Type: "http", URL: "https://host/${selection.Namespace}/${prompt.name}/${params.id}"},
	}}}
	before := c.Data.Sources["s"].URL
	c.SubstituteEnv()
	if got := c.Data.Sources["s"].URL; got != before {
		t.Errorf("URL = %q, want %q — only ${env.*} belongs to this pass", got, before)
	}
}
