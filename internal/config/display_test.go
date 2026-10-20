package config

import (
	"strings"
	"testing"
)

func TestColorRulePathOnlyOnAutoInspector(t *testing.T) {
	rule := []ColorRule{{Path: "status", Color: "red"}}
	for _, tc := range []struct {
		name    string
		c       *Component
		wantErr string
	}{
		{"auto inspector", &Component{Type: "inspector", Auto: true, Source: "s", ColorRules: rule}, ""},
		{"list", &Component{Type: "list", ColorRules: rule}, "color_rules[0].path: only applies on an auto: true inspector"},
		{"column", &Component{Type: "table", Columns: []Column{{Title: "A", ColorRules: rule}}}, "columns[0].color_rules[0].path"},
		{"field", &Component{Type: "inspector", Fields: []InspectorField{{Label: "a", ColorRules: rule}}}, "fields[0].color_rules[0].path"},
	} {
		err := tc.c.validateRulePaths("c")
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestBoldNotOnMetric(t *testing.T) {
	col := &Column{Bold: true, Metric: &Metric{Ratio: []string{"a", "b"}}}
	if err := col.validateMetric("c", "s"); err == nil || !strings.Contains(err.Error(), "not with `bold: true`") {
		t.Errorf("err = %v", err)
	}
}
