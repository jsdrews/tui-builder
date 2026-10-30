package gen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = "../fixture/things/things.yaml"

// withOverrides generates the fixture with an overrides file holding
// text.
func withOverrides(t *testing.T, text string, opts ...func(*Options)) (Result, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "o.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{Input: Input{Spec: fixture}, Overrides: path}
	for _, f := range opts {
		f(&o)
	}
	return Generate(context.Background(), o)
}

func TestOverrideRefusals(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"gen: {nmae: x}", `has no "nmae" (did you mean name?)`},
		{"gen: {operations: {\"GET /things\": {titel: X}}}", `has no "titel" (did you mean title?)`},
		{"tuii: {}", `isn't gen: or a config key (app, data, actions, tui) (did you mean tui?)`},
		{"gen: {operations: {\"GET /thing\": {title: X}}}", `no such operation (did you mean GET /things?)`},
		{"gen: {operations: {\"GET /things\": {skip: true, title: X}}}", "skip leaves nothing"},
		{"gen: {operations: {\"GET /things\": {columns: [name, colour]}}}", "the rows have no field colour"},
		{"gen: {operations: {\"GET /things\": {max_pages: 5}}}", "doesn't walk its pages"},
		{"gen: {operations: {\"GET /things\": {sort_refuse: [owner_id]}}}", "isn't one of the sort fields (name, created_at, size, status)"},
		{"gen: {operations: {\"GET /things/{thing_id}\": {columns: [name]}}}", "only a list takes it, and this is a detail"},
		{"gen: {operations: {\"GET /owners/{owner_id}/things\": {hide: true}}}", "reached by a push"},
		{"gen: {operations: {\"POST /things\": {title: X}}}", "a write takes only confirm"},
		{"gen: {operations: {\"GET /things\": {confirm: true}}}", "only a write takes it"},
		{"gen: {operations: {\"GET /things\": {root: nope}}}", "no list named nope"},
		{"gen: {order: [\"GET /nope\"]}", "gen.order: the spec has no GET /nope"},
		{"gen: {security: Nope}", `no alternative uses "Nope"`},
		{"tui: {components: {things: {columns: [{title: Nope, $drop: true}]}}}", `$drop matches no item with title "Nope"`},
		{"tui: {components: {things: {columns: [{value: x}]}}}", "each item needs its title:"},
		{"gen: {operations: {\"GET /things\": {title: Stuff}}}\ntui: {components: {things: {title: Other}}}", "tui.components.things.title is set by a gen: hint and by the config too"},
		{"tui: {components: {things: {type: nope}}}", "the config with the overrides from"},
	} {
		if _, err := withOverrides(t, tc.text); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s\n  err %v\n want %q", tc.text, err, tc.want)
		}
	}
}

// A hint for an operation gone from the spec is an error, or with
// --prune-overrides a warning.
func TestPruneOverrides(t *testing.T) {
	text := "gen: {operations: {\"GET /gone\": {title: X}}}"
	if _, err := withOverrides(t, text); err == nil || !strings.Contains(err.Error(), "--prune-overrides") {
		t.Errorf("no prune hint: %v", err)
	}
	res, err := withOverrides(t, text, func(o *Options) { o.PruneOverrides = true })
	if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "GET /gone") {
		t.Errorf("pruned: %v %q", err, res.Warnings)
	}
}

func TestOverrideHints(t *testing.T) {
	res, err := withOverrides(t, `
gen:
  name: stuff
  layout: menu
  order: ["GET /owners", "GET /things"]
  operations:
    "GET /things": {title: Inventory, group: stock, columns: [name, size]}
    "GET /owners/{owner_id}/things": {title: "Owned"}
    "POST /things": {confirm: "Really create one?"}
    "PATCH /things/{thing_id}": {skip: true}
`)
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Config)
	for _, want := range []string{
		"stuff:\n      type: http",             // gen.name
		"type: menu",                           // gen.layout
		"label: Owners, screen: owners",        // listed before things
		"group: stock, label: Inventory",       // title and group
		"- {title: Name, value: name, bold: true, flex: 1}\n        - {title: Size, value: size, align: right}\n        - {title: Id, value: id, hidden: true}\n        # More fields from the schema; uncomment one to add it:\n        # - {title: Status", // columns, the bound id hidden, the rest by rank
		"title: Owned",
		"confirm: 'Really create one?'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config lacks %q", want)
		}
	}
	if strings.Index(out, "screen: owners") > strings.Index(out, "screen: things") {
		t.Error("gen.order didn't put owners first")
	}
	if strings.Contains(out, "update_thing") || !strings.Contains(strings.Join(res.Skipped, "\n"), "PATCH /things/{thing_id}: skip: in the overrides") {
		t.Errorf("skip: %q", res.Skipped)
	}
}

// A flag wins over the file's global key.
func TestFlagsBeatOverrides(t *testing.T) {
	res, err := withOverrides(t, "gen: {name: from_file, layout: menu}", func(o *Options) { o.Doc.Name = "from_flag" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Config), "from_flag:\n      type: http") || !strings.Contains(string(res.Config), "type: menu") {
		t.Errorf("--name should win and gen.layout apply:\n%s", res.Config[:600])
	}
}

// hide keeps a screen but takes it off the tabs.
func TestHideHint(t *testing.T) {
	res, err := withOverrides(t, "gen: {operations: {\"GET /owners\": {hide: true}}}")
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Config)
	if strings.Contains(out, "label: Owners") || !strings.Contains(out, "\n    owners:\n      title: Owners") {
		t.Errorf("hide:\n%s", out[strings.Index(out, "  screens:"):])
	}
}

// Hints on an operation nothing was generated from are a warning.
func TestUnusedHintWarns(t *testing.T) {
	res, err := withOverrides(t, "gen: {operations: {\"GET /things\": {title: X}}}", func(o *Options) { o.Select.Tags = []string{"owners"} })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "GET /things: nothing was generated from it") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// The starter loads as an overrides file untouched, and names every
// generated operation.
func TestStarter(t *testing.T) {
	res, err := Generate(context.Background(), Options{Input: Input{Spec: fixture}, InitOverrides: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`# "GET /things":`, `# "DELETE /things/{thing_id}":   # delete_thing`, "#   columns: [name, status"} {
		if !strings.Contains(string(res.Starter), want) {
			t.Errorf("starter lacks %q:\n%s", want, res.Starter)
		}
	}
	if _, err := withOverrides(t, string(res.Starter)); err != nil {
		t.Errorf("the starter as written doesn't load: %v", err)
	}
}

// Keyed lists merge by identity; other lists replace; null deletes.
func TestMergeRules(t *testing.T) {
	res, err := withOverrides(t, `
app:
  title: Mine
  version: null
  env:
    - {name: EXTRA, required: true}
tui:
  screens:
    home:
      tabs:
        - {label: Owners, $drop: true}
`)
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Config)
	if !strings.Contains(out, "title: Mine") || strings.Contains(out, "version: 1.0.0") || !strings.Contains(out, "name: EXTRA") || !strings.Contains(out, "name: THINGS_API_TOKEN") {
		t.Errorf("app merge:\n%s", out[:900])
	}
	if strings.Contains(out, "label: Owners") || !strings.Contains(out, "label: Things") {
		t.Error("the tab $drop")
	}
}
