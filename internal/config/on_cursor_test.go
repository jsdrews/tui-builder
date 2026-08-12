package config

import (
	"strings"
	"testing"
)

// Builds a Config with one table + one source-bound inspector wired
// via on_cursor. Mutations to the returned config exercise the
// specific rule under test.
func onCursorFixture() *Config {
	return &Config{
		Data: DataBlock{
			Sources: map[string]*Source{
				"pods": {Type: "static", Data: []any{}},
				"pod_detail": {
					Type:       "http",
					URL:        "http://x/${params.name}",
					Parameters: map[string]*Parameter{"name": {Type: "string", Required: true}},
				},
			},
		},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"pods_table": {
					Type: "table", Source: "pods",
					Columns: []Column{{Title: "Name", Value: Path{"metadata.name"}}},
				},
				"pod_pane": {
					Type: "inspector", Source: "pod_detail", Auto: true,
					OnCursor: &OnCursor{
						Source: "pods_table",
						Bind:   map[string]string{"name": "${cursor.Name}"},
					},
				},
			},
			Screen: Screen{
				Layout: Node{VStack: []Item{
					{Node: Node{Component: "pods_table"}},
					{Node: Node{Component: "pod_pane"}},
				}},
			},
		},
	}
}

func TestOnCursorHappyPath(t *testing.T) {
	c := onCursorFixture()
	if err := c.Validate(); err != nil {
		t.Errorf("valid on_cursor config should pass, got %v", err)
	}
}

func TestOnCursorSourceMustBeInLayout(t *testing.T) {
	c := onCursorFixture()
	c.TUI.Components["pod_pane"].OnCursor.Source = "somewhere_else"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "not used in this screen's layout") {
		t.Errorf("want unknown-driver error, got %v", err)
	}
}

func TestOnCursorDriverListAccepted(t *testing.T) {
	c := onCursorFixture()
	// Swap the driver for a list — v0.17.0 emits SelectedChangedMsg
	// from lists, so this is now a valid driver.
	c.TUI.Components["pods_table"] = &Component{Type: "list", Source: "pods", Item: "metadata.name"}
	if err := c.Validate(); err != nil {
		t.Errorf("list driver should be accepted, got %v", err)
	}
}

func TestOnCursorDriverTreeAccepted(t *testing.T) {
	c := onCursorFixture()
	// Swap the driver for a source-bound tree — v0.17.0 emits
	// SelectedChangedMsg from trees, so this is now a valid driver.
	c.TUI.Components["pods_table"] = &Component{
		Type: "tree", Source: "pods", Label: Path{"name"},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("tree driver should be accepted, got %v", err)
	}
}

func TestOnCursorDriverInspectorRejected(t *testing.T) {
	c := onCursorFixture()
	c.TUI.Components["pods_table"] = &Component{
		Type: "inspector", Source: "pods", Auto: true,
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "must be a table, list, or tree") {
		t.Errorf("want driver-type error, got %v", err)
	}
}

func TestOnCursorTargetMustBeSourceBound(t *testing.T) {
	c := onCursorFixture()
	c.TUI.Components["pod_pane"].Source = ""
	c.TUI.Components["pod_pane"].Fields = []InspectorField{{Label: "x", Value: "static"}}
	c.TUI.Components["pod_pane"].Auto = false
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "no `source:`") {
		t.Errorf("want target-has-no-source error, got %v", err)
	}
}

// TestHiddenColumnAcceptedOnTable pins that setting Hidden doesn't
// break the existing validation flow (no new required fields; the
// value stays a plain dot-path just like non-hidden columns).
func TestHiddenColumnAcceptedOnTable(t *testing.T) {
	c := onCursorFixture()
	c.TUI.Components["pods_table"].Columns = []Column{
		{Title: "Namespace", Value: Path{"metadata.namespace"}, Hidden: true},
		{Title: "Name", Value: Path{"metadata.name"}},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("hidden column should validate, got %v", err)
	}
}
