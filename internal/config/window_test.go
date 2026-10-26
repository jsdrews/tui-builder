package config

import (
	"strings"
	"testing"
)

// windowedConfig is a minimal valid config: one windowed http source
// bound to one table. Tests mutate the returned value to isolate the
// rule they're checking.
func windowedConfig() *Config {
	return &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"books": NewEntry(&Source{
				Type: "http",
				URL:  "https://example.test/search",
				Root: "docs",
				Window: &WindowConfig{
					OffsetParam: "offset",
					LimitParam:  "limit",
					TotalPath:   "numFound",
					SearchParam: "q",
					Filters:     map[string]string{"Author": "author"},
				},
			}),
		}},
		TUI: TUIBlock{
			Components: map[string]*Component{
				"books": {
					Type:       "table",
					Source:     "books",
					Filterable: true,
					Columns: []Column{
						{Title: "Title", Width: 40, Value: Path{"title"}},
						{Title: "Author", Width: 20, Value: Path{"author_name.0"}},
					},
				},
			},
			Screen: Screen{Title: "Books", Layout: Node{Component: "books"}},
		},
	}
}

func TestWindowValidConfigMarksComponentWindowed(t *testing.T) {
	c := windowedConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !c.TUI.Components["books"].Windowed {
		t.Error("Windowed not set on a table bound to a source declaring window:")
	}
}

func TestWindowNotSetOnOrdinarySource(t *testing.T) {
	c := windowedConfig()
	c.Data.Sources["books"].Window = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.TUI.Components["books"].Windowed {
		t.Error("Windowed set on a table whose source declares no window:")
	}
}

func TestWindowRequiredParams(t *testing.T) {
	for name, mutate := range map[string]func(w *WindowConfig){
		"offset_param": func(w *WindowConfig) { w.OffsetParam = "" },
		"limit_param":  func(w *WindowConfig) { w.LimitParam = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := windowedConfig()
			mutate(c.Data.Sources["books"].Window)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate succeeded with %s unset", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not name the missing field %q", err, name)
			}
		})
	}
}

// A windowed table filters remotely, so it needs somewhere to send the
// filter — otherwise the filter bar silently does nothing.
func TestWindowNeedsSomewhereToSendAFilter(t *testing.T) {
	c := windowedConfig()
	c.Data.Sources["books"].Window.SearchParam = ""
	c.Data.Sources["books"].Window.Filters = nil
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate succeeded with neither search_param nor filters")
	}
	if !strings.Contains(err.Error(), "search_param") {
		t.Errorf("error %q should point at search_param", err)
	}

	// A table without a filter bar needs nowhere to send one: an API
	// with no server-side filter is a window bound to such a table.
	c.TUI.Components["books"].Filterable = false
	if err := c.Validate(); err != nil {
		t.Errorf("Validate failed for a table with no filter bar: %v", err)
	}
}

// Without a total the table never asks past its first window, so a
// seekable window must say where the total is.
func TestWindowRequiresTotal(t *testing.T) {
	for name, c := range map[string]*Config{"http": windowedConfig(), "exec": execWindowedConfig()} {
		c.Data.Sources["books"].Window.TotalPath = ""
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "window.total_path: required") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWindowRejectsNonTableBinding(t *testing.T) {
	c := windowedConfig()
	c.TUI.Components["books"].Type = "list"
	c.TUI.Components["books"].Columns = nil
	c.TUI.Components["books"].Item = "title"
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate succeeded binding a windowed source to a list")
	}
	if !strings.Contains(err.Error(), "only a table or an eventlog") {
		t.Errorf("error %q should say only tables and eventlogs can hold a window", err)
	}
}

func TestWindowRejectsSortableColumnWithoutSortParam(t *testing.T) {
	c := windowedConfig()
	c.TUI.Components["books"].Columns[0].Sortable = yes()
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate succeeded with a sortable column and no sort_param")
	}
	if !strings.Contains(err.Error(), "sort_param") {
		t.Errorf("error %q should point at window.sort_param", err)
	}

	// With sort_param declared, the same config is fine.
	c.Data.Sources["books"].Window.SortParam = "ordering"
	if err := c.Validate(); err != nil {
		t.Errorf("Validate failed once sort_param was declared: %v", err)
	}
}

