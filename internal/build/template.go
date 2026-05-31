package build

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Selection is the data captured from a source component (list or table)
// when an on_enter binding fires. It seeds the ${selection} token in the
// pushed screen's config.
type Selection struct {
	// String is the primary representation: a list's selected item, or a
	// table's first cell. Drives bare ${selection}.
	String string
	// Cells are the full row of cells for a table source. Drives
	// ${selection.N} (1-based) and ${selection.COLNAME}.
	Cells []string
	// Columns are the parallel column titles for Cells. Used to resolve
	// ${selection.COLNAME} to the right cell.
	Columns []string
}

// SubstituteAll runs ${selection.*}/${env.*} substitution on every entry
// in argv and returns a new slice. Used by action dispatch: the run argv
// references the focused row's selection at fire time (not push time).
func SubstituteAll(argv []string, sel Selection) []string {
	out := make([]string, len(argv))
	for i, s := range argv {
		out[i] = substitute(s, sel)
	}
	return out
}

// SubstituteScreen returns deep-copied Screen + Components + DataSources
// with every ${selection*} token replaced from sel. Original config is
// left untouched, so each push can re-substitute against a fresh
// selection. DataSources go through the same substitution so a child's
// URL / headers / body can reference the parent row.
func SubstituteScreen(s *cfg.Screen, components map[string]*cfg.Component, dataSources map[string]*cfg.DataSource, sel Selection) (*cfg.Screen, map[string]*cfg.Component, map[string]*cfg.DataSource) {
	out := *s
	out.Title = substitute(s.Title, sel)
	comps := map[string]*cfg.Component{}
	for name, c := range components {
		comps[name] = cloneComponent(c, sel)
	}
	sources := map[string]*cfg.DataSource{}
	for name, d := range dataSources {
		sources[name] = cloneDataSource(d, sel)
	}
	return &out, comps, sources
}

func cloneDataSource(d *cfg.DataSource, sel Selection) *cfg.DataSource {
	if d == nil {
		return nil
	}
	out := *d
	out.URL = substitute(d.URL, sel)
	out.Body = substitute(d.Body, sel)
	if len(d.Headers) > 0 {
		out.Headers = make(map[string]string, len(d.Headers))
		for k, v := range d.Headers {
			out.Headers[k] = substitute(v, sel)
		}
	}
	return &out
}

func cloneComponent(c *cfg.Component, sel Selection) *cfg.Component {
	if c == nil {
		return nil
	}
	out := *c
	out.Title = substitute(c.Title, sel)
	out.FilterPlaceholder = substitute(c.FilterPlaceholder, sel)
	out.InitialFilter = substitute(c.InitialFilter, sel)
	out.InitialQuery = substitute(c.InitialQuery, sel)

	if c.Items != nil {
		out.Items = make([]string, len(c.Items))
		for i, v := range c.Items {
			out.Items[i] = substitute(v, sel)
		}
	}
	if c.Lines != nil {
		out.Lines = make([]string, len(c.Lines))
		for i, v := range c.Lines {
			out.Lines[i] = substitute(v, sel)
		}
	}
	if c.Columns != nil {
		out.Columns = make([]cfg.Column, len(c.Columns))
		for i, col := range c.Columns {
			col.Title = substitute(col.Title, sel)
			out.Columns[i] = col
		}
	}
	if c.Rows != nil {
		out.Rows = make([][]any, len(c.Rows))
		for i, row := range c.Rows {
			cells := make([]any, len(row))
			for j, cell := range row {
				cells[j] = substituteCell(cell, sel)
			}
			out.Rows[i] = cells
		}
	}
	if c.Root != nil {
		out.Root = cloneTreeNode(c.Root, sel)
	}
	if c.Fields != nil {
		out.Fields = cloneInspectorFields(c.Fields, sel)
	}
	return &out
}

func cloneTreeNode(n *cfg.TreeNode, sel Selection) *cfg.TreeNode {
	if n == nil {
		return nil
	}
	out := &cfg.TreeNode{Label: substitute(n.Label, sel)}
	if len(n.Children) > 0 {
		out.Children = make([]*cfg.TreeNode, len(n.Children))
		for i, ch := range n.Children {
			out.Children[i] = cloneTreeNode(ch, sel)
		}
	}
	return out
}

func cloneInspectorFields(in []cfg.InspectorField, sel Selection) []cfg.InspectorField {
	if len(in) == 0 {
		return nil
	}
	out := make([]cfg.InspectorField, len(in))
	for i, f := range in {
		out[i] = cfg.InspectorField{
			Label:    substitute(f.Label, sel),
			Value:    substitute(f.Value, sel),
			Path:     f.Path,
			Children: cloneInspectorFields(f.Children, sel),
		}
	}
	return out
}

// substituteCell handles styled-cell mappings (value/color, label/url)
// alongside bare-string cells.
func substituteCell(v any, sel Selection) any {
	switch x := v.(type) {
	case string:
		return substitute(x, sel)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			if s, ok := vv.(string); ok {
				out[k] = substitute(s, sel)
			} else {
				out[k] = vv
			}
		}
		return out
	}
	return v
}

// tokenRe matches two token families:
//
//	${selection}            — parent row's primary string
//	${selection.SUFFIX}     — table cell by 1-based index or column-title prefix
//	${env.NAME}             — os.Getenv("NAME") (empty when unset)
//
// The first capture group is the namespace ("selection" or "env"); the
// second is the optional suffix.
var tokenRe = regexp.MustCompile(`\$\{(selection|env)(?:\.([A-Za-z0-9_]+))?\}`)

func substitute(s string, sel Selection) string {
	if s == "" || !strings.Contains(s, "${") {
		return s
	}
	return tokenRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := tokenRe.FindStringSubmatch(match)
		return resolveToken(groups[1], groups[2], sel)
	})
}

func resolveToken(namespace, key string, sel Selection) string {
	switch namespace {
	case "selection":
		return resolveSelection(key, sel)
	case "env":
		return os.Getenv(key)
	}
	return ""
}

func resolveSelection(key string, sel Selection) string {
	if key == "" {
		return sel.String
	}
	// Numeric: 1-based index into Cells.
	if n, err := strconv.Atoi(key); err == nil {
		idx := n - 1
		if idx >= 0 && idx < len(sel.Cells) {
			return sel.Cells[idx]
		}
		return ""
	}
	// Name: case-insensitive prefix match against column titles.
	lk := strings.ToLower(key)
	for i, col := range sel.Columns {
		if strings.HasPrefix(strings.ToLower(col), lk) {
			if i < len(sel.Cells) {
				return sel.Cells[i]
			}
			return ""
		}
	}
	return fmt.Sprintf("${selection.%s}", key) // pass-through if unresolved
}
