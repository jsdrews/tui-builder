package datasource

import "testing"

// A list of scalars in a cell reads as "a, b"; anything holding an
// object or a list keeps the old fmt.Sprint form.
func TestStringJoinsScalarLists(t *testing.T) {
	item := map[string]any{
		"tags":  []any{"web", "prod"},
		"ports": []any{float64(80), float64(443)},
		"mixed": []any{"a", true, nil},
		"none":  []any{},
		"objs":  []any{map[string]any{"n": "x"}},
	}
	for path, want := range map[string]string{
		"tags":  "web, prod",
		"ports": "80, 443",
		"mixed": "a, true, ",
		"none":  "",
		"objs":  "[map[n:x]]",
	} {
		if got := String(item, path); got != want {
			t.Errorf("String(%s) = %q, want %q", path, got, want)
		}
	}
}
