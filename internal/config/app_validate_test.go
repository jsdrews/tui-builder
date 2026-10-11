package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestShellSettingsValidation(t *testing.T) {
	no := false
	cases := []struct {
		name string
		set  func(a *App)
		want string // "" = valid
	}{
		{"defaults", func(a *App) {}, ""},
		{"all set", func(a *App) {
			a.QuitKey, a.SuspendKey, a.HelpKey = "Q", "-", "h"
			a.Mouse, a.DoubleClick, a.EscPops = "off", "400ms", &no
			a.Output = &OutputConfig{MaxRecords: 500, ExportDir: "/tmp/logs"}
			a.ActionsTitle, a.FormSpacing = "Verbs", 1
			a.Keys = map[string]KeyList{"up": {"k", "up"}, "filter": {"F"}}
		}, ""},
		{"quit off", func(a *App) { a.QuitKey = "-" }, "app.quit_key"},
		{"help off", func(a *App) { a.HelpKey = "-" }, "app.help_key"},
		{"quit on theme key", func(a *App) { a.QuitKey = "t" }, `key "t" both`},
		{"help on refresh", func(a *App) { a.HelpKey = "r" }, "refreshes"},
		{"bad mouse", func(a *App) { a.Mouse = "drag" }, "app.mouse"},
		{"bad double click", func(a *App) { a.DoubleClick = "fast" }, "app.double_click"},
		{"negative spacing", func(a *App) { a.FormSpacing = -1 }, "app.form_spacing"},
		{"negative records", func(a *App) { a.Output = &OutputConfig{MaxRecords: -1} }, "max_records"},
		{"unknown binding", func(a *App) { a.Keys = map[string]KeyList{"jump": {"J"}} }, "app.keys.jump"},
		{"empty binding", func(a *App) { a.Keys = map[string]KeyList{"up": {}} }, "at least one key"},
		{"binding on refresh", func(a *App) { a.Keys = map[string]KeyList{"top": {"r"}} }, "reserved"},
		{"binding on quit", func(a *App) { a.Keys = map[string]KeyList{"bottom": {"q"}} }, "app.quit_key"},
		{"wide mask glyph", func(a *App) { a.Glyphs.Mask = "**" }, "app.glyphs.mask"},
		{"wide activity glyph", func(a *App) { a.Glyphs.ActivityOK = "ok" }, "app.glyphs.activity_ok"},
	}
	for _, tc := range cases {
		c := promptConfig()
		tc.set(&c.App)
		err := c.Validate()
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: Validate: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error mentioning %q, got %v", tc.name, tc.want, err)
		}
	}
}

// Moving a shell key moves its reservation: the old key is free for an
// action, and the new one isn't.
func TestReservedKeysFollowRebinding(t *testing.T) {
	c := promptConfig()
	c.App.QuitKey = "Q"
	bindKey(c, "q")
	if err := c.Validate(); err != nil {
		t.Errorf("q should be free once quit moves to Q: %v", err)
	}

	c = promptConfig()
	c.App.QuitKey = "Q"
	bindKey(c, "Q")
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "app.quit_key") {
		t.Errorf("want Q rejected as the quit key, got %v", err)
	}

	// ctrl+c quits whatever quit_key says.
	c = promptConfig()
	c.App.QuitKey = "Q"
	bindKey(c, "ctrl+c")
	if err := c.Validate(); err == nil {
		t.Error("ctrl+c should stay reserved")
	}

	c = promptConfig()
	c.App.SuspendKey = "-"
	bindKey(c, "ctrl+z")
	if err := c.Validate(); err != nil {
		t.Errorf("ctrl+z should be free once suspending is off: %v", err)
	}
}

func TestKeymapAcceptsAKeyOrAList(t *testing.T) {
	var a App
	if err := yaml.Unmarshal([]byte("keys: {filter: F, up: [k, up]}"), &a); err != nil {
		t.Fatal(err)
	}
	if got := a.Keys["filter"]; len(got) != 1 || got[0] != "F" {
		t.Errorf("filter = %v, want [F]", got)
	}
	if got := a.Keys["up"]; len(got) != 2 || got[1] != "up" {
		t.Errorf("up = %v, want [k up]", got)
	}
}
