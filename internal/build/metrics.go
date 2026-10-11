package build

import (
	"strconv"
	"strings"

	"github.com/jsdrews/tuilib/pkg/metrics"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// defaultMetricWidth sizes a bar or sparkline in a column (or inspector
// field) with no width of its own.
const defaultMetricWidth = 10

// renderMetric draws m from one item through tuilib's pkg/metrics.
// colWidth is the column's own width, the bar's or sparkline's default.
// The output is foreground-only ANSI, so a selected row's background
// stays intact.
func renderMetric(m *cfg.Metric, item any, colWidth int, th theme.Theme) string {
	width := m.Width
	if width == 0 {
		width = colWidth
	}
	if width <= 0 {
		width = defaultMetricWidth
	}
	color := colorIndexFromSpec(m.Color, th) // -1 when unset
	switch m.Kind() {
	case "badge":
		return metrics.Badge(intAt(item, m.Badge[0]), intAt(item, m.Badge[1]), intAt(item, m.Badge[2]))
	case "ratio":
		return metrics.Ratio(intAt(item, m.Ratio[0]), intAt(item, m.Ratio[1]))
	case "bar":
		value := numberAt(item, m.Bar)
		max, err := strconv.ParseFloat(strings.TrimSpace(m.Max), 64)
		if err != nil {
			max = numberAt(item, m.Max) // a path rather than a number
		}
		if color >= 0 {
			return metrics.BarStyled(value, max, width, color)
		}
		return metrics.Bar(value, max, width)
	case "spark":
		series := seriesAt(item, m.Spark)
		if color >= 0 {
			return metrics.SparkStyled(series, width, color)
		}
		return metrics.Spark(series, width)
	}
	return ""
}

// metricRaw is the logical value behind a metric cell: its first input,
// as text. It's what `${selection.<col>}` and `${cursor.<col>}` see.
func metricRaw(m *cfg.Metric, item any) string {
	return ds.String(item, m.FirstInput())
}

// hasMetrics reports whether any of c's columns draws a metric.
func hasMetrics(c *cfg.Component) bool {
	for _, col := range c.Columns {
		if col.Metric != nil {
			return true
		}
	}
	return false
}

// columnCell renders one column of one item: a metric, or the text at
// the column's path through its color rules.
func columnCell(col cfg.Column, item any, th theme.Theme) string {
	if col.Metric != nil {
		return renderMetric(col.Metric, item, col.Width, th)
	}
	return applyColorRules(ds.FirstString(item, col.Value), col.ColorRules, th)
}

// RawSelection puts the logical value back into a table selection's
// metric columns, which otherwise hold the drawn glyphs. The focused
// row's item comes from the table's keyed rows; a table with metric
// columns always keys its rows for this reason.
func RawSelection(c *Component, sel Selection) Selection {
	if c == nil || c.Kind != KTable || !hasMetrics(c.Cfg) {
		return sel
	}
	key := CursorKey(c)
	if key == "" {
		return sel
	}
	for _, r := range c.Table.KeyedRows() {
		if r.Key != key {
			continue
		}
		return withRawMetrics(sel, c.Cfg.Columns, r.Data)
	}
	return sel
}

// withRawMetrics swaps each metric column's cell for its raw value.
func withRawMetrics(sel Selection, cols []cfg.Column, item any) Selection {
	cells := append([]string(nil), sel.Cells...)
	for i, col := range cols {
		if col.Metric != nil && i < len(cells) {
			cells[i] = metricRaw(col.Metric, item)
		}
	}
	sel.Cells = cells
	if len(cells) > 0 {
		sel.String = cells[0]
	}
	return sel
}

func numberAt(item any, path string) float64 {
	return toFloat(ds.Get(item, path))
}

func intAt(item any, path string) int { return int(numberAt(item, path)) }

// seriesAt reads a list of numbers for a sparkline; non-numbers count
// as zero so a gap doesn't drop a point and shift the line.
func seriesAt(item any, path string) []float64 {
	items := ds.Iter(ds.Get(item, path))
	out := make([]float64, len(items))
	for i, v := range items {
		out[i] = toFloat(v)
	}
	return out
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	case bool:
		if x {
			return 1
		}
	}
	return 0
}
