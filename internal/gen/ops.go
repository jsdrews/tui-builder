package gen

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// op is one operation, with where it is.
type op struct {
	method string // GET, POST…
	path   string
	item   *openapi3.PathItem
	op     *openapi3.Operation
	line   int // where it is in the spec, for ordering
}

func (o op) String() string { return o.method + " " + o.path }

// Select is which operations to generate from.
type Select struct {
	// Tags and Operations narrow to those listed (operationIds, globs
	// like list_*); ExcludeTags and ExcludeOperations then remove.
	Tags, ExcludeTags             []string
	Operations, ExcludeOperations []string
	// ReadOnly generates nothing from POST, PUT, PATCH or DELETE.
	ReadOnly bool
	// IncludeDeprecated keeps deprecated: true operations.
	IncludeDeprecated bool
}

var methods = []string{"GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE"}

// operations lists the spec's operations in the order they're written.
func operations(doc *openapi3.T) []op {
	var out []op
	for _, p := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(p)
		if item == nil {
			continue
		}
		for _, m := range methods {
			o := item.GetOperation(m)
			if o == nil {
				continue
			}
			out = append(out, op{method: m, path: p, item: item, op: o, line: lineOf(o.Origin, item.Origin)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].line != out[j].line {
			return out[i].line < out[j].line
		}
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return slices.Index(methods, out[i].method) < slices.Index(methods, out[j].method)
	})
	return out
}

// lineOf is the first origin's line; 0 when none was recorded (a spec
// read from JSON records lines too, so 0 is rare).
func lineOf(origins ...*openapi3.Origin) int {
	for _, o := range origins {
		if o != nil && o.Key != nil && o.Key.Line > 0 {
			return o.Key.Line
		}
	}
	return 0
}

// selected says whether an operation passes --tag / --operation and
// their excludes. Deselected operations get no skip line.
func (sel Select) selected(o op) bool {
	if sel.ReadOnly && o.method != "GET" && o.method != "HEAD" {
		return false
	}
	id := o.op.OperationID
	if len(sel.Tags) > 0 && !slices.ContainsFunc(o.op.Tags, func(t string) bool { return matchAny(sel.Tags, t) }) {
		return false
	}
	if len(sel.Operations) > 0 && !matchAny(sel.Operations, id) {
		return false
	}
	if slices.ContainsFunc(o.op.Tags, func(t string) bool { return matchAny(sel.ExcludeTags, t) }) {
		return false
	}
	return !matchAny(sel.ExcludeOperations, id)
}

func matchAny(globs []string, s string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, s); ok {
			return true
		}
	}
	return false
}

// skip is an operation not generated, and why.
type skip struct {
	op     op
	reason string
}

func (s skip) String() string { return fmt.Sprintf("skipped %s: %s", s.op, s.reason) }

// skipReason is why an operation can't be generated whatever its shape,
// or "".
func (sel Select) skipReason(o op) string {
	if o.op.Deprecated && !sel.IncludeDeprecated {
		return "deprecated (keep it with --include-deprecated)"
	}
	for _, p := range params(o) {
		if p.Required && (p.In == "header" || p.In == "cookie") {
			return fmt.Sprintf("needs the %s %s %q, which gen can't fill", p.In, "parameter", p.Name)
		}
	}
	if o.method == "GET" {
		if _, ct := okResponse(o.op); ct != "" && !isJSON(ct) {
			return fmt.Sprintf("answers %s, not JSON", ct)
		}
	}
	return ""
}

// params are an operation's parameters, the path item's included (the
// operation's win on a name clash).
func params(o op) []*openapi3.Parameter {
	byKey := map[string]*openapi3.Parameter{}
	var order []string
	for _, refs := range []openapi3.Parameters{o.item.Parameters, o.op.Parameters} {
		for _, r := range refs {
			if r == nil || r.Value == nil {
				continue
			}
			k := r.Value.In + ":" + r.Value.Name
			if _, ok := byKey[k]; !ok {
				order = append(order, k)
			}
			byKey[k] = r.Value
		}
	}
	out := make([]*openapi3.Parameter, len(order))
	for i, k := range order {
		out[i] = byKey[k]
	}
	return out
}

// okResponse is the operation's 200 (else first 2xx) response and its
// first content type, JSON preferred.
func okResponse(o *openapi3.Operation) (*openapi3.Response, string) {
	if o.Responses == nil {
		return nil, ""
	}
	codes := make([]string, 0, o.Responses.Len())
	for code := range o.Responses.Map() {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	pick := ""
	if slices.Contains(codes, "200") {
		pick = "200"
	} else {
		for _, c := range codes {
			if strings.HasPrefix(c, "2") {
				pick = c
				break
			}
		}
	}
	if pick == "" {
		return nil, ""
	}
	r := o.Responses.Value(pick)
	if r == nil || r.Value == nil {
		return nil, ""
	}
	types := make([]string, 0, len(r.Value.Content))
	for ct := range r.Value.Content {
		types = append(types, ct)
	}
	sort.Strings(types)
	for _, ct := range types {
		if isJSON(ct) {
			return r.Value, ct
		}
	}
	if len(types) > 0 {
		return r.Value, types[0]
	}
	return r.Value, ""
}

func isJSON(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	return ct == "application/json" || strings.HasSuffix(ct, "+json") || ct == "*/*"
}

// responseSchema is the JSON schema of an operation's success response.
func responseSchema(o *openapi3.Operation) *openapi3.Schema {
	r, ct := okResponse(o)
	if r == nil || !isJSON(ct) {
		return nil
	}
	mt := r.Content[ct]
	if mt == nil || mt.Schema == nil {
		return nil
	}
	return mt.Schema.Value
}
