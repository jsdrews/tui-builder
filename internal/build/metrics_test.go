package build

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/metrics"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

var metricRow = map[string]any{
	"name": "api", "ready": 1.0, "desired": "3", "ok": 7.0, "warn": 3.0, "down": 1.0,
	"cpu": 87.0, "limit": 200.0, "latency": []any{30.0, 34.0, "41", 55.0},
}

func TestRenderMetricMatchesTuilib(t *testing.T) {
	th := theme.Nord()
	cases := []struct {
		name string
		m    cfg.Metric
		want string
	}{
		{"badge", cfg.Metric{Badge: []string{"ok", "warn", "down"}}, metrics.Badge(7, 3, 1)},
		// "3" is a string in the data: inputs read numbers either way.
		{"ratio", cfg.Metric{Ratio: []string{"ready", "desired"}}, metrics.Ratio(1, 3)},
		{"bar", cfg.Metric{Bar: "cpu", Max: "100"}, metrics.Bar(87, 100, 12)},
		{"bar, max from a path", cfg.Metric{Bar: "cpu", Max: "limit"}, metrics.Bar(87, 200, 12)},
		{"bar, own width and color", cfg.Metric{Bar: "cpu", Max: "100", Width: 5, Color: "4"}, metrics.BarStyled(87, 100, 5, 4)},
		{"spark", cfg.Metric{Spark: "latency"}, metrics.Spark([]float64{30, 34, 41, 55}, 12)},
	}
	for _, tc := range cases {
		if got := renderMetric(&tc.m, metricRow, 12, th); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// No column width: the default.
	if got, want := renderMetric(&cfg.Metric{Spark: "latency"}, metricRow, 0, th), metrics.Spark([]float64{30, 34, 41, 55}, defaultMetricWidth); got != want {
		t.Errorf("default width: got %q, want %q", got, want)
	}
}

// A metric column draws glyphs, but anything reading the row as a
// selection gets its first input.
func TestMetricColumnSelectionIsTheRawValue(t *testing.T) {
	th := theme.Nord()
	c, err := NewComponent(&cfg.Component{Type: "table", Source: "deploys", Columns: []cfg.Column{
		{Title: "Name", Value: cfg.Path{"name"}},
		{Title: "Ready", Metric: &cfg.Metric{Ratio: []string{"ready", "desired"}}},
	}}, th)
	if err != nil {
		t.Fatal(err)
	}
	ApplyData(c, []any{metricRow}, th)

	if got := c.Table.Rows()[0][1]; got != metrics.Ratio(1, 3) {
		t.Errorf("cell = %q, want the drawn ratio", got)
	}
	if keys := c.Table.KeyedRows(); len(keys) != 1 || keys[0].Key != "0" {
		t.Fatalf("a metric table should key its rows by position, got %+v", keys)
	}
	sel := RawSelection(c, Selection{String: "api", Cells: []string{"api", "1/3"}, Columns: []string{"Name", "Ready"}})
	if sel.Cells[1] != "1" {
		t.Errorf("selection Ready = %q, want the raw ready count 1", sel.Cells[1])
	}
	if by := c.RowsByKey["0"]; by.Cells[1] != "1" {
		t.Errorf("RowsByKey Ready = %q, want 1", by.Cells[1])
	}
}

func TestInspectorFieldMetric(t *testing.T) {
	th := theme.Nord()
	fields := buildInspectorFields([]cfg.InspectorField{
		{Label: "Pods", Metric: &cfg.Metric{Badge: []string{"ok", "warn", "down"}}},
	}, metricRow, th)
	if fields[0].Value != metrics.Badge(7, 3, 1) {
		t.Errorf("field value = %q", fields[0].Value)
	}
}
