package datasource

import (
	"fmt"
	"strconv"
	"strings"
)

// Get resolves a dot-path into a parsed JSON value. Supported syntax:
//
//	"a.b.c"        nested object keys
//	"a[0].b"       array index, optionally chained
//	"a.0.b"        array index expressed as a dotted segment
//	""             return v unchanged
//
// Missing keys / out-of-range indices return nil. Type-mismatches (e.g.
// indexing a non-array) also return nil — callers render nil as the empty
// string. The function never panics.
func Get(v any, path string) any {
	if path == "" {
		return v
	}
	cur := v
	for _, seg := range splitPath(path) {
		if cur == nil {
			return nil
		}
		switch c := cur.(type) {
		case map[string]any:
			cur = c[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil
			}
			cur = c[i]
		default:
			return nil
		}
	}
	return cur
}

// String resolves a path and renders the result as a display string.
// Strings pass through, numbers / booleans format predictably, nested
// values fall back to fmt.Sprint.
func String(v any, path string) string {
	r := Get(v, path)
	if r == nil {
		return ""
	}
	switch x := r.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		// JSON numbers parse as float64; render integers without ".0".
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(r)
}

// splitPath splits "a.b[0].c" into ["a", "b", "0", "c"]. Bracketed
// segments are recognised but they otherwise behave like dotted ones.
func splitPath(p string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '.':
			flush()
		case '[':
			flush()
		case ']':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out
}

// Iter returns v as a slice when v is a JSON array, or wraps a single
// object in a 1-element slice. Used by list/table bindings that walk an
// iterable root.
func Iter(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case nil:
		return nil
	default:
		return []any{v}
	}
}
