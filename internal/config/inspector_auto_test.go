package config

import (
	"strings"
	"testing"
)

// Tests for the `auto: true` inspector schema (features B + C from the
// tui-builder integration batch). Behavior under check:
//
//   1. Auto + Fields declared → error (mutex; can't do both).
//   2. Auto without Source → error (nothing to derive from).
//   3. Auto on non-inspector → error (only inspector has FromAny wiring).
//   4. Auto + Source + no Fields → accepted.

func TestValidateInspectorAutoMutexWithFields(t *testing.T) {
	c := &Component{
		Type:   "inspector",
		Source: "src",
		Auto:   true,
		Fields: []InspectorField{{Label: "Name", Path: "name"}},
	}
	err := c.validate("tui.components.x")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("want mutex error, got %v", err)
	}
}

func TestValidateInspectorAutoWithoutSourceRejected(t *testing.T) {
	c := &Component{Type: "inspector", Auto: true}
	err := c.validate("tui.components.x")
	if err == nil || !strings.Contains(err.Error(), "needs a `source:`") {
		t.Errorf("want missing-source error, got %v", err)
	}
}

func TestValidateInspectorAutoRejectedOnNonInspector(t *testing.T) {
	c := &Component{Type: "list", Source: "src", Item: "name", Auto: true}
	err := c.validate("tui.components.x")
	if err == nil || !strings.Contains(err.Error(), "only valid on inspector") {
		t.Errorf("want non-inspector rejection, got %v", err)
	}
}

func TestValidateInspectorAutoHappyPath(t *testing.T) {
	c := &Component{Type: "inspector", Source: "src", Auto: true}
	if err := c.validate("tui.components.x"); err != nil {
		t.Errorf("valid auto inspector should not error, got %v", err)
	}
}
