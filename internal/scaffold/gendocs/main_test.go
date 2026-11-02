package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generated file matches the docs; run `go generate
// ./internal/scaffold` after editing a reference table.
func TestGeneratedFileIsCurrent(t *testing.T) {
	want, err := render(filepath.Join("..", "..", "..", "docs", "reference"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "fields_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("internal/scaffold/fields_gen.go is stale: run `go generate ./internal/scaffold`")
	}
}

func TestParse(t *testing.T) {
	page := strings.Join([]string{
		"# Page",
		"## `table`",
		header,
		"|---|---|---|---|---|",
		"| `columns` | list | — | required | The `a \\| b` and `c | d` columns. |",
		"",
		"```",
		"## not a heading",
		"```",
		"| Locator | Means |",
		"|---|---|",
		"| `meta.total` | x |",
		"### Tabs",
		header,
		"|---|---|---|---|---|",
		"| `label` | string | — | — | One. |",
		"### Tabs",
		header,
		"|---|---|---|---|---|",
		"| `screen` | string | — | — | Two. |",
	}, "\n")
	got, err := parse("p.md", page)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].key != "p.md#table" || got[1].key != "p.md#tabs" || got[2].key != "p.md#tabs-1" {
		t.Fatalf("sections %+v", got)
	}
	if r := got[0].rows[0]; r.name != "columns" || r.desc != "The `a | b` and `c | d` columns." || r.validOn != "required" {
		t.Errorf("row %+v", r)
	}
	if _, err := parse("p.md", "## x\n"+header+"\n|---|\n| `a` | b |\n"); err == nil {
		t.Error("a short row parsed")
	}
	if _, err := parse("p.md", "## x\n"+header+"\n|---|\n| a.b | t | — | — | d |\n"); err == nil {
		t.Error("a non-key Field cell parsed")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"`app.glyphs`":                    "appglyphs",
		"Fields shared by leaf sources":   "fields-shared-by-leaf-sources",
		"Values from a file or a command": "values-from-a-file-or-a-command",
		"`on_cursor`":                     "on_cursor",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
