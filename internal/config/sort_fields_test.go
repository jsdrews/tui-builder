package config

import (
	"strings"
	"testing"
)

// sortFieldsConfig is windowedConfig with a sort_param and the given
// sort_fields, plus a third column with a plain key.
func sortFieldsConfig(fields ...string) *Config {
	c := windowedConfig()
	w := c.Data.Sources["books"].Window
	w.SortParam = "order_by"
	w.SortFields = fields
	tbl := c.TUI.Components["books"]
	tbl.Columns = append(tbl.Columns, Column{Title: "Year", Value: Path{"first_publish_year"}})
	return c
}

// A listed field makes its column sortable without `sortable: true`,
// and sends that field rather than the title.
func TestSortFieldsMakeMatchedColumnsSortable(t *testing.T) {
	c := sortFieldsConfig("title", "first_publish_year")
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cols := c.TUI.Components["books"].Columns
	for i, want := range []bool{true, false, true} {
		if got := cols[i].IsSortable(); got != want {
			t.Errorf("columns[%d] (%s) sortable = %v, want %v", i, cols[i].Title, got, want)
		}
	}
	if got := c.Data.Sources["books"].Window.Sorts["Year"]; got != "first_publish_year" {
		t.Errorf("Year should send its listed field, got %q", got)
	}
}

// A `sorts:` entry is the column's field when it has one, so a nested
// value can still match.
func TestSortFieldsMatchThroughSorts(t *testing.T) {
	c := sortFieldsConfig("author")
	c.Data.Sources["books"].Window.Sorts = map[string]string{"Author": "author"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !c.TUI.Components["books"].Columns[1].IsSortable() {
		t.Error("Author maps to a listed field through sorts: but isn't sortable")
	}
}

// An explicit `sortable: false` opts a listed column out.
func TestSortFieldsExplicitFalseOptsOut(t *testing.T) {
	c := sortFieldsConfig("title")
	off := false
	c.TUI.Components["books"].Columns[0].Sortable = &off
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.TUI.Components["books"].Columns[0].IsSortable() {
		t.Error("sortable: false was overridden by sort_fields")
	}
}

// The source has the last word: `sortable: true` on a column the list
// doesn't match is a sort the API won't answer.
func TestSortFieldsRejectUnlistedSortable(t *testing.T) {
	c := sortFieldsConfig("title")
	c.TUI.Components["books"].Columns[2].Sortable = yes()
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), `columns[2] ("Year") is sortable, but the source's window.sort_fields doesn't list its field`) {
		t.Fatalf("want an unlisted-sortable error, got %v", err)
	}
}

func TestSortFieldsValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"no sort_param", func(c *Config) { c.Data.Sources["books"].Window.SortParam = "" }, "sort_fields: set without `sort_param:`"},
		{"empty field", func(c *Config) { c.Data.Sources["books"].Window.SortFields = []string{"title", " "} }, "sort_fields[1]: empty sort field name"},
		{"anchored", func(c *Config) {
			w := c.Data.Sources["books"].Window
			*w = WindowConfig{Cursor: "ts", LimitParam: "limit", SortFields: []string{"title"}}
		}, "sort_fields: doesn't apply to an Anchored window"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sortFieldsConfig("title")
			tc.mutate(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// On exec, sort_fields only decides which columns sort; the command
// still gets the title as ${window.sort}.
func TestSortFieldsOnExec(t *testing.T) {
	c := sortFieldsConfig("title")
	c.Data.Sources["books"] = NewEntry(&Source{
		Type:    "exec",
		Command: []string{"sh", "-c", "list ${window.offset} ${window.limit} ${window.search} ${window.sort} ${window.sort_dir} ${window.filters.author}"},
		Window:  &WindowConfig{TotalPath: "total", Filters: map[string]string{"Author": "author"}, SortFields: []string{"title"}},
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !c.TUI.Components["books"].Columns[0].IsSortable() {
		t.Error("Title is listed but not sortable on exec")
	}
	if len(c.Data.Sources["books"].Window.Sorts) != 0 {
		t.Error("exec windows have no sorts: map; nothing should be added")
	}

	c.Data.Sources["books"].Window.SortFields = []string{"title"}
	c.Data.Sources["books"].Command = []string{"sh", "-c", "list ${window.offset} ${window.limit} ${window.search} ${window.filters.author}"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "never references ${window.sort}") {
		t.Fatalf("want a missing ${window.sort} error, got %v", err)
	}
}
