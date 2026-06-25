package expr

import (
	"strings"
	"testing"
)

func TestCompileAndEval(t *testing.T) {
	p, err := Compile("status.phase == 'Running'")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{
		"status": map[string]any{"phase": "Running"},
	}
	got, err := EvalBool(p, env)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Errorf("Running pod should pass, got false")
	}
}

func TestEvalBoolFalseShapes(t *testing.T) {
	cases := []struct {
		src  string
		env  map[string]any
		want bool
	}{
		{"status.phase == 'Running'", map[string]any{"status": map[string]any{"phase": "Pending"}}, false},
		{"missing == 'x'", map[string]any{}, false},
		{"name", map[string]any{"name": ""}, false},
		{"name", map[string]any{"name": "set"}, true},
	}
	for _, tc := range cases {
		p, err := Compile(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := EvalBool(p, tc.env)
		if err != nil {
			t.Fatalf("eval %q: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestCompileEmptyErrors(t *testing.T) {
	if _, err := Compile(""); err == nil {
		t.Errorf("expected empty expr to fail compile")
	}
	if _, err := Compile("   "); err == nil {
		t.Errorf("expected whitespace-only expr to fail compile")
	}
}

func TestCompileBadSyntaxErrors(t *testing.T) {
	_, err := Compile("=== weird syntax")
	if err == nil {
		t.Errorf("expected syntax error")
	}
	if !strings.Contains(err.Error(), "compile") {
		t.Errorf("error should mention compile; got: %v", err)
	}
}

func TestBuiltinFunctions(t *testing.T) {
	// Mix of our added helpers (lower/upper) and expr-lang's native
	// built-ins (contains as infix, hasPrefix/hasSuffix as functions,
	// matches for regex, len for length) to make sure the wrap doesn't
	// clobber either.
	cases := []struct {
		src  string
		env  map[string]any
		want bool
	}{
		{"lower(name) == 'foo'", map[string]any{"name": "FOO"}, true},
		{"upper(name) == 'FOO'", map[string]any{"name": "foo"}, true},
		{"name contains 'ube'", map[string]any{"name": "kube-system"}, true},
		{"name contains 'xyz'", map[string]any{"name": "kube-system"}, false},
		{"hasPrefix(name, 'kube-')", map[string]any{"name": "kube-system"}, true},
		{"hasSuffix(name, 'system')", map[string]any{"name": "kube-system"}, true},
		{"name matches 'kube-.*'", map[string]any{"name": "kube-system"}, true},
		{"len(items) > 0", map[string]any{"items": []any{1, 2}}, true},
		{"len(items) > 0", map[string]any{"items": []any{}}, false},
		{"len(name) == 3", map[string]any{"name": "abc"}, true},
	}
	for _, tc := range cases {
		p, err := Compile(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := EvalBool(p, tc.env)
		if err != nil {
			t.Fatalf("eval %q: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestEvalWithParams(t *testing.T) {
	// Pipeline-bound params surface as `params.X` in the expression.
	p, err := Compile("len(name) >= int(params.min_len)")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		params map[string]string
		name   string
		want   bool
	}{
		{map[string]string{"min_len": "5"}, "alpha", true},
		{map[string]string{"min_len": "5"}, "abc", false},
		{map[string]string{"min_len": "10"}, "alpha", false},
	}
	for _, tc := range cases {
		got, err := EvalBoolWithParams(p, map[string]any{"name": tc.name}, tc.params)
		if err != nil {
			t.Fatalf("%v: %v", tc.params, err)
		}
		if got != tc.want {
			t.Errorf("name=%q params=%v: got %v, want %v", tc.name, tc.params, got, tc.want)
		}
	}
}

func TestEvalParamsDontShadowItemFields(t *testing.T) {
	// Item's top-level fields stay accessible alongside params.
	// `status.phase` is the item; `params.target` is the bound param.
	p, err := Compile("status.phase == params.target")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{
		"status": map[string]any{"phase": "Running"},
	}
	got, err := EvalBoolWithParams(p, env, map[string]string{"target": "Running"})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Errorf("phase=Running, target=Running should match; got false")
	}
}

func TestEvalBoolNonBoolerrors(t *testing.T) {
	// A numeric expression that returns a number greater than 0 is
	// truthy, but a complex object isn't bool-coercible.
	p, err := Compile("status")
	if err != nil {
		t.Fatal(err)
	}
	_, err = EvalBool(p, map[string]any{
		"status": map[string]any{"phase": "Running"},
	})
	if err == nil {
		t.Errorf("expected non-bool-coercible map to error; got nil")
	}
}
