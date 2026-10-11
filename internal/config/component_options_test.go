package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKindOptionsOnlyOnTheirKinds(t *testing.T) {
	yes := true
	cases := []struct {
		name string
		comp Component
		want string // "" = valid
	}{
		{"loading label on a list", Component{Type: "list", Items: []string{"a"}, LoadingLabel: "loading…"}, ""},
		{"loading label on a logview", Component{Type: "logview", LoadingLabel: "loading…"}, "`loading_label:` is only valid"},
		{"line numbers on a logview", Component{Type: "logview", LineNumbers: true}, ""},
		{"line numbers on a list", Component{Type: "list", Items: []string{"a"}, LineNumbers: true}, "`line_numbers:` is only valid"},
		{"gutter on a list", Component{Type: "list", Items: []string{"a"}, Gutter: &yes}, "`gutter:` is only valid"},
		{"new_for on a list", Component{Type: "list", Items: []string{"a"}, NewFor: "1s"}, "`new_for:` is only valid"},
	}
	for _, tc := range cases {
		err := tc.comp.validate("c")
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

func TestNewForValues(t *testing.T) {
	for v, ok := range map[string]bool{"2s": true, "-": true, "soon": false, "-1s": false} {
		c := Component{Type: "eventlog", NewFor: v}
		err := c.validateKindOptions("c")
		if (err == nil) != ok {
			t.Errorf("new_for %q: err = %v, want ok=%v", v, err, ok)
		}
	}
}

func TestDistinctAcceptsAListOrASource(t *testing.T) {
	var col Column
	if err := yaml.Unmarshal([]byte("distinct: [Running, Pending]"), &col); err != nil {
		t.Fatal(err)
	}
	if len(col.Distinct.Values) != 2 || col.Distinct.Source != "" {
		t.Errorf("list form = %+v", col.Distinct)
	}
	col = Column{}
	if err := yaml.Unmarshal([]byte("distinct: {source: phases}"), &col); err != nil {
		t.Fatal(err)
	}
	if col.Distinct.Source != "phases" || len(col.Distinct.Values) != 0 {
		t.Errorf("source form = %+v", col.Distinct)
	}
}

func TestDistinctValidation(t *testing.T) {
	phases := &Source{Type: "static", Data: []any{"Running"}}
	paged := &Source{Type: "http", URL: "http://x", Window: &WindowConfig{OffsetParam: "o", LimitParam: "l", SearchParam: "q"}}
	cases := []struct {
		name     string
		windowed bool
		distinct *Distinct
		extra    map[string]*Source
		want     string
	}{
		{"values on a windowed table", true, &Distinct{Values: []string{"a"}}, nil, ""},
		{"source on a windowed table", true, &Distinct{Source: "phases"}, map[string]*Source{"phases": phases}, ""},
		{"local table", false, &Distinct{Values: []string{"a"}}, nil, "only applies to a table bound to a windowed source"},
		{"both", true, &Distinct{Values: []string{"a"}, Source: "phases"}, map[string]*Source{"phases": phases}, "not both"},
		{"neither", true, &Distinct{}, nil, "needs values"},
		{"unknown source", true, &Distinct{Source: "nope"}, nil, "not defined"},
		{"windowed source", true, &Distinct{Source: "paged"}, map[string]*Source{"paged": paged}, "is windowed"},
	}
	for _, tc := range cases {
		c := windowedTableConfig("", tc.windowed)
		for k, v := range tc.extra {
			c.Data.Sources[k] = v
		}
		c.TUI.Components["books"].Columns[0].Distinct = tc.distinct
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
