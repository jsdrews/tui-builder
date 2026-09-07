package config

import (
	"strings"
	"testing"
)

// Pushes used to be their own `on_key:` block. They are actions now —
// `push:` names the destination, and the binding's `bind:` fills that
// screen's parameters rather than the action's inputs.

func pushConfig(bindings []ActionBinding, reg map[string]*Action) *Config {
	return &Config{
		Actions: reg,
		Data: DataBlock{
			Sources: map[string]*Source{"src": {Type: "static", Data: []any{}}},
		},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"tbl": {Type: "table", Source: "src", Columns: []Column{{Title: "A", Value: Path{"a"}}}},
			},
			Screens: map[string]*Screen{
				"a": {Layout: Node{Component: "tbl"}, Actions: bindings},
				"b": {Layout: Node{Component: "tbl"}},
			},
			Initial: "a",
		},
	}
}

func TestPushActionAcceptsEnter(t *testing.T) {
	c := pushConfig(
		[]ActionBinding{{Key: "enter", Action: "open", From: "tbl"}},
		map[string]*Action{"open": {Push: "b"}})
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// `push:` alone is enough — there is no other reason to name a screen,
// so a drilldown stays as short as it was under on_key:.
func TestPushKindInferredFromPushField(t *testing.T) {
	a := &Action{Push: "b"}
	if got := a.Kind(); got != "push" {
		t.Errorf("Kind() = %q, want push", got)
	}
}

// A push always reads the focused row, declared templates or not: the
// row is what the drilldown drills into.
func TestPushRequiresFrom(t *testing.T) {
	c := pushConfig(
		[]ActionBinding{{Key: "enter", Action: "open"}},
		map[string]*Action{"open": {Push: "b"}})
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "from:") {
		t.Fatalf("expected `from:` to be required for a push, got: %v", err)
	}
}

// bind: on a push fills the DESTINATION screen's parameters, which that
// screen's sources declare — so it must not be checked against the
// action's own inputs.
func TestPushBindIsNotCheckedAgainstInputs(t *testing.T) {
	c := pushConfig(
		[]ActionBinding{{
			Key: "enter", Action: "open", From: "tbl",
			Bind: map[string]string{"namespace": "${selection}"},
		}},
		map[string]*Action{"open": {Push: "b"}})
	if err := c.Validate(); err != nil {
		t.Fatalf("a push's bind names destination params, not inputs: %v", err)
	}
}

func TestPushRejectsRunAndInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  *Action
		want string
	}{
		{"run", &Action{Push: "b", Run: []string{"echo"}}, "run:"},
		{"inputs", &Action{Push: "b", Inputs: map[string]*Parameter{"x": {}}}, "inputs:"},
	} {
		c := pushConfig(
			[]ActionBinding{{Key: "enter", Action: "open", From: "tbl"}},
			map[string]*Action{"open": tc.act})
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: expected rejection mentioning %q, got: %v", tc.name, tc.want, err)
		}
	}
}

// A binding with no key at all is menu-only, which is the payoff: the
// letter budget stops being the ceiling on how many verbs a screen has.
func TestBindingKeyIsRequiredForNow(t *testing.T) {
	c := pushConfig(
		[]ActionBinding{{Action: "open", From: "tbl"}},
		map[string]*Action{"open": {Push: "b"}})
	if err := c.Validate(); err == nil {
		t.Skip("keyless bindings are accepted; menu-only is live")
	} else if !strings.Contains(err.Error(), "key") {
		t.Errorf("unexpected error: %v", err)
	}
}
