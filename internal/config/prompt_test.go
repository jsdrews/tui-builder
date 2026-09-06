package config

import (
	"strings"
	"testing"
)

// promptConfig is a minimal valid config with one static source bound to
// a list, so a test can attach prompts and call Validate without the
// rest of the schema getting in the way.
func promptConfig() *Config {
	return &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"items": NewEntry(&Source{
				Type: "static",
				Data: []any{"a", "b"},
			}),
		}},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"items": {Type: "list", Source: "items", Item: "name"},
			},
			Screen: Screen{Title: "Items", Layout: Node{Component: "items"}},
		},
	}
}

func TestPromptTypesAccepted(t *testing.T) {
	for _, ty := range []string{"", "text", "password", "select", "confirm"} {
		c := promptConfig()
		p := Prompt{Key: "TOKEN", Type: ty}
		if ty == "select" {
			p.Options = []string{"one", "two"}
		}
		c.App.Prompts = []Prompt{p}
		if err := c.Validate(); err != nil {
			t.Errorf("type %q: Validate: %v", ty, err)
		}
	}
}

// A typo'd type used to fall through to a text field. That was harmless
// until password existed; now it renders a token in the clear, which is
// the one thing the field is for.
func TestPromptUnknownTypeRejected(t *testing.T) {
	c := promptConfig()
	c.App.Prompts = []Prompt{{Key: "TOKEN", Type: "passwrod"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for an unknown prompt type")
	}
	if !strings.Contains(err.Error(), "app.prompts[0]") || !strings.Contains(err.Error(), "passwrod") {
		t.Errorf("error should name the path and the bad type, got: %v", err)
	}
}

func TestPromptKeyRequired(t *testing.T) {
	c := promptConfig()
	c.App.Prompts = []Prompt{{Type: "password"}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("expected a missing-key error, got: %v", err)
	}
}

func TestPromptSelectNeedsOptions(t *testing.T) {
	c := promptConfig()
	c.App.Prompts = []Prompt{{Key: "ENV", Type: "select"}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "needs options") {
		t.Fatalf("expected a missing-options error, got: %v", err)
	}
}

// Action prompts route through the same validator, so the rules hold on
// both lists and the path names which one failed.
func TestActionPromptTypesValidated(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.Actions = []Action{{
		Key:     "x",
		Source:  "items",
		Run:     []string{"echo", "hi"},
		Prompts: []Prompt{{Key: "TOKEN", Type: "nope"}},
	}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for an unknown action prompt type")
	}
	if !strings.Contains(err.Error(), "actions[0].prompts[0]") {
		t.Errorf("error should name the action prompt path, got: %v", err)
	}
}

func TestActionPasswordPromptAccepted(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.Actions = []Action{{
		Key:     "x",
		Source:  "items",
		Run:     []string{"echo", "${prompt.TOKEN}"},
		Prompts: []Prompt{{Key: "TOKEN", Type: "password"}},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
