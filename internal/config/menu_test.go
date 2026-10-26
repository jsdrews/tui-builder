package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadMenu loads a two-screen config whose home screen holds the given
// menu component YAML (indented under `home_menu:`).
func loadMenu(t *testing.T, menu string) (*Config, error) {
	t.Helper()
	src := `
data:
  sources:
    items: {type: static, data: []}
    db:
      type: static
      data: []
      parameters: {engine: {type: string, required: true}}
tui:
  components:
    home_menu:
` + menu + `
    things: {type: list, items: [a, b]}
    db_table: {type: table, source: db, columns: [{title: N, value: n}]}
  screens:
    home: {layout: {component: home_menu}}
    things: {layout: {component: things}}
    db: {layout: {component: db_table}}
  initial: home
`
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// A menu's items are maps; a list's stay strings in the same config.
func TestMenuItemsDecode(t *testing.T) {
	t.Setenv("TEST_MENU_ENGINE", "mysql")
	c, err := loadMenu(t, `
      type: menu
      title: Sections
      items:
        - {group: data, label: Things, screen: things, description: "all of them"}
        - {group: data, label: "DB (${env.TEST_MENU_ENGINE})", screen: db, bind: {engine: "${env.TEST_MENU_ENGINE}"}}`)
	if err != nil {
		t.Fatal(err)
	}
	m := c.TUI.Components["home_menu"]
	if len(m.MenuItems) != 2 || len(m.Items) != 0 {
		t.Fatalf("menu items = %+v, list items = %v", m.MenuItems, m.Items)
	}
	if got := m.MenuItems[1]; got.Screen != "db" || got.Group != "data" || got.Bind["engine"] != "${env.TEST_MENU_ENGINE}" {
		t.Errorf("item 1 = %+v", got)
	}
	if got := c.TUI.Components["things"].Items; len(got) != 2 {
		t.Errorf("a list's items should still decode as strings, got %v", got)
	}
	c.SubstituteEnv()
	if got := m.MenuItems[1]; got.Label != "DB (mysql)" || got.Bind["engine"] != "mysql" {
		t.Errorf("${env.*} not substituted in a menu item: %+v", got)
	}
}

func TestMenuValidation(t *testing.T) {
	for _, tc := range []struct {
		name, menu, want string
	}{
		{"no items", `
      type: menu`, "a menu needs `items:`"},
		{"no label", `
      type: menu
      items: [{screen: things}]`, "items[0]: `label:` is required"},
		{"no screen", `
      type: menu
      items: [{label: X}]`, "items[0]: `screen:` is required"},
		{"dangling screen", `
      type: menu
      items: [{label: X, screen: nope}]`, `tui.components.home_menu.items[0]: screen "nope" not defined in tui.screens`},
		{"selection token", `
      type: menu
      items: [{label: X, screen: db, bind: {engine: "${selection.Name}"}}]`, "bind.engine: ${selection…} isn't available in a menu"},
		{"source", `
      type: menu
      source: items
      items: [{label: X, screen: things}]`, "doesn't display a source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadMenu(t, tc.menu)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// A menu owns enter, so it can't be a binding's from:.
func TestMenuCantBeFrom(t *testing.T) {
	c := pushConfig(nil, map[string]*Action{"open": {Push: "b"}})
	c.TUI.Components["m"] = &Component{Type: "menu", MenuItems: []MenuItem{{Label: "B", Screen: "b"}}}
	c.TUI.Screens["a"].Layout = Node{Component: "m"}
	c.TUI.Screens["a"].Actions = []ActionBinding{{Key: "p", Action: "open", From: "m"}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), `from "m" is a menu`) {
		t.Fatalf("want a menu-as-from error, got %v", err)
	}
}
