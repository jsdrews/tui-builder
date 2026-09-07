package config

import (
	"strings"
	"testing"
)

// markConfig is a valid config with one source-bound table, so a test
// can toggle markable / mark_key without the rest of the schema
// getting in the way.
func markConfig() *Config {
	return &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"pods": NewEntry(&Source{Type: "static", Data: []any{}}),
		}},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"pods": {
					Type:    "table",
					Source:  "pods",
					Columns: []Column{{Title: "Name", Value: Path{"metadata.name"}}},
				},
			},
			Screen: Screen{Title: "Pods", Layout: Node{Component: "pods"}},
		},
	}
}

func TestMarkableTableNeedsMarkKey(t *testing.T) {
	c := markConfig()
	c.TUI.Components["pods"].Markable = true
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error: a source-bound markable table needs mark_key")
	}
	if !strings.Contains(err.Error(), "mark_key") {
		t.Errorf("error should name the missing field, got: %v", err)
	}
	// And it passes once supplied.
	c.TUI.Components["pods"].MarkKey = Path{"metadata.uid"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate with mark_key: %v", err)
	}
}

// The whole point of requiring it is that the obvious default is unsafe,
// so the error has to say why or someone will just add the default back.
func TestMarkKeyErrorExplainsWhyItIsNotDefaulted(t *testing.T) {
	c := markConfig()
	c.TUI.Components["pods"].Markable = true
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"first column", "poll"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// A windowed table carries rows without keys (tuilib's keyAt returns ""
// when windowed), so the gutter would draw and never respond.
func TestMarkableRejectsWindowedTable(t *testing.T) {
	c := markConfig()
	c.Data.Sources["pods"] = NewEntry(&Source{
		Type:   "http",
		URL:    "https://example.com/pods",
		Window: &WindowConfig{PageSize: 50, LimitParam: "limit", OffsetParam: "offset"},
	})
	comp := c.TUI.Components["pods"]
	comp.Markable = true
	comp.MarkKey = Path{"metadata.uid"}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for markable + windowed")
	}
	if !strings.Contains(err.Error(), "windowed") {
		t.Errorf("error should name the conflict, got: %v", err)
	}
}

func TestMarkableRejectsUnsupportedKinds(t *testing.T) {
	for _, kind := range []string{"inspector", "logview", "textview"} {
		c := markConfig()
		c.TUI.Components["pods"] = &Component{Type: kind, Markable: true}
		if kind == "inspector" {
			c.TUI.Components["pods"].Fields = []InspectorField{{Label: "a", Value: "b"}}
		}
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: expected markable to be rejected", kind)
			continue
		}
		if !strings.Contains(err.Error(), "markable") {
			t.Errorf("%s: error should name the field, got: %v", kind, err)
		}
	}
}

// List and tree derive their own identity, so accepting mark_key there
// would let an author believe they had set it.
func TestMarkKeyRejectedOnListAndTree(t *testing.T) {
	for _, kind := range []string{"list", "tree"} {
		c := markConfig()
		comp := &Component{Type: kind, Markable: true, MarkKey: Path{"id"}}
		switch kind {
		case "list":
			comp.Source, comp.Item = "pods", "name"
		case "tree":
			comp.Root = &TreeNode{Label: "root"}
		}
		c.TUI.Components["pods"] = comp
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: expected mark_key to be rejected", kind)
			continue
		}
		if !strings.Contains(err.Error(), "mark_key") {
			t.Errorf("%s: error should name the field, got: %v", kind, err)
		}
	}
}

// Static rows are fixed at load, so position is a legitimate identity
// and there is nothing for mark_key to name.
func TestMarkKeyRejectedOnStaticTable(t *testing.T) {
	c := markConfig()
	c.TUI.Components["pods"] = &Component{
		Type:     "table",
		Columns:  []Column{{Title: "Name"}},
		Rows:     [][]any{{"a"}, {"b"}},
		Markable: true,
		MarkKey:  Path{"id"},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected mark_key to be rejected on a static table")
	}
	if !strings.Contains(err.Error(), "static") {
		t.Errorf("error should explain why, got: %v", err)
	}
}

func TestStaticTableMarkableWithoutKey(t *testing.T) {
	c := markConfig()
	c.TUI.Components["pods"] = &Component{
		Type:     "table",
		Columns:  []Column{{Title: "Name"}},
		Rows:     [][]any{{"a"}, {"b"}},
		Markable: true,
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestMarkKeyWithoutMarkableRejected(t *testing.T) {
	c := markConfig()
	c.TUI.Components["pods"].MarkKey = Path{"metadata.uid"}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for mark_key without markable")
	}
	if !strings.Contains(err.Error(), "markable") {
		t.Errorf("error should point at the missing switch, got: %v", err)
	}
}

// Marking is off by default and must stay a no-op when unset.
func TestUnmarkedConfigStillValid(t *testing.T) {
	if err := markConfig().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
