package gen

import (
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
)

// properties is a schema's properties with allOf merged in, in the
// order they're declared. Recursive schemas are pointer cycles in
// kin-openapi, so it keeps a visited set.
func properties(s *openapi3.Schema) ([]string, map[string]*openapi3.Schema) {
	var order []string
	props := map[string]*openapi3.Schema{}
	visited := map[*openapi3.Schema]bool{}
	var walk func(s *openapi3.Schema)
	walk = func(s *openapi3.Schema) {
		if s == nil || visited[s] {
			return
		}
		visited[s] = true
		for _, sub := range s.AllOf {
			if sub != nil {
				walk(sub.Value)
			}
		}
		// By name first, so properties with no recorded line (a spec
		// built in code) still come out the same every time.
		names := sortedKeys(s.Properties)
		slices.SortStableFunc(names, func(a, b string) int {
			return lineOf(propOrigin(s.Properties[a])) - lineOf(propOrigin(s.Properties[b]))
		})
		for _, n := range names {
			ref := s.Properties[n]
			if ref == nil || ref.Value == nil {
				continue
			}
			if _, seen := props[n]; !seen {
				order = append(order, n)
			}
			props[n] = unwrapNull(ref.Value)
		}
	}
	walk(s)
	return order, props
}

// propOrigin is where a property is written: the $ref's own origin for
// a reference (its schema's is the referenced definition's), else the
// inline schema's.
func propOrigin(r *openapi3.SchemaRef) *openapi3.Origin {
	switch {
	case r == nil:
		return nil
	case r.Ref != "" && r.Origin != nil:
		return r.Origin
	case r.Value != nil:
		return r.Value.Origin
	}
	return nil
}

// unwrapNull turns Optional[X] (anyOf/oneOf of X and null) into X.
func unwrapNull(s *openapi3.Schema) *openapi3.Schema {
	for _, alts := range []openapi3.SchemaRefs{s.AnyOf, s.OneOf} {
		var nonNull []*openapi3.Schema
		for _, a := range alts {
			if a != nil && a.Value != nil && !a.Value.Type.Is("null") {
				nonNull = append(nonNull, a.Value)
			}
		}
		if len(alts) == 2 && len(nonNull) == 1 {
			return nonNull[0]
		}
	}
	return s
}

// shape is what a response holds.
type shape int

const (
	shapeNone   shape = iota // no schema to tell
	shapeList                // rows: an array, or an object wrapping one
	shapeObject              // one object
	shapeScalar              // a string, number…
)

// listNames are the wrapper fields that win when an object has several
// lists of objects.
var listNames = []string{"entries", "items", "data", "results", "records", "values"}

// shapeOf reads a response schema: an array is a list (no root); an
// object with exactly one list of objects is a list rooted there, as is
// one with several when one of them has a usual name; any other object
// is one object.
func shapeOf(s *openapi3.Schema) (shape, string) {
	if s == nil {
		return shapeNone, ""
	}
	s = unwrapNull(s)
	if s.Type.Is("array") {
		return shapeList, ""
	}
	order, props := properties(s)
	if !s.Type.Is("object") && len(order) == 0 && len(s.AllOf) == 0 {
		if s.Type == nil || len(*s.Type) == 0 {
			return shapeNone, ""
		}
		return shapeScalar, ""
	}
	var lists []string
	for _, n := range order {
		if p := props[n]; p.Type.Is("array") && p.Items != nil && p.Items.Value != nil && isObject(p.Items.Value) {
			lists = append(lists, n)
		}
	}
	switch {
	case len(lists) == 1:
		return shapeList, lists[0]
	case len(lists) > 1:
		for _, want := range listNames {
			if slices.Contains(lists, want) {
				return shapeList, want
			}
		}
		return shapeList, ""
	}
	return shapeObject, ""
}

func isObject(s *openapi3.Schema) bool {
	s = unwrapNull(s)
	return s.Type.Is("object") || len(s.Properties) > 0 || len(s.AllOf) > 0
}
