package config

import (
	"strings"
	"testing"
)

func TestRemoteExtrasValidation(t *testing.T) {
	find := func(f *FindConfig) func(*Config) {
		return func(c *Config) { c.Data.Sources["events"].Window.Find = f }
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"anchored find", find(&FindConfig{Body: map[string]any{"q": "${window.term}"}}), ""},
		{"find with nothing to send", find(&FindConfig{}), "needs `params:` or `body:`"},
		{"position on an anchored find", find(&FindConfig{Params: map[string]any{"q": "x"}, Position: "rank"}), "doesn't apply to an Anchored source"},
		{"viewport delay", func(c *Config) { c.Data.Sources["events"].Window.ViewportDelay = "150ms" }, ""},
		{"viewport delay off", func(c *Config) { c.Data.Sources["events"].Window.ViewportDelay = "-" }, ""},
		{"bad viewport delay", func(c *Config) { c.Data.Sources["events"].Window.ViewportDelay = "soon" }, "viewport_delay"},
		{"anchor template on env", func(c *Config) { c.Data.Sources["events"].Window.Anchor = "${env.START}" }, ""},
		{"anchor template on an input", func(c *Config) {
			c.Data.Sources["events"].Window.Anchor = "${input.jump}"
			c.TUI.Components["jump"] = &Component{Type: "input"}
			c.TUI.Screen.Layout = Node{VStack: []Item{{Node: Node{Component: "events"}}, {Node: Node{Component: "jump"}}}}
		}, ""},
		{"anchor template on a missing input", func(c *Config) { c.Data.Sources["events"].Window.Anchor = "${input.jump}" }, "window.anchor: ${input.jump} needs"},
	} {
		err := anchoredConfig(tc.mutate).Validate()
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

func TestSeekableFindNeedsAPosition(t *testing.T) {
	c := windowedConfig()
	for _, s := range c.Data.Sources {
		if s.Window != nil {
			s.Window.Find = &FindConfig{Params: map[string]any{"q": "${window.term}"}}
		}
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "find.position: required") {
		t.Errorf("want position required on a paged source, got %v", err)
	}
	c = execWindowedConfig()
	for _, s := range c.Data.Sources {
		if s.Window != nil {
			s.Window.Find = &FindConfig{Params: map[string]any{"q": "x"}, Position: "n"}
		}
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "find: http-only") {
		t.Errorf("want find rejected on exec, got %v", err)
	}
}
