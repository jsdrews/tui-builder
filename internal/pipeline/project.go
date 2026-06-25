package pipeline

import (
	"encoding/json"
	"fmt"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// newProject builds a Pipeline that replaces each upstream item with
// a new object whose keys come from def.Keep. Each value in Keep is
// an expression evaluated against the input item; expressions are
// compiled once at Build time so per-item evaluation is cheap.
//
// Behavior by upstream shape:
//   - Snapshot []any: each map item becomes a slimmer object; non-map
//     items (scalars) drop because there's nothing to project from
//     them. Final shape is []any of the survivors.
//   - Snapshot single map: produces one projected object.
//   - Snapshot scalar / nil: returns nil.
//   - Streaming JSON-frame event: parsed, projected, re-encoded as JSON.
//   - Streaming text-line event: dropped (no fields to project from
//     a raw string).
func newProject(name string, upstream ds.Source, def *cfg.ProjectOp, params map[string]string) (*Pipeline, error) {
	progs, err := compileMap(def.Keep, "project.keep")
	if err != nil {
		return nil, err
	}
	p := &Pipeline{name: name, upstream: upstream}
	p.transformSnapshot = func(data any) (any, error) {
		return projectSnapshot(data, progs, params)
	}
	p.transformEvent = func(ev ds.Event) (ds.Event, bool, error) {
		return projectEvent(ev, progs, params)
	}
	return p, nil
}

func projectSnapshot(data any, progs map[string]*expr.Program, params map[string]string) (any, error) {
	switch x := data.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			if _, ok := item.(map[string]any); !ok {
				continue // skip non-map items
			}
			proj, err := projectOne(item, progs, params)
			if err != nil {
				return nil, err
			}
			out = append(out, proj)
		}
		return out, nil
	case map[string]any:
		return projectOne(x, progs, params)
	default:
		return nil, nil
	}
}

func projectOne(item any, progs map[string]*expr.Program, params map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(progs))
	for k, prog := range progs {
		v, err := expr.EvalWithParams(prog, item, params)
		if err != nil {
			return nil, fmt.Errorf("project key %q: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

func projectEvent(ev ds.Event, progs map[string]*expr.Program, params map[string]string) (ds.Event, bool, error) {
	if ev.Err != nil {
		return ev, true, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(ev.Line), &parsed); err != nil {
		// Non-JSON event — nothing to project from. Drop silently.
		return ev, false, nil
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return ev, false, nil
	}
	proj, err := projectOne(m, progs, params)
	if err != nil {
		return ev, false, err
	}
	encoded, err := json.Marshal(proj)
	if err != nil {
		return ev, false, fmt.Errorf("project re-encode: %w", err)
	}
	return ds.Event{Line: string(encoded)}, true, nil
}

// compileMap compiles every expression in src into an expr.Program.
// Used by both project (`keep:`) and derive (`compute:`). The label
// argument is woven into the error so config debugging tells you
// which operator failed.
func compileMap(src map[string]string, label string) (map[string]*expr.Program, error) {
	out := make(map[string]*expr.Program, len(src))
	for k, e := range src {
		prog, err := expr.Compile(e)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", label, k, err)
		}
		out[k] = prog
	}
	return out, nil
}
