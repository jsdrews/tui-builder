package config

import (
	"strings"
	"testing"
)

func activityConfig(mutate func(*Config)) *Config {
	c := &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"pods": {Type: "http", URL: "http://example/pods", Refresh: "5s"},
		}},
		TUI: TUIBlock{
			Components: map[string]*Component{"pods": {
				Type: "table", Source: "pods", MarkKey: Path{"uid"},
				Columns: []Column{
					{Title: "Name", Value: Path{"name"}},
					{Title: "Phase", Value: Path{"status.phase"}},
				},
				Activity: &Activity{Status: Path{"status.phase"}, Busy: []string{"Pending"}},
			}},
			Screen: Screen{Layout: Node{Component: "pods"}},
		},
	}
	if mutate != nil {
		mutate(c)
	}
	return c
}

func TestActivityValidation(t *testing.T) {
	act := func(c *Config) *Activity { return c.TUI.Components["pods"].Activity }
	for _, tc := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"table, column found by status path", nil, ""},
		{"busy_when", func(c *Config) { act(c).Busy = nil; act(c).BusyWhen = "status.phase != 'Running'" }, ""},
		{"explicit column", func(c *Config) { act(c).Column = "Name" }, ""},
		{"list", func(c *Config) {
			c.TUI.Components["pods"] = &Component{Type: "list", Source: "pods", Item: "name",
				Activity: &Activity{Status: Path{"phase"}, Busy: []string{"Pending"}}}
		}, ""},
		{"no status", func(c *Config) { act(c).Status = nil }, "`status:` is required"},
		{"busy and busy_when", func(c *Config) { act(c).BusyWhen = "true" }, "not both"},
		{"neither", func(c *Config) { act(c).Busy = nil }, "set `busy:`"},
		{"busy_when doesn't compile", func(c *Config) { act(c).Busy = nil; act(c).BusyWhen = "a ==" }, "busy_when"},
		{"no mark_key", func(c *Config) { c.TUI.Components["pods"].MarkKey = nil }, "needs `mark_key:`"},
		{"no column shows the status", func(c *Config) { act(c).Status = Path{"status.reason"} }, "set `column:`"},
		{"column isn't a title", func(c *Config) { act(c).Column = "Nope" }, `"Nope" isn't one of the table's column titles`},
		{"column on a list", func(c *Config) {
			c.TUI.Components["pods"] = &Component{Type: "list", Source: "pods", Item: "name",
				Activity: &Activity{Status: Path{"phase"}, Busy: []string{"x"}, Column: "A"}}
		}, "`column:` is table-only"},
		{"on an inspector", func(c *Config) {
			c.TUI.Components["pods"] = &Component{Type: "inspector", Source: "pods", Auto: true,
				Activity: &Activity{Status: Path{"phase"}, Busy: []string{"x"}}}
		}, "only a table, list or tree"},
		{"streaming source", func(c *Config) { c.Data.Sources["pods"].Follow = true; c.Data.Sources["pods"].Refresh = "" }, "not available on a streaming source"},
		{"windowed source", func(c *Config) {
			c.Data.Sources["pods"].Window = &WindowConfig{OffsetParam: "o", LimitParam: "l", SearchParam: "q", TotalPath: "total"}
			c.Data.Sources["pods"].Refresh = ""
		}, "not available on a windowed source"},
		{"mark_key without markable is fine with activity", func(c *Config) { c.TUI.Components["pods"].Markable = false }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := activityConfig(tc.mutate).Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}
