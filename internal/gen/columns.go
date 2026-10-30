package gen

import (
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/columns"
)

// field is a row property a column can show, with what the schema says
// about it.
type field struct {
	path   string
	schema *openapi3.Schema
	list   bool // a list of scalars, shown comma-joined
}

// schemaFields are a row's columns: scalars, nested objects flattened
// two levels (labels.team), and lists of scalars, in the order the schema
// declares them. Left out: lists of objects, maps, alternatives of
// different shapes, and writeOnly fields.
func schemaFields(item *openapi3.Schema) []field {
	if item == nil {
		return nil
	}
	if !isObject(item) {
		return []field{{path: ".", schema: item}} // a list of plain values
	}
	var out []field
	var walk func(prefix string, s *openapi3.Schema, depth int)
	walk = func(prefix string, s *openapi3.Schema, depth int) {
		order, props := properties(s)
		for _, n := range order {
			p := props[n]
			path := n
			if prefix != "" {
				path = prefix + "." + n
			}
			switch {
			case p.WriteOnly, len(p.OneOf) > 0, len(p.AnyOf) > 0:
				// writeOnly never comes back; unwrapNull already took
				// Optional[X], so what's left is several shapes.
			case isScalar(p):
				out = append(out, field{path: path, schema: p})
			case p.Type.Is("array"):
				if p.Items != nil && p.Items.Value != nil && isScalar(p.Items.Value) {
					out = append(out, field{path: path, schema: p, list: true})
				}
			case isMap(p):
			case isObject(p) && depth == 0:
				walk(path, p, 1)
			}
		}
	}
	walk("", item, 0)
	return out
}

// isMap is an object of arbitrary keys (additionalProperties), with no
// properties of its own to make columns of.
func isMap(s *openapi3.Schema) bool {
	return len(s.Properties) == 0 && len(s.AllOf) == 0 && (s.AdditionalProperties.Has != nil && *s.AdditionalProperties.Has || s.AdditionalProperties.Schema != nil)
}

// nameLike are the fields a row's identity is read from, in order.
var nameLike = []string{"name", "title", "display_name", "displayName", "full_name", "login", "label"}

// identityOf picks the identity column: a name-like field, else an id
// the detail would bind (id, <singular>_id, slug, key).
func identityOf(fs []field, listSeg string) string {
	one := singular(listSeg)
	for _, want := range append(slices.Clone(nameLike), "id", one+"_id", "slug", "key") {
		for _, f := range fs {
			if f.path == want && !f.list {
				return f.path
			}
		}
	}
	return ""
}

// rank classifies each field from its schema: identity, status (an enum,
// a boolean, or a status-like name), time (date-time / date), numbers,
// short text, then long text (description-like names, or a maxLength
// over 80).
func rank(fs []field, identity string) []columns.Column {
	titles := fieldTitles(fs)
	out := make([]columns.Column, len(fs))
	for i, f := range fs {
		s := unwrapNull(f.schema)
		name := lastSegment(f.path)
		c := columns.Column{Path: f.path, Title: titles[i], Values: enumValues(s)}
		numeric := !f.list && (s.Type.Is("integer") || s.Type.Is("number"))
		if numeric {
			c.Sort = "number"
		}
		switch {
		case f.path == identity:
			c.Rank = columns.Identity
		case !f.list && (len(c.Values) > 0 || s.Type.Is("boolean") || columns.IsStatusName(name)):
			c.Rank = columns.Status
			c.Bool = s.Type.Is("boolean")
		case !f.list && (s.Format == "date-time" || s.Format == "date" || s.Type.Is("string") && columns.IsTimeName(name)):
			c.Rank = columns.Time
		case numeric:
			c.Rank = columns.Number
		case !f.list && (columns.IsLongName(name) || s.MaxLength != nil && *s.MaxLength > 80):
			c.Rank = columns.Long
		default:
			c.Rank = columns.Short
		}
		out[i] = c
	}
	return out
}

func lastSegment(path string) string { return path[strings.LastIndex(path, ".")+1:] }

// fieldTitles titles each field from its last segment in words,
// prefixing the parent's when two would clash ("Labels team").
func fieldTitles(fs []field) []string {
	out := make([]string, len(fs))
	count := map[string]int{}
	for i, f := range fs {
		if f.path == "." {
			out[i] = "Value"
		} else {
			out[i] = words(lastSegment(f.path))
		}
		count[out[i]]++
	}
	for i, f := range fs {
		if count[out[i]] > 1 && strings.Contains(f.path, ".") {
			segs := strings.Split(f.path, ".")
			out[i] = words(segs[len(segs)-2]) + " " + strings.ToLower(out[i])
		}
	}
	return out
}

// columnNode writes one column. sortable is false on a window, where
// sort_fields decides.
func columnNode(c columns.Column, st columns.Style, sortable bool, distinct bool) *yaml.Node {
	col := mapping("title", str(c.Title), "value", str(c.Path))
	if sortable {
		add(col, "sortable", boolean(true))
	}
	if c.Sort != "" && sortable {
		add(col, "sort", str(c.Sort))
	}
	if distinct && len(c.Values) > 0 {
		add(col, "distinct", flow(strs(c.Values)))
	}
	if st.Bold {
		add(col, "bold", boolean(true))
	}
	if st.Align != "" {
		add(col, "align", str(st.Align))
	}
	if st.Flex > 0 {
		add(col, "flex", intNode(st.Flex))
	}
	if st.MaxWidth > 0 {
		add(col, "max_width", intNode(st.MaxWidth))
	}
	if len(st.Rules) == 0 {
		return flow(col)
	}
	rules := seq()
	for _, r := range st.Rules {
		rules.Content = append(rules.Content, flow(mapping("when", str(r.When), "color", str(r.Color))))
	}
	add(col, "color_rules", rules)
	return col
}

// inspectorRules colour a detail's enum fields, each rule scoped by its
// path, from the status vocabulary.
func inspectorRules(item *openapi3.Schema) *yaml.Node {
	rules := seq()
	for _, f := range schemaFields(item) {
		if f.list {
			continue
		}
		for _, r := range columns.StatusRules(enumValues(f.schema)) {
			rules.Content = append(rules.Content, flow(mapping("when", str(r.When), "color", str(r.Color), "path", str(f.path))))
		}
	}
	if len(rules.Content) == 0 {
		return nil
	}
	return rules
}
