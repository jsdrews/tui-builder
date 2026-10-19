package config

import (
	"strings"
	"testing"
)

func anchoredConfig(mutate func(*Config)) *Config {
	return eventlogConfig(func(c *Config) {
		c.Data.Sources["events"] = &Source{
			Type: "http", Method: "POST", URL: "http://example/_search",
			Body: `{"size": ${window.limit}, "q": "${window.search}"}`,
			Window: &WindowConfig{
				Cursor: "sort",
				Older:  map[string]any{"sort": "desc", "search_after": "${window.cursor}"},
				Newer:  map[string]any{"sort": "asc", "search_after": "${window.cursor}"},
			},
		}
		if mutate != nil {
			mutate(c)
		}
	})
}

func TestAnchoredWindowValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"body api", nil, ""},
		{"growing", func(c *Config) { c.Data.Sources["events"].Window.Growing = &Growing{Always: true} }, ""},
		{"anchor oldest", func(c *Config) { c.Data.Sources["events"].Window.Anchor = "oldest" }, ""},
		{"anchor newest", func(c *Config) { c.Data.Sources["events"].Window.Anchor = "newest" }, ""},
		{"unknown anchor", func(c *Config) { c.Data.Sources["events"].Window.Anchor = "middle" }, "unknown anchor"},
		{"start on an anchored source", func(c *Config) { c.TUI.Components["events"].Start = "oldest" }, "set `window.anchor: newest|oldest`"},
		{"no newer patch", func(c *Config) { c.Data.Sources["events"].Window.Newer = nil }, "needs both `older:` and `newer:`"},
		{"patch without cursor", func(c *Config) { c.Data.Sources["events"].Window.Cursor = "" }, "only apply with `cursor:`"},
		{"offsets don't apply", func(c *Config) { c.Data.Sources["events"].Window.OffsetParam = "from" }, "offset_param: doesn't apply to an Anchored window"},
		{"total doesn't apply", func(c *Config) { c.Data.Sources["events"].Window.TotalPath = "hits.total" }, "total_path: doesn't apply"},
		{"no way to filter", func(c *Config) {
			c.Data.Sources["events"].Body = `{"size": ${window.limit}}`
			c.TUI.Components["events"].Filterable = true
		}, "gives the filter nowhere to go"},
		{"no filter bar, no destination needed", func(c *Config) { c.Data.Sources["events"].Body = `{"size": ${window.limit}}` }, ""},
		{"span table", func(c *Config) {
			c.TUI.Components["events"] = &Component{Type: "table", Source: "events", MarkKey: Path{"_id"},
				Columns: []Column{{Title: "T", Value: Path{"t"}}}}
		}, ""},
		{"span table needs mark_key", func(c *Config) {
			c.TUI.Components["events"] = &Component{Type: "table", Source: "events", Columns: []Column{{Title: "T", Value: Path{"t"}}}}
		}, "needs `mark_key:`"},
		{"span table is markable", func(c *Config) {
			c.TUI.Components["events"] = &Component{Type: "table", Source: "events", MarkKey: Path{"_id"}, Markable: true, MaxItems: 1000,
				Columns: []Column{{Title: "T", Value: Path{"t"}}}}
		}, ""},
		{"span table can't sort", func(c *Config) {
			c.TUI.Components["events"] = &Component{Type: "table", Source: "events", MarkKey: Path{"_id"},
				Columns: []Column{{Title: "T", Value: Path{"t"}, Sortable: true}}}
		}, "order it walks them"},
		{"span table has no initial sort", func(c *Config) {
			c.TUI.Components["events"] = &Component{Type: "table", Source: "events", MarkKey: Path{"_id"},
				InitialSort: &Sort{Column: "T"}, Columns: []Column{{Title: "T", Value: Path{"t"}}}}
		}, "`initial_sort:` doesn't apply"},
		{"max_items on an ordinary table", func(c *Config) {
			c.TUI.Components["plain"] = &Component{Type: "table", MaxItems: 10, Columns: []Column{{Title: "T"}}, Rows: [][]any{{"a"}}}
		}, "`max_items:` applies to an eventlog or a table over an Anchored source"},
		{"exec needs the cursor tokens", func(c *Config) {
			c.Data.Sources["events"] = &Source{Type: "exec", Command: []string{"logs", "--limit", "${window.limit}", "--q", "${window.search}"},
				Window: &WindowConfig{Cursor: "ts"}}
		}, "never references ${window.cursor}"},
		{"exec takes no patches", func(c *Config) {
			c.Data.Sources["events"] = &Source{Type: "exec",
				Command: []string{"logs", "${window.cursor}", "${window.dir}", "${window.limit}", "${window.search}"},
				Window:  &WindowConfig{Cursor: "ts", Older: map[string]any{"a": 1}}}
		}, "`older:` / `newer:` are http-only"},
		{"exec anchored", func(c *Config) {
			c.Data.Sources["events"] = &Source{Type: "exec",
				Command: []string{"logs", "${window.cursor}", "${window.dir}", "${window.limit}", "${window.search}"},
				Window:  &WindowConfig{Cursor: "ts"}}
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := anchoredConfig(tc.mutate).Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}
