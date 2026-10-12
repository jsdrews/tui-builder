package config

import (
	"strings"
	"testing"
)

// inputsConfig: a search input and a table whose source takes q.
func inputsConfig(edit func(c *Config)) *Config {
	c := &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"items": NewEntry(&Source{Type: "exec", Command: []string{"echo", "${params.q}"},
				Parameters: map[string]*Parameter{"q": {}, "n": {}}}),
		}},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"search": {Type: "input", Title: "Search"},
				"flag":   {Type: "toggle"},
				"items": {Type: "list", Source: "items", Item: "name",
					OnInput: &OnInput{Bind: map[string]string{"q": "${input.search}"}}},
			},
			Screen: Screen{Layout: Node{VStack: []Item{
				{Node: Node{Component: "search"}},
				{Node: Node{Component: "flag"}},
				{Node: Node{Component: "items"}},
			}}},
		},
	}
	if edit != nil {
		edit(c)
	}
	return c
}

func TestInputValidation(t *testing.T) {
	cases := []struct {
		name string
		edit func(c *Config)
		want string
	}{
		{"valid", nil, ""},
		{"input fields", func(c *Config) {
			s := c.TUI.Components["search"]
			s.Initial, s.Placeholder, s.CharLimit, s.Mask = "x", "type…", 20, true
		}, ""},
		{"toggle labels", func(c *Config) {
			f := c.TUI.Components["flag"]
			f.Initial, f.YesLabel, f.NoLabel = "true", "on", "off"
		}, ""},
		{"input with a source", func(c *Config) { c.TUI.Components["search"].Source = "items" }, "holds a value"},
		{"bad toggle initial", func(c *Config) { c.TUI.Components["flag"].Initial = "maybe" }, "want true|false"},
		{"placeholder on a toggle", func(c *Config) { c.TUI.Components["flag"].Placeholder = "x" }, "`placeholder:` is only valid on input"},
		{"yes_label on a list", func(c *Config) { c.TUI.Components["items"].YesLabel = "x" }, "`yes_label:` is only valid on toggle"},
		{"flex in a vstack", func(c *Config) { c.TUI.Screen.Layout.VStack[0].Flex = 2 }, "3 rows tall"},
		{"fixed 3 in a vstack", func(c *Config) { c.TUI.Screen.Layout.VStack[0].Fixed = 3 }, ""},
		{"width in an hstack", func(c *Config) {
			c.TUI.Screen.Layout = Node{VStack: []Item{
				{Node: Node{HStack: []Item{{Flex: 2, Node: Node{Component: "search"}}, {Fixed: 20, Node: Node{Component: "flag"}}}}, Fixed: 3},
				{Node: Node{Component: "items"}},
			}}
		}, ""},
		{"on_input without bind", func(c *Config) { c.TUI.Components["items"].OnInput.Bind = nil }, "`bind:` is required"},
		{"on_input undeclared param", func(c *Config) { c.TUI.Components["items"].OnInput.Bind["zz"] = "${input.search}" }, "declares no parameter"},
		{"on_input and on_cursor on one param", func(c *Config) {
			c.TUI.Components["items"].OnCursor = &OnCursor{Source: "items", Bind: map[string]string{"q": "${cursor}"}}
		}, "bind each parameter once"},
		{"unknown input", func(c *Config) { c.TUI.Components["items"].OnInput.Bind["q"] = "${input.nope}" }, "${input.nope} needs an input or toggle"},
		{"input that isn't one", func(c *Config) { c.TUI.Components["items"].OnInput.Bind["q"] = "${input.items}" }, "${input.items} needs"},
		{"binding confirm", func(c *Config) {
			c.Actions = map[string]*Action{"go": {Run: []string{"true"}}}
			c.TUI.Screen.Actions = []ActionBinding{{Key: "d", Action: "go", Confirm: Confirm{Message: "for ${input.gone}?"}}}
		}, "actions[0].confirm"},
		{"action definition", func(c *Config) {
			c.Actions = map[string]*Action{"go": {Run: []string{"echo", "${input.missing}"}}}
		}, "actions.go: ${input.missing}"},
	}
	for _, tc := range cases {
		err := inputsConfig(tc.edit).Validate()
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

// ${input.NAME} reads an input on the same screen: on another screen of
// a multi-screen config it's an error.
func TestInputRefsAreScopedToTheScreen(t *testing.T) {
	c := inputsConfig(nil)
	c.TUI.Screens = map[string]*Screen{
		"main":  {Layout: Node{VStack: []Item{{Node: Node{Component: "search"}}, {Node: Node{Component: "flag"}}}}},
		"other": {Layout: Node{Component: "items"}},
	}
	c.TUI.Initial = "main"
	c.TUI.Screen = Screen{}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "on this screen") {
		t.Errorf("want the other screen's ${input.search} rejected, got %v", err)
	}
}
