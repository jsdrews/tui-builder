package build

import (
	"strings"

	"github.com/jsdrews/tuilib/pkg/inspector"
	"github.com/jsdrews/tuilib/pkg/table"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// ApplyData maps a data source response onto a bound component. Component
// shape and binding fields decide how the response is consumed:
//
//   - list      Cfg.Item       (dot-path to display string)
//   - table     Column.Value   (dot-path per column)
//   - inspector field.Path     (dot-path per field; static Value preserved when Path is empty)
//
// The iterable root is selected via Cfg.Source's root path (already
// applied in screen.Model before this is called) so data is the
// already-rooted value. All three updates happen in place; tuilib's
// SetItems/SetRows/SetFields preserve cursor + filter + InitialDepth
// pre-expansion across refreshes.
func ApplyData(c *Component, data any) {
	if c == nil {
		return
	}
	switch c.Kind {
	case KList:
		applyList(c, data)
	case KTable:
		applyTable(c, data)
	case KInspector:
		applyInspector(c, data)
	case KLogview:
		applyLogview(c, data)
	}
}

// applyLogview replaces the logview buffer with the data. Plain strings
// (from format:text sources) are split on \n; a []string from a JSON
// source is used as-is.
func applyLogview(c *Component, data any) {
	c.Logview.Clear()
	switch x := data.(type) {
	case string:
		c.Logview.AppendLines(strings.Split(strings.TrimRight(x, "\n"), "\n"))
	case []any:
		lines := make([]string, 0, len(x))
		for _, v := range x {
			if s, ok := v.(string); ok {
				lines = append(lines, s)
			}
		}
		c.Logview.AppendLines(lines)
	}
}

func applyList(c *Component, data any) {
	items := ds.Iter(data)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, ds.String(it, c.Cfg.Item))
	}
	c.List.SetItems(out)
}

func applyTable(c *Component, data any) {
	items := ds.Iter(data)
	rows := make([]table.Row, 0, len(items))
	for _, it := range items {
		cells := make([]string, len(c.Cfg.Columns))
		for i, col := range c.Cfg.Columns {
			cells[i] = ds.String(it, col.Value)
		}
		rows = append(rows, table.Row(cells))
	}
	c.Table.SetRows(rows)
}

func applyInspector(c *Component, data any) {
	c.Inspector.SetFields(buildInspectorFields(c.Cfg.Fields, data))
}

// buildInspectorFields walks the config field tree, plucking each field's
// Path from data when Path is set; otherwise it keeps the static Value.
func buildInspectorFields(in []cfg.InspectorField, data any) []inspector.Field {
	if len(in) == 0 {
		return nil
	}
	out := make([]inspector.Field, len(in))
	for i, f := range in {
		v := f.Value
		if f.Path != "" {
			v = ds.String(data, f.Path)
		}
		out[i] = inspector.Field{
			Label:    f.Label,
			Value:    v,
			Children: buildInspectorFields(f.Children, data),
		}
	}
	return out
}
