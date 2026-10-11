package config

import (
	"strings"
	"testing"
)

// tabsConfig is promptConfig with its screen turned into two tabs over
// the same list.
func tabsConfig(set func(s *Screen)) *Config {
	c := promptConfig()
	c.TUI.Screen = Screen{Title: "Items", Tabs: []Tab{
		{Label: "One", Layout: Node{Component: "items"}},
		{Label: "Two", Layout: Node{Component: "items"}},
	}}
	if set != nil {
		set(&c.TUI.Screen)
	}
	return c
}

func TestTabsValidation(t *testing.T) {
	no := false
	cases := []struct {
		name string
		set  func(s *Screen)
		want string
	}{
		{"same component in two tabs", nil, ""},
		{"all strip settings", func(s *Screen) {
			s.StripPos, s.Initial, s.PrevKey, s.NextKey, s.NumberKeys = "bottom", "Two", "[", "-", &no
		}, ""},
		{"layout and tabs", func(s *Screen) { s.Layout = Node{Component: "items"} }, "not both"},
		{"screen actions", func(s *Screen) { s.Actions = []ActionBinding{{Key: "d", Action: "run"}} }, "no `actions:` of its own"},
		{"missing label", func(s *Screen) { s.Tabs[1].Label = "" }, "`label:` is required"},
		{"duplicate label", func(s *Screen) { s.Tabs[1].Label = "One" }, "already used"},
		{"bad initial", func(s *Screen) { s.Initial = "Three" }, "not a tab label"},
		{"bad strip", func(s *Screen) { s.StripPos = "left" }, "strip_pos"},
		{"switch key on the quit key", func(s *Screen) { s.NextKey = "q" }, "app.quit_key"},
		{"switch key on refresh", func(s *Screen) { s.PrevKey = "r" }, "reserved"},
		{"component twice in one tab", func(s *Screen) {
			s.Tabs[0].Layout = Node{VStack: []Item{{Node: Node{Component: "items"}}, {Node: Node{Component: "items"}}}}
		}, "placed 2 times"},
	}
	for _, tc := range cases {
		c := tabsConfig(tc.set)
		c.Actions = map[string]*Action{"run": {Run: []string{"echo"}}}
		err := c.Validate()
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestStripSettingsNeedTabs(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.StripPos = "bottom"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "only applies to a screen with `tabs:`") {
		t.Errorf("want strip_pos rejected without tabs, got %v", err)
	}
}

// A tab's bindings are checked against that tab's components, and its
// inline actions are hoisted like a screen's.
func TestTabActionsAreScopedAndHoisted(t *testing.T) {
	c := tabsConfig(func(s *Screen) {
		s.Tabs[1].Actions = []ActionBinding{{Key: "enter", Name: "open", Inline: Action{Run: []string{"echo", "${inputs.x}"},
			Inputs: map[string]*Parameter{"x": {Required: true}}}, From: "items", Bind: map[string]string{"x": "${selection}"}}}
	})
	if err := hoistInlineActions(c); err != nil {
		t.Fatal(err)
	}
	if c.TUI.Screen.Tabs[1].Actions[0].Action != "open" || c.Actions["open"] == nil {
		t.Fatalf("inline tab action not hoisted: %+v", c.TUI.Screen.Tabs[1].Actions[0])
	}
	if err := c.Validate(); err != nil {
		t.Errorf("valid tab action: %v", err)
	}

	c = tabsConfig(func(s *Screen) {
		s.Tabs[0].Layout = Node{Component: "other"}
		s.Tabs[1].Actions = []ActionBinding{{Key: "enter", Action: "run", From: "other", Bind: map[string]string{"x": "${selection}"}}}
	})
	c.TUI.Components["other"] = &Component{Type: "list", Items: []string{"a"}}
	c.Actions = map[string]*Action{"run": {Run: []string{"echo", "${inputs.x}"}, Inputs: map[string]*Parameter{"x": {Required: true}}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "tabs[1]") {
		t.Errorf("a binding's from: must be in its own tab, got %v", err)
	}
}
