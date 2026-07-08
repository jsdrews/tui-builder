package config

import (
	"strings"
	"testing"
)

// Tests for the extended OnEnterBinding schema — feature D from the
// tui-builder integration batch: on_enter now accepts an optional
// `key:` so custom keys (not just Enter) push screens.

func makeConfig(bindings []OnEnterBinding) *Config {
	// Minimal multi-screen shell that will surface OnEnter validation
	// errors from Config.Validate. The layout wires the source component
	// so `source %q not used` doesn't misfire.
	return &Config{
		Data: DataBlock{
			Sources: map[string]*Source{
				"src": {Type: "static", Data: []any{}},
			},
		},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"tbl": {Type: "table", Source: "src", Columns: []Column{{Title: "A", Value: Path{"a"}}}},
			},
			Screens: map[string]*Screen{
				"a": {
					Layout: Node{Component: "tbl"},
					OnEnter: append([]OnEnterBinding(nil), bindings...),
				},
				"b": {Layout: Node{Component: "tbl"}},
			},
			Initial: "a",
		},
	}
}

func TestOnEnterAcceptsCustomKey(t *testing.T) {
	c := makeConfig([]OnEnterBinding{
		{Source: "tbl", Push: "b", Key: "d"},
	})
	if err := c.Validate(); err != nil {
		t.Errorf("valid custom-key binding should pass, got %v", err)
	}
}

func TestOnEnterMultipleBindingsPerSourceAcceptedWhenKeysDiffer(t *testing.T) {
	c := makeConfig([]OnEnterBinding{
		{Source: "tbl", Push: "b"},           // Enter → b
		{Source: "tbl", Push: "b", Key: "d"}, // d → b (imagine two screens; same push for simplicity)
	})
	if err := c.Validate(); err != nil {
		t.Errorf("two bindings on same source with distinct keys should pass, got %v", err)
	}
}

func TestOnEnterDuplicateSourceKeyPairRejected(t *testing.T) {
	c := makeConfig([]OnEnterBinding{
		{Source: "tbl", Push: "b", Key: "d"},
		{Source: "tbl", Push: "b", Key: "d"},
	})
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Errorf("duplicate (source, key) should error, got %v", err)
	}
}

func TestOnEnterEmptyKeyTreatedAsEnterForDedup(t *testing.T) {
	c := makeConfig([]OnEnterBinding{
		{Source: "tbl", Push: "b"},              // empty Key → "enter"
		{Source: "tbl", Push: "b", Key: "enter"}, // explicit "enter"
	})
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Errorf("empty Key + explicit \"enter\" should collide, got %v", err)
	}
}
