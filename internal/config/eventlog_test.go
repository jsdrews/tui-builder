package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func eventlogConfig(mutate func(*Config)) *Config {
	c := &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"events": {Type: "http", URL: "http://example/events",
				Window: &WindowConfig{OffsetParam: "offset", LimitParam: "limit", SearchParam: "q"}},
			"job": {Type: "http", URL: "http://example/job"},
		}},
		TUI: TUIBlock{
			Components: map[string]*Component{"events": {
				Type: "eventlog", Source: "events", Key: Path{"id"}, Text: Path{"stdout"},
			}},
			Screen: Screen{Layout: Node{Component: "events"}},
		},
	}
	if mutate != nil {
		mutate(c)
	}
	return c
}

func TestEventlogValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"minimal", nil, ""},
		{"start newest", func(c *Config) { c.TUI.Components["events"].Start = "newest" }, ""},
		{"growing always", func(c *Config) { c.Data.Sources["events"].Window.Growing = &Growing{Always: true} }, ""},
		{"growing on a job", func(c *Config) {
			c.Data.Sources["events"].Window.Growing = &Growing{Source: "job", While: "status == 'running'"}
			c.Data.Sources["events"].Window.FollowEvery = "1s"
		}, ""},
		{"no key", func(c *Config) { c.TUI.Components["events"].Key = nil }, "needs `key:`"},
		{"no text", func(c *Config) { c.TUI.Components["events"].Text = nil }, "needs `text:`"},
		{"bad start", func(c *Config) { c.TUI.Components["events"].Start = "middle" }, "unknown start"},
		{"not windowed", func(c *Config) { c.Data.Sources["events"].Window = nil }, "binds only to a source that declares `window:`"},
		{"eventlog fields elsewhere", func(c *Config) {
			c.TUI.Components["list"] = &Component{Type: "list", Items: []string{"a"}, Text: Path{"x"}}
		}, "`text:` is only valid on an eventlog"},
		{"growing on a table", func(c *Config) {
			c.Data.Sources["events"].Window.Growing = &Growing{Always: true}
			c.TUI.Components["events"] = &Component{Type: "table", Source: "events",
				Columns: []Column{{Title: "T", Value: Path{"t"}}}}
		}, "only an eventlog can follow"},
		{"growing with refresh", func(c *Config) {
			c.Data.Sources["events"].Window.Growing = &Growing{Always: true}
			c.Data.Sources["events"].Refresh = "5s"
		}, "`refresh:` and `window.growing:` are mutually exclusive"},
		{"follow_every without growing", func(c *Config) { c.Data.Sources["events"].Window.FollowEvery = "1s" }, "set without `growing:`"},
		{"growing names a missing source", func(c *Config) {
			c.Data.Sources["events"].Window.Growing = &Growing{Source: "nope", While: "true"}
		}, `source "nope" not defined`},
		{"growing condition doesn't compile", func(c *Config) {
			c.Data.Sources["events"].Window.Growing = &Growing{Source: "job", While: "status =="}
		}, "growing.while"},
		{"half a condition", func(c *Config) {
			c.Data.Sources["events"].Window.Growing = &Growing{Source: "job"}
		}, "both `source:` and `while:`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := eventlogConfig(tc.mutate).Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestGrowingAcceptsBothForms(t *testing.T) {
	var w struct {
		A Growing `yaml:"a"`
		B Growing `yaml:"b"`
	}
	src := "a: true\nb: {source: job, while: \"status == 'running'\"}\n"
	if err := yaml.Unmarshal([]byte(src), &w); err != nil {
		t.Fatal(err)
	}
	if !w.A.Always {
		t.Error("`growing: true` didn't set Always")
	}
	if w.B.Always || w.B.Source != "job" || w.B.While == "" {
		t.Errorf("map form decoded as %+v", w.B)
	}
	var bad struct {
		G Growing `yaml:"g"`
	}
	if err := yaml.Unmarshal([]byte("g: sometimes\n"), &bad); err == nil {
		t.Error("`growing: sometimes` should be an error")
	}
}
