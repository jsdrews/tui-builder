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

// A prompt is a Key plus an inlined Parameter, so the vocabulary is the
// DATA type — the widget follows from it.
func TestPromptTypesAccepted(t *testing.T) {
	for _, ty := range []string{"", "string", "int", "bool", "duration"} {
		c := promptConfig()
		c.App.Prompts = []Prompt{{Key: "TOKEN", Parameter: Parameter{Type: ty}}}
		if err := c.Validate(); err != nil {
			t.Errorf("type %q: Validate: %v", ty, err)
		}
	}
}

// A typo'd type falls through to a plain text box. Harmless once, but a
// mistyped `mask:` field would put a token on screen in the clear.
func TestPromptUnknownTypeRejected(t *testing.T) {
	c := promptConfig()
	c.App.Prompts = []Prompt{{Key: "TOKEN", Parameter: Parameter{Type: "passwrod"}}}
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
	c.App.Prompts = []Prompt{{Parameter: Parameter{Mask: true}}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("expected a missing-key error, got: %v", err)
	}
}

// Masking is a rendering choice on a string, so it lives beside the type
// rather than on it — `type: password` would put a widget name on the
// axis that holds string / int / bool / duration.
func TestPromptMaskAccepted(t *testing.T) {
	c := promptConfig()
	c.App.Prompts = []Prompt{{Key: "TOKEN", Parameter: Parameter{Mask: true}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// A select renders every choice on screen, so there is nothing left to
// mask — asking for both means one of them isn't going to happen.
func TestPromptMaskWithOptionsRejected(t *testing.T) {
	c := promptConfig()
	c.App.Prompts = []Prompt{{
		Key:       "ENV",
		Parameter: Parameter{Mask: true, Options: []string{"a", "b"}},
	}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "mask") {
		t.Fatalf("expected a mask/options conflict, got: %v", err)
	}
}

func TestPromptRequiredAndDefaultConflict(t *testing.T) {
	c := promptConfig()
	c.App.Prompts = []Prompt{{
		Key:       "ENV",
		Parameter: Parameter{Required: true, Default: "x"},
	}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a required/default conflict, got: %v", err)
	}
}

// Action inputs share the Parameter vocabulary, so they share the rules
// — and the error names which input failed.
func TestActionInputTypesValidated(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.Actions = []ActionBinding{{Key: "x", Action: "run"}}
	c.Actions = map[string]*Action{"run": {
		Run:    []string{"echo", "${inputs.token}"},
		Inputs: map[string]*Parameter{"token": {Type: "nope"}},
	}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for an unknown action input type")
	}
	if !strings.Contains(err.Error(), "inputs.token") {
		t.Errorf("error should name the input, got: %v", err)
	}
}

func TestActionMaskedInputAccepted(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.Actions = []ActionBinding{{Key: "x", Action: "run"}}
	c.Actions = map[string]*Action{"run": {
		Run:    []string{"echo", "${inputs.token}"},
		Inputs: map[string]*Parameter{"token": {Mask: true}},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