// An operator over a windowed source would filter/sort/transform one
// page and present the result as if it had seen the whole set.
func TestWindowRejectsOperatorUpstream(t *testing.T) {
	c := windowedConfig()
	c.Data.Sources["recent"] = NewEntry(&Source{
		Type: "filter", From: "books", Where: `first_publish_year > 2000`,
	})
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate succeeded with a filter operator over a windowed source")
	}
	for _, want := range []string{"window:", "filter"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestWindowMutuallyExclusiveWithPaginateAndFollow(t *testing.T) {
	t.Run("paginate", func(t *testing.T) {
		c := windowedConfig()
		c.Data.Sources["books"].Paginate = &PaginateConfig{Strategy: "link", NextPath: "next"}
		if err := c.Validate(); err == nil {
			t.Fatal("Validate succeeded with both window: and paginate:")
		}
	})
	t.Run("follow", func(t *testing.T) {
		c := windowedConfig()
		c.Data.Sources["books"].Follow = true
		if err := c.Validate(); err == nil {
			t.Fatal("Validate succeeded with both window: and follow: true")
		}
	})
	t.Run("text format", func(t *testing.T) {
		c := windowedConfig()
		c.Data.Sources["books"].Format = "text"
		if err := c.Validate(); err == nil {
			t.Fatal("Validate succeeded with window: and format: text")
		}
	})
}

// `window:` on a kind that can't serve it must fail loudly. Silently
// ignoring it would leave the bound table in remote-filter mode with
// nothing answering, so the filter bar and sort keys would do nothing.
func TestWindowRejectedOnNonHTTPKinds(t *testing.T) {
	for kind, mutate := range map[string]func(s *Source){
		"file":        func(s *Source) { s.Path = "./fixture.json" },
		"static":      func(s *Source) { s.Data = []any{} },
		"websocket":   func(s *Source) { s.URL = "wss://example.test" },
		"passthrough": func(s *Source) { s.From = "other" },
	} {
		t.Run(kind, func(t *testing.T) {
			s := &Source{Type: kind, Window: &WindowConfig{
				OffsetParam: "offset", LimitParam: "limit", SearchParam: "q",
			}}
			mutate(s)
			err := NewEntry(s).Validate("data.sources.x")
			if err == nil {
				t.Fatalf("Validate accepted `window:` on a %s source", kind)
			}
			if !strings.Contains(err.Error(), "type: http") {
				t.Errorf("error %q should name the kinds that can window", err)
			}
		})
	}
}

// Clone must deep-copy the window's maps — a pushed screen clones every
// source, and an aliased map header would let one screen's substitution
// reach into another's.
func TestWindowCloneCopiesMaps(t *testing.T) {
	orig := &Source{
		Type: "http", URL: "https://example.test",
		Window: &WindowConfig{
			OffsetParam: "offset", LimitParam: "limit",
			Filters: map[string]string{"Author": "author"},
			Sorts:   map[string]string{"Year": "year"},
		},
	}
	clone := orig.Clone()
	clone.Window.Filters["Author"] = "mutated"
	clone.Window.Sorts["Year"] = "mutated"
	if orig.Window.Filters["Author"] != "author" {
		t.Error("mutating the clone's Filters reached the original")
	}
	if orig.Window.Sorts["Year"] != "year" {
		t.Error("mutating the clone's Sorts reached the original")
	}
}

// execWindowedConfig is the exec counterpart of windowedConfig.
func execWindowedConfig() *Config {
	c := windowedConfig()
	c.Data.Sources["books"] = NewEntry(&Source{
		Type: "exec",
		Command: []string{"sh", "-c",
			`q --offset=${window.offset} --limit=${window.limit} --grep=${window.search} --author=${window.filters.author}`},
		Root:   "rows",
		Window: &WindowConfig{TotalPath: "total", Filters: map[string]string{"Author": "author"}},
	})
	return c
}

func TestExecWindowValidConfig(t *testing.T) {
	c := execWindowedConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !c.TUI.Components["books"].Windowed {
		t.Error("Windowed not set on a table bound to a windowed exec source")
	}
}

// The query-string fields name HTTP parameters. Ignoring them on an exec
// source would leave paging quietly broken, so each is rejected by name.
func TestExecWindowRejectsHTTPOnlyFields(t *testing.T) {
	for field, mutate := range map[string]func(w *WindowConfig){
		"offset_param":     func(w *WindowConfig) { w.OffsetParam = "offset" },
		"limit_param":      func(w *WindowConfig) { w.LimitParam = "limit" },
		"search_param":     func(w *WindowConfig) { w.SearchParam = "q" },
		"sort_param":       func(w *WindowConfig) { w.SortParam = "ordering" },
		"sort_desc_prefix": func(w *WindowConfig) { w.SortDescPrefix = "-" },
	} {
		t.Run(field, func(t *testing.T) {
			c := execWindowedConfig()
			mutate(c.Data.Sources["books"].Window)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %s on an exec window", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error %q should name %q", err, field)
			}
		})
	}
}

