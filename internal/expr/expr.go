// Package expr is the data layer's expression evaluator. Pipeline
// operators that need conditional logic (filter `where:`, derive
// `compute:`, join `on:`) compile expressions once at Build time and
// evaluate them per item at Fetch / Subscribe time.
//
// The package wraps github.com/expr-lang/expr behind a tiny Go
// interface so the rest of the codebase doesn't import the library
// directly. Centralizing this boundary lets us:
//
//   - Swap evaluators later (cel-go, govaluate, custom) without
//     touching every operator that compiles expressions.
//   - Concentrate built-in functions (now, len, lower, upper, …) in
//     one place — operator implementations get a consistent set of
//     helpers automatically.
//   - Format error messages uniformly so configs surface compile and
//     runtime errors the same way regardless of evaluator.
//
// Like the rest of the data layer, this package never imports anything
// TUI-related. A CI check enforces.
package expr

import (
	"fmt"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// Program is a compiled expression ready for evaluation. Compile once,
// run many — building once at pipeline.Build keeps per-item evaluation
// cheap.
type Program struct {
	src     string // original source for error messages
	program *vm.Program
}

// Compile parses and compiles src. The returned Program can be Eval'd
// against any environment (map[string]any or struct). Compilation
// errors include the original expression so config debugging is
// straightforward.
func Compile(src string) (*Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("expr: empty expression")
	}
	prog, err := expr.Compile(src, expr.AllowUndefinedVariables())
	if err != nil {
		return nil, fmt.Errorf("expr: compile %q: %w", src, err)
	}
	return &Program{src: src, program: prog}, nil
}

// Eval runs the program against env (typically the item being
// evaluated, exposed at the top level — so `status.phase` works
// without an `item.` prefix). Pipeline-level parameters are passed
// separately via EvalWithParams; this convenience form is for
// callers that don't have params (or only have the item).
func Eval(p *Program, env any) (any, error) {
	return EvalWithParams(p, env, nil)
}

// EvalWithParams runs the program with the item AND a separate
// pipeline-bound parameters map. Params show up as `params.<name>`
// in the expression; item fields stay at the top level. Pass nil
// for params when the operator has none.
func EvalWithParams(p *Program, env any, params map[string]string) (any, error) {
	if p == nil {
		return nil, fmt.Errorf("expr: nil program")
	}
	wrapped := wrapEnv(env)
	if len(params) > 0 {
		// Convert string → any for the env. params.X access in expressions
		// returns the raw string; operator authors can wrap with int()
		// or float() as needed.
		paramsAny := make(map[string]any, len(params))
		for k, v := range params {
			paramsAny[k] = v
		}
		wrapped["params"] = paramsAny
	}
	out, err := expr.Run(p.program, wrapped)
	if err != nil {
		return nil, fmt.Errorf("expr: run %q: %w", p.src, err)
	}
	return out, nil
}

// EvalBool runs the program and coerces the result to bool. Used by
// predicate operators (filter `where:`, future `if` clauses).
//
// Coercion rules: real bool returns directly; nil and "" → false;
// non-zero numbers → true; non-empty strings (besides "") → true.
// Any other type errors so a misspelled field doesn't silently pass
// the filter.
func EvalBool(p *Program, env any) (bool, error) {
	return EvalBoolWithParams(p, env, nil)
}

// EvalBoolWithParams is EvalBool with pipeline-bound params available
// as `params.<name>` in the expression env.
func EvalBoolWithParams(p *Program, env any, params map[string]string) (bool, error) {
	out, err := EvalWithParams(p, env, params)
	if err != nil {
		return false, err
	}
	switch v := out.(type) {
	case bool:
		return v, nil
	case nil:
		return false, nil
	case string:
		return v != "", nil
	case int:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	}
	return false, fmt.Errorf("expr: %q returned %T, want bool-coercible (bool/string/number)", p.src, out)
}

// wrapEnv enriches the caller's environment with built-in functions
// available in every expression. We inject only the helpers expr-lang
// doesn't natively provide — string `contains` / `startsWith` /
// `endsWith` / `matches`, `len` over strings/slices/maps, numeric
// comparison are all native. Time arithmetic is what we add here.
//
// We use the caller's env as a map-of-everything so top-level field
// access works without an `item.` prefix (`status.phase` is what
// people actually want to write).
func wrapEnv(env any) map[string]any {
	out := map[string]any{
		// Time helpers — useful for filtering on freshness, derive on age.
		"now": func() time.Time { return time.Now() },
		"parseTime": func(s string) (time.Time, error) {
			// Common formats first; expand the list as use-cases arrive.
			for _, layout := range []string{
				time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02",
			} {
				if t, err := time.Parse(layout, s); err == nil {
					return t, nil
				}
			}
			return time.Time{}, fmt.Errorf("parseTime: cannot parse %q", s)
		},

		// Case helpers — handy for case-insensitive equality without a
		// dedicated operator (and `lower(x) == 'foo'` reads cleanly).
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
	}
	// Splat the caller's env into the same map so top-level field
	// access works (`status.phase` rather than `item.status.phase`).
	if m, ok := env.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	// For non-map envs we still want top-level access via the typed
	// item, so expose it under `item`.
	out["item"] = env
	return out
}
