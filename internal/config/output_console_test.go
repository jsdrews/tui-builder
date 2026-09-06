package config

import (
	"strings"
	"testing"
)

func TestOutputConsoleKeyDefaults(t *testing.T) {
	cases := []struct{ set, want string }{
		{"", "o"},  // unset → default
		{"-", ""},  // explicit disable
		{"O", "O"}, // custom
		{"ctrl+o", "ctrl+o"},
	}
	for _, tc := range cases {
		a := App{OutputKey: tc.set}
		if got := a.OutputConsoleKey(); got != tc.want {
			t.Errorf("OutputKey %q: got %q, want %q", tc.set, got, tc.want)
		}
	}
}

// The shell claims the console key globally, so an action bound to it
// would read correctly in the config and never fire.
func TestActionKeyCannotShadowOutputConsole(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.Actions = []Action{{
		Key: "o", Source: "items", Run: []string{"open", "${selection}"},
	}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error binding an action to the console key")
	}
	if !strings.Contains(err.Error(), "output console key") {
		t.Errorf("error should explain the collision, got: %v", err)
	}
}

func TestActionKeyFreeWhenConsoleDisabled(t *testing.T) {
	c := promptConfig()
	c.App.OutputKey = "-"
	c.TUI.Screen.Actions = []Action{{
		Key: "o", Source: "items", Run: []string{"open", "${selection}"},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestActionKeyCollidesWithCustomConsoleKey(t *testing.T) {
	c := promptConfig()
	c.App.OutputKey = "L"
	c.TUI.Screen.Actions = []Action{{
		Key: "L", Source: "items", Run: []string{"echo", "hi"},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected a collision against the custom console key")
	}
	// And "o" is free once the console moved off it.
	c.TUI.Screen.Actions[0].Key = "o"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
