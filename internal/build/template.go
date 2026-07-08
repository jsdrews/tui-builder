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

// SubstituteAll runs ${selection.*}/${env.*}/${prompt.*} substitution
// on every entry in argv and returns a new slice. Used by action
// dispatch — the run argv references the focused row's selection AND
// any prompt values entered moments before dispatch. Pass nil for
// prompts when there were none.
func SubstituteAll(argv []string, sel Selection, prompts map[string]string) []string {
	out := make([]string, len(argv))
	for i, s := range argv {
		out[i] = substituteAll(s, sel, prompts)
	}
	return out
}

// Substitute resolves ${selection.*} and ${env.*} tokens in s against
// the given Selection. Used by push-site `bind:` resolution to turn a
// template like `${selection.Namespace}` into the focused row's actual
// namespace value before that value is passed into a destination
// source's parameter binder.
func Substitute(s string, sel Selection) string {
	return substituteAll(s, sel, nil)
}

// SubstituteScreen returns deep-copied Screen + Components + Entries
// with every ${selection*} token replaced from sel. Original config is
// left untouched, so each push can re-substitute against a fresh
// selection. Leaf-kind entries go through the same substitution so a
// child's URL / headers / body can reference the parent row.
//
// When params is non-nil, every cloned leaf entry that declares
// `parameters:` ALSO has BindLeafParams called against the subset of
// params it actually declares. This is how the explicit push-site
// bind: block feeds into the destination screen's parameterized
// sources. Missing required params surface as an error so the caller
// (tryPush) can pop an alert instead of building a broken screen.
//
// Operator entries (filter, sort, …) are cloned without substitution
// — they don't carry templated string fields and the expression
// language is bind-time, not screen-time.
//
// Pass nil for params to skip parameter binding (single-screen New
// and initial multi-screen construction — no push site context).
// Parameterized sources in that path will be left with unresolved
// ${params.*} templates and will fail at fetch with a clear URL.
func SubstituteScreen(s *cfg.Screen, components map[string]*cfg.Component, sources map[string]*cfg.Source, sel Selection, params map[string]string) (*cfg.Screen, map[string]*cfg.Component, map[string]*cfg.Source, error) {
	out := *s
	out.Title = substitute(s.Title, sel)
	comps := map[string]*cfg.Component{}
	for name, c := range components {
		comps[name] = cloneComponent(c, sel)
	}
	// Determine which sources THIS screen actually references via
	// its layout's components. The Multi config keeps ALL screens'
	// sources in one map (so child screens can resolve them after a
	// push); applying param-binding indiscriminately would try to
	// satisfy required params on OTHER screens' sources and fail —
	// e.g. pushing to "pods" with bind {namespace: ...} would error
	// on `pod_detail` needing a `name` it can't see.
	used := usedSources(&s.Layout, components)
	out2 := map[string]*cfg.Source{}
	for name, src := range sources {
		cloned := cloneSource(src, sel)
		if used[name] && cloned != nil && cloned.IsLeaf() {
			if err := applyBindSourceParams(cloned, params); err != nil {
				return nil, nil, nil, fmt.Errorf("data.sources.%s: %w", name, err)
			}
		}
		out2[name] = cloned
	}
	return &out, comps, out2, nil
}

// cloneSource returns a deep copy of src with ${selection*} tokens
// substituted in the leaf source's templated fields. Operator
// sources are returned unchanged (they don't carry per-screen
// templates — the expression language is bind-time).
func cloneSource(src *cfg.Source, sel Selection) *cfg.Source {
	if src == nil {
		return nil
	}
	if !src.IsLeaf() {
		// Operator — alias the original; nothing per-screen to mutate.
		return src
	}
	cloned := src.Clone()
	cloned.SubstituteStrings(func(s string) string { return substitute(s, sel) })
	return cloned
}

