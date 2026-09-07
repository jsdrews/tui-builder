package config

import (
	"strings"
	"testing"
)

func TestThemeCycleKeyDefaults(t *testing.T) {
	cases := []struct{ set, want string }{
		{"", "t"},  // unset → default
		{"-", ""},  // pinned palette
		{"T", "T"}, // custom
		{"ctrl+t", "ctrl+t"},
	}
	for _, tc := range cases {
		a := App{ThemeKey: tc.set}
		if got := a.ThemeCycleKey(); got != tc.want {
			t.Errorf("ThemeKey %q: got %q, want %q", tc.set, got, tc.want)
		}
	}
}

// Same failure as the console key: the shell claims it before a screen
// sees it, so the binding would read correctly and never fire.
func TestActionKeyCannotShadowThemeCycle(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.Actions = []Action{{
		Key: "t", Source: "items", Run: []string{"echo", "hi"},
	}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error binding an action to the theme key")
	}
	if !strings.Contains(err.Error(), "theme") {
		t.Errorf("error should explain the collision, got: %v", err)
	}
}

func TestActionKeyFreeWhenThemeCyclePinned(t *testing.T) {
	c := promptConfig()
	c.App.ThemeKey = "-"
	c.TUI.Screen.Actions = []Action{{
		Key: "t", Source: "items", Run: []string{"echo", "hi"},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestActionKeyCollidesWithCustomThemeKey(t *testing.T) {
	c := promptConfig()
	c.App.ThemeKey = "P"
	c.TUI.Screen.Actions = []Action{{
		Key: "P", Source: "items", Run: []string{"echo", "hi"},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected a collision against the custom theme key")
	}
	// And "t" is free once cycling moved off it.
	c.TUI.Screen.Actions[0].Key = "t"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
