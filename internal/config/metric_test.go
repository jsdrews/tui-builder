package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMetricDecodesANumberOrAPathForMax(t *testing.T) {
	var col Column
	if err := yaml.Unmarshal([]byte("metric: {bar: cpu, max: 100}"), &col); err != nil {
		t.Fatal(err)
	}
	if col.Metric.Max != "100" || col.Metric.Kind() != "bar" {
		t.Errorf("metric = %+v", col.Metric)
	}
}

func TestMetricValidation(t *testing.T) {
	cases := []struct {
		name string
		col  Column
		src  string
		want string
	}{
		{"ratio", Column{Title: "R", Metric: &Metric{Ratio: []string{"a", "b"}}}, "items", ""},
		{"bar", Column{Title: "B", Metric: &Metric{Bar: "cpu", Max: "100", Color: "blue", Width: 8}}, "items", ""},
		{"two kinds", Column{Title: "X", Metric: &Metric{Bar: "a", Spark: "b", Max: "1"}}, "items", "exactly one"},
		{"no kind", Column{Title: "X", Metric: &Metric{}}, "items", "exactly one"},
		{"short badge", Column{Title: "X", Metric: &Metric{Badge: []string{"a", "b"}}}, "items", "three paths"},
		{"long ratio", Column{Title: "X", Metric: &Metric{Ratio: []string{"a", "b", "c"}}}, "items", "two paths"},
		{"bar without max", Column{Title: "X", Metric: &Metric{Bar: "a"}}, "items", "needs `max:`"},
		{"max on spark", Column{Title: "X", Metric: &Metric{Spark: "a", Max: "1"}}, "items", "only applies to a bar"},
		{"color on ratio", Column{Title: "X", Metric: &Metric{Ratio: []string{"a", "b"}, Color: "red"}}, "items", "only apply to a bar or a sparkline"},
		{"with value", Column{Title: "X", Value: Path{"v"}, Metric: &Metric{Spark: "a"}}, "items", "not with `value:`"},
		{"with color rules", Column{Title: "X", ColorRules: []ColorRule{{Color: "red"}}, Metric: &Metric{Spark: "a"}}, "items", "not with `color_rules:`"},
		{"sortable", Column{Title: "X", Sortable: yes(), Metric: &Metric{Spark: "a"}}, "items", "not with `sortable: true`"},
		{"static table", Column{Title: "X", Metric: &Metric{Spark: "a"}}, "", "needs a table bound"},
	}
	for _, tc := range cases {
		c := promptConfig()
		tbl := &Component{Type: "table", Source: tc.src, Columns: []Column{tc.col}}
		if tc.src == "" {
			tbl.Rows = [][]any{{"x"}}
		}
		c.TUI.Components["items"] = tbl
		err := c.Validate()
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

func TestInspectorFieldMetricValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		field InspectorField
		want  string
	}{
		"ok":         {InspectorField{Label: "L", Metric: &Metric{Spark: "s"}}, ""},
		"with path":  {InspectorField{Label: "L", Path: "p", Metric: &Metric{Spark: "s"}}, "not with `value:` / `path:`"},
		"nested bad": {InspectorField{Label: "G", Children: []InspectorField{{Label: "L", Metric: &Metric{Bar: "b"}}}}, "children[0].metric"},
	} {
		c := promptConfig()
		c.TUI.Components["items"] = &Component{Type: "inspector", Source: "items", Fields: []InspectorField{tc.field}}
		err := c.Validate()
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", name, tc.want, err)
		}
	}
}