// A command that never references the paging tokens returns the same
// rows for every window — the table would scroll through copies of
// page one, which looks like data rather than a bug.
func TestExecWindowRequiresPagingTokens(t *testing.T) {
	for _, missing := range []string{"${window.offset}", "${window.limit}"} {
		t.Run(missing, func(t *testing.T) {
			c := execWindowedConfig()
			cmd := c.Data.Sources["books"].Command
			cmd[2] = strings.ReplaceAll(cmd[2], missing, "0")
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate accepted a command with no %s", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error %q should name %q", err, missing)
			}
		})
	}
}

// An exec window sorts when its command carries the sort column and its
// direction; with only the column, descending would sort as ascending.
func TestExecWindowSortTokens(t *testing.T) {
	for _, tc := range []struct{ tokens, wantErr string }{
		{"", "never references ${window.sort}"},
		{" --sort=${window.sort}", "but not ${window.sort_dir}"},
		{" --sort=${window.sort} --dir=${window.sort_dir}", ""},
	} {
		c := execWindowedConfig()
		c.Data.Sources["books"].Command[2] += tc.tokens
		c.TUI.Components["books"].Columns[0].Sortable = yes()
		err := c.Validate()
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%q: err = %v, want %q", tc.tokens, err, tc.wantErr)
		}
	}
}

func TestExecWindowRequiresSomewhereToSendAFilter(t *testing.T) {
	c := execWindowedConfig()
	c.Data.Sources["books"].Command[2] = "q --offset=${window.offset} --limit=${window.limit}"
	c.Data.Sources["books"].Window.Filters = nil
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a command referencing no filter token")
	}
	if !strings.Contains(err.Error(), "${window.search}") {
		t.Errorf("error %q should point at the missing filter tokens", err)
	}
}

// A declared filter whose token the command never uses is dead config —
// the user would type "author:x" and watch nothing happen.
func TestExecWindowRejectsUnusedFilterToken(t *testing.T) {
	c := execWindowedConfig()
	c.Data.Sources["books"].Window.Filters["Title"] = "title"
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a filters: entry the command never references")
	}
	if !strings.Contains(err.Error(), "${window.filters.title}") {
		t.Errorf("error %q should name the unreferenced token", err)
	}
}

func TestExecWindowRejectsTokenInProgramName(t *testing.T) {
	c := execWindowedConfig()
	c.Data.Sources["books"].Command[0] = "${window.search}"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted ${window.*} in command[0]")
	}
}

func TestExecWindowMutuallyExclusiveWithFollow(t *testing.T) {
	c := execWindowedConfig()
	c.Data.Sources["books"].Follow = true
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted window: with follow: true on exec")
	}
}

// With only column filters, the placeholder shows the syntax that works.
func TestWindowFiltersOnlyDefaultsThePlaceholder(t *testing.T) {
	c := windowedConfig()
	c.Data.Sources["books"].Window.SearchParam = ""
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := c.TUI.Components["books"].FilterPlaceholder; got != "author:…" {
		t.Errorf("placeholder = %q", got)
	}
	c = windowedConfig()
	c.Data.Sources["books"].Window.SearchParam = ""
	c.TUI.Components["books"].FilterPlaceholder = "mine"
	if c.Validate(); c.TUI.Components["books"].FilterPlaceholder != "mine" {
		t.Error("explicit filter_placeholder was replaced")
	}
}

func TestWindowPageParam(t *testing.T) {
	one := 1
	neg := -1
	for _, tc := range []struct {
		name    string
		mutate  func(w *WindowConfig)
		wantErr string
	}{
		{"page instead of offset", func(w *WindowConfig) { w.OffsetParam, w.PageParam, w.PageStart = "", "page", &one }, ""},
		{"both", func(w *WindowConfig) { w.PageParam = "page" }, "offset_param and page_param are mutually exclusive"},
		{"neither", func(w *WindowConfig) { w.OffsetParam = "" }, "set offset_param (row offsets) or page_param (page numbers)"},
		{"page_start alone", func(w *WindowConfig) { w.PageStart = &one }, "page_start: only with page_param"},
		{"negative page_start", func(w *WindowConfig) { w.OffsetParam, w.PageParam, w.PageStart = "", "page", &neg }, "page_start: must be >= 0"},
	} {
		c := windowedConfig()
		tc.mutate(c.Data.Sources["books"].Window)
		err := c.Validate()
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
		}
	}
}

// yes is a *bool true, for Column.Sortable.
func yes() *bool { v := true; return &v }
