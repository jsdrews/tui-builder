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

// A binding with no key is menu-only. That is the payoff of the menu:
// the alphabet stops capping how many verbs a screen can have.
func TestKeylessBindingIsValid(t *testing.T) {
	c := pushConfig(
		[]ActionBinding{{Action: "open", From: "tbl"}},
		map[string]*Action{"open": {Push: "b"}})
	if err := c.Validate(); err != nil {
		t.Fatalf("a keyless binding should be legal: %v", err)
	}
}

// Two keyless bindings don't collide — there is no key to collide on.
func TestTwoKeylessBindingsDoNotCollide(t *testing.T) {
	c := pushConfig(
		[]ActionBinding{{Action: "open", From: "tbl"}, {Action: "open2", From: "tbl"}},
		map[string]*Action{"open": {Push: "b"}, "open2": {Push: "b"}})
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// A push naming a screen that doesn't exist is a load error, not an
// alert the first time the key is pressed — bound or not.
func TestPushToUndefinedScreenFailsLoad(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindings []ActionBinding
	}{
		{"bound", []ActionBinding{{Key: "enter", Action: "open", From: "tbl"}}},
		{"unbound", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := pushConfig(tc.bindings, map[string]*Action{"open": {Push: "nope"}})
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), `actions.open: push: screen "nope" not defined`) {
				t.Fatalf("expected an undefined-screen error, got: %v", err)
			}
			if !strings.Contains(err.Error(), "a, b") {
				t.Errorf("error should list the screens there are: %v", err)
			}
		})
	}
}

// A single-screen config has nowhere to push to.
func TestPushFromSingleScreenFailsLoad(t *testing.T) {
	c := pushConfig(nil, map[string]*Action{"open": {Push: "b"}})
	c.TUI.Screen = *c.TUI.Screens["a"]
	c.TUI.Screens, c.TUI.Initial = nil, ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "single `tui.screen:`") {
		t.Fatalf("expected a single-screen error, got: %v", err)
	}
}
