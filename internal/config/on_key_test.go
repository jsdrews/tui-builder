package config

import (
	"strings"
	"testing"
)

// Tests for the OnKeyBinding schema — feature D from the tui-builder
// integration batch. Every binding must spell its trigger key
// explicitly (`key: enter` for the classic drilldown, `key: d` for a
// custom push).

func makeConfig(bindings []OnKeyBinding) *Config {
	// Minimal multi-screen shell that will surface OnKey validation
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
					OnKey:  append([]OnKeyBinding(nil), bindings...),
				},
				"b": {Layout: Node{Component: "tbl"}},
			},
			Initial: "a",
		},
	}
}

func TestOnKeyAcceptsExplicitEnter(t *testing.T) {
	c := makeConfig([]OnKeyBinding{
		{Source: "tbl", Push: "b", Key: "enter"},
	})
	if err := c.Validate(); err != nil {
		t.Errorf("explicit `key: enter` should validate, got %v", err)
	}
}

func TestOnKeyAcceptsCustomKey(t *testing.T) {
	c := makeConfig([]OnKeyBinding{
		{Source: "tbl", Push: "b", Key: "d"},
	})
	if err := c.Validate(); err != nil {
		t.Errorf("valid custom-key binding should pass, got %v", err)
	}
}

func TestOnKeyMultipleBindingsPerSourceAcceptedWhenKeysDiffer(t *testing.T) {
	c := makeConfig([]OnKeyBinding{
		{Source: "tbl", Push: "b", Key: "enter"},
		{Source: "tbl", Push: "b", Key: "d"},
	})
	if err := c.Validate(); err != nil {
		t.Errorf("two bindings on same source with distinct keys should pass, got %v", err)
	}
}

func TestOnKeyDuplicateSourceKeyPairRejected(t *testing.T) {
	c := makeConfig([]OnKeyBinding{
		{Source: "tbl", Push: "b", Key: "d"},
		{Source: "tbl", Push: "b", Key: "d"},
	})
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Errorf("duplicate (source, key) should error, got %v", err)
	}
}

func TestOnKeyMissingKeyRejected(t *testing.T) {
	c := makeConfig([]OnKeyBinding{
		{Source: "tbl", Push: "b"}, // no key
	})
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "`key:` is required") {
		t.Errorf("missing key should error, got %v", err)
	}
}