// applyBindSourceParams filters the screen-wide params map down to
// the source's declared params before calling BindParams so unrelated
// keys don't trigger BindParams' "extra params" rejection.
func applyBindSourceParams(src *cfg.Source, params map[string]string) error {
	if src == nil || len(src.Parameters) == 0 || params == nil {
		return nil
	}
	subset := make(map[string]string, len(src.Parameters))
	for name := range src.Parameters {
		if v, ok := params[name]; ok {
			subset[name] = v
		}
	}
	return src.BindParams(subset)
}

// usedSources walks a screen's layout tree and returns the set of
// data-source names referenced by any component on the screen — both
// direct `source:` bindings and via pipelines (`pipeline:` chains
// resolved through their `from:` upstreams).
//
// Pipeline chains terminate at the first defined data source name
// they reference; if the chain is malformed the validator has already
// rejected the config, so we don't need to defend against that here.
func usedSources(n *cfg.Node, components map[string]*cfg.Component) map[string]bool {
	used := map[string]bool{}
	walkUsed(n, components, used)
	return used
}

func walkUsed(n *cfg.Node, components map[string]*cfg.Component, used map[string]bool) {
	if n == nil {
		return
	}
	if n.Component != "" {
		if c, ok := components[n.Component]; ok && c != nil {
			if c.Source != "" {
				used[c.Source] = true
			}
		}
	}
	for _, it := range n.VStack {
		walkUsed(&it.Node, components, used)
	}
	for _, it := range n.HStack {
		walkUsed(&it.Node, components, used)
	}
	if n.ZStack != nil {
		walkUsed(&n.ZStack.Base, components, used)
		walkUsed(&n.ZStack.Overlay, components, used)
	}
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
	out.RootLabel = substitute(c.RootLabel, sel)

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
			Label:      substitute(f.Label, sel),
			Value:      substitute(f.Value, sel),
			Path:       f.Path,
			ColorRules: f.ColorRules,
			Children:   cloneInspectorFields(f.Children, sel),
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

// tokenRe matches three token families:
//
//	${selection}            — parent row's primary string
//	${selection.SUFFIX}     — table cell by 1-based index or column-title prefix
//	${env.NAME}             — os.Getenv("NAME") (empty when unset)
//	${prompt.KEY}           — value collected from a form prompt at action-fire time
//
// The first capture group is the namespace; the second is the optional suffix.
var tokenRe = regexp.MustCompile(`\$\{(selection|env|prompt)(?:\.([A-Za-z0-9_]+))?\}`)

func substitute(s string, sel Selection) string {
	return substituteAll(s, sel, nil)
}

// substituteAll is the general form — sel for ${selection.*}, prompts
// for ${prompt.*}. Either map can be nil.
func substituteAll(s string, sel Selection, prompts map[string]string) string {
	if s == "" || !strings.Contains(s, "${") {
		return s
	}
	return tokenRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := tokenRe.FindStringSubmatch(match)
		return resolveToken(groups[1], groups[2], sel, prompts)
	})
}

func resolveToken(namespace, key string, sel Selection, prompts map[string]string) string {
	switch namespace {
	case "selection":
		return resolveSelection(key, sel)
	case "env":
		return os.Getenv(key)
	case "prompt":
		if key == "" {
			return ""
		}
		return prompts[key]
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
	// Exact (case-insensitive) match against column titles. Misses
	// pass through as the literal ${selection.X} so the unresolved
	// token is visible in URLs / argv — easier to spot than silent
	// substitution to "".
	//
	// Earlier versions also fell back to a case-insensitive prefix
	// match. That was a footgun (${selection.Name} silently matched a
	// "Namespace" column) and nothing in examples/ relied on it. Exact
	// is safer and the savings of the prefix sugar weren't worth it.
	for i, col := range sel.Columns {
		if strings.EqualFold(col, key) {
			if i < len(sel.Cells) {
				return sel.Cells[i]
			}
			return ""
		}
	}
	return fmt.Sprintf("${selection.%s}", key) // pass-through if unresolved
}
