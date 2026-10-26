package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFilterRule loads one windowed http source (its window: body given)
// bound to a table with the given columns.
func loadFilterRule(t *testing.T, window, columns string) (*Config, error) {
	t.Helper()
	src := `
data:
  sources:
    things:
      type: http
      url: https://api.example.com/things
      root: entries
      window:
        page_param: page
        limit_param: per_page
        total_path: total
` + window + `
tui:
  components:
    tbl:
      type: table
      source: things
      filterable: true
      columns:
` + columns + `
  screen: {layout: {component: tbl}}
`
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

const ruleColumns = `
        - {title: Name, value: name}
        - {title: Year, value: year}
        - {title: Owner, value: owner.login}
        - {title: Id, value: id}
        - {title: Secret, value: secret, hidden: true}
        - {title: State, value: [state, status]}`

func TestFilterRuleExpands(t *testing.T) {
	c, err := loadFilterRule(t, `
        filter_ops: {contains: "${column.value}__icontains", gte: "${column.value}__gte"}
        filter_except: [Id]
        filters:
          Name: q_name
          Year: {gte: year_from, lte: year_to}`, ruleColumns+`
        - {title: Kind, value: kind}`)
	if err != nil {
		t.Fatal(err)
	}
	w := c.Data.Sources["things"].Window
	want := map[string]map[string]string{
		"Name": {"contains": "q_name"},                 // string entry: contains only
		"Year": {"gte": "year_from", "lte": "year_to"}, // map entry replaces the rule
	}
	for title, ops := range want {
		for op, param := range ops {
			if got := w.FilterParams[title][op]; got != param {
				t.Errorf("%s %s → %q, want %q", title, op, got, param)
			}
		}
		if len(w.FilterParams[title]) != len(ops) {
			t.Errorf("%s: params = %v, want exactly %v", title, w.FilterParams[title], ops)
		}
	}
	if got := w.FilterParams["Kind"]; got["contains"] != "kind__icontains" || got["gte"] != "kind__gte" {
		t.Errorf("Kind should be covered by the rule, got %v", got)
	}
	for _, skipped := range []string{"Owner", "Id", "Secret", "State"} {
		if _, ok := w.FilterParams[skipped]; ok {
			t.Errorf("%s should be outside the rule, got %v", skipped, w.FilterParams[skipped])
		}
	}
	if w.Filters["Name"] != "q_name" {
		t.Errorf("Filters = %v", w.Filters)
	}
}

func TestFilterRuleCoversPlainColumns(t *testing.T) {
	c, err := loadFilterRule(t, `
        filter_ops: {contains: "${column.value}__icontains", eq: "${column.value}"}`, ruleColumns)
	if err != nil {
		t.Fatal(err)
	}
	w := c.Data.Sources["things"].Window
	if got := w.FilterParams["Year"]; got["contains"] != "year__icontains" || got["eq"] != "year" {
		t.Errorf("Year = %v", got)
	}
	if w.Filters["Name"] != "name__icontains" {
		t.Errorf("a rule's contains should land in Filters, got %v", w.Filters)
	}
	if !strings.Contains(c.TUI.Components["tbl"].FilterPlaceholder, "year:…") {
		t.Errorf("placeholder = %q", c.TUI.Components["tbl"].FilterPlaceholder)
	}
}

// With only comparisons in the rule, a filterable table still has
// somewhere to send a scoped term.
func TestFilterRuleWithoutContainsIsFilterable(t *testing.T) {
	if _, err := loadFilterRule(t, `
        filter_ops: {gte: "${column.value}__gte"}`, ruleColumns); err != nil {
		t.Fatal(err)
	}
}

func TestFilterRuleErrors(t *testing.T) {
	for _, tc := range []struct{ name, window, columns, want string }{
		{"no token", `
        filter_ops: {contains: "q"}`, ruleColumns, "filter_ops.contains: \"q\" doesn't use ${column.value}"},
		{"unknown op", `
        filter_ops: {like: "${column.value}__like"}`, ruleColumns, "filter_ops.like: unknown comparison"},
		{"unknown map op", `
        filters: {Year: {after: y}}`, ruleColumns, "filters.Year.after: unknown comparison"},
		{"except without rule", `
        search_param: q
        filter_except: [Id]`, ruleColumns, "filter_except: set without `filter_ops:`"},
		{"except unknown", `
        filter_ops: {contains: "${column.value}__icontains"}
        filter_except: [Nope]`, ruleColumns, `filter_except: no bound table has a column titled "Nope"`},
		{"covers nothing", `
        filter_ops: {contains: "${column.value}__icontains"}`, `
        - {title: Owner, value: owner.login}`, "filter_ops: covers no column"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadFilterRule(t, tc.window, tc.columns)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// Two tables on one source can't send the same title to two fields.
func TestFilterRuleTablesDisagree(t *testing.T) {
	c := windowedConfig()
	w := c.Data.Sources["books"].Window
	w.Filters = nil
	w.FilterOps = map[string]string{"contains": "${column.value}__icontains"}
	c.TUI.Components["books"].Columns = []Column{{Title: "Title", Value: Path{"title"}}}
	c.TUI.Components["other"] = &Component{Type: "table", Source: "books", Columns: []Column{{Title: "Title", Value: Path{"name"}}}}
	c.TUI.Screen.Layout = Node{VStack: []Item{{Node: Node{Component: "books"}}, {Node: Node{Component: "other"}}}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "the filter rule can't send both") {
		t.Fatalf("want a disagreement error, got %v", err)
	}
}

func TestFilterRuleExecRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		w    *WindowConfig
		want string
	}{
		{"filter_ops", &WindowConfig{TotalPath: "t", FilterOps: map[string]string{"contains": "${column.value}"}}, "filter_ops: http-only"},
		{"map entry", &WindowConfig{TotalPath: "t", FilterColumnOps: map[string]map[string]string{"Year": {"gte": "y"}}}, "filters.Year: a map of comparisons is http-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Source{Type: "exec", Command: []string{"sh", "-c", "x ${window.offset} ${window.limit}"}, Window: tc.w}
			err := s.Validate("data.sources.x")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
