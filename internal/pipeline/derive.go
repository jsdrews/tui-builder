package pipeline

import (
	"encoding/json"
	"fmt"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// newDerive builds a Pipeline that copies each upstream item and adds
// extra fields computed from expressions. Useful for synthesizing
// derived values (age from a start timestamp, a status label from a
// status combination) without losing the original fields.
//
// Behavior by upstream shape:
//   - Snapshot []any: map items get extended in-place (on a copy);
//     non-map items pass through unchanged.
//   - Snapshot single map: returns the extended copy.
//   - Snapshot scalar / nil: returns unchanged (nowhere to add fields).
//   - Streaming JSON-frame event: parsed, extended, re-encoded.
//   - Streaming text-line event: passes through unchanged (no place
//     to add fields to a raw string; consumers using `item` style
//     would use filter, not derive).
func newDerive(name string, upstream ds.Source, def *cfg.DeriveOp, params map[string]string) (*Pipeline, error) {
	progs, err := compileMap(def.Compute, "derive.compute")
	if err != nil {
		return nil, err
	}
	p := &Pipeline{name: name, upstream: upstream}
	p.transformSnapshot = func(data any) (any, error) {
		return deriveSnapshot(data, progs, params)
	}
	p.transformEvent = func(ev ds.Event) (ds.Event, bool, error) {
		return deriveEvent(ev, progs, params)
	}
	return p, nil
}

func deriveSnapshot(data any, progs map[string]*expr.Program, params map[string]string) (any, error) {
	switch x := data.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			m, ok := item.(map[string]any)
			if !ok {
				out = append(out, item)
				continue
			}
			extended, err := deriveOne(m, progs, params)
			if err != nil {
				return nil, err
			}
			out = append(out, extended)
		}
		return out, nil
	case map[string]any:
		return deriveOne(x, progs, params)
	default:
		return x, nil
	}
}

// deriveOne returns an extended COPY of item. Existing keys survive;
// derived keys win on collision (intentional — lets the user override
// awkward source fields in-place).
func deriveOne(item map[string]any, progs map[string]*expr.Program, params map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(item)+len(progs))
	for k, v := range item {
		out[k] = v
	}
	for k, prog := range progs {
		v, err := expr.EvalWithParams(prog, item, params)
		if err != nil {
			return nil, fmt.Errorf("derive key %q: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

func deriveEvent(ev ds.Event, progs map[string]*expr.Program, params map[string]string) (ds.Event, bool, error) {
	if ev.Err != nil {
		return ev, true, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(ev.Line), &parsed); err != nil {
		// Non-JSON event — pass through unchanged. Derive on text
		// frames isn't semantically meaningful.
		return ev, true, nil
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return ev, true, nil
	}
	extended, err := deriveOne(m, progs, params)
	if err != nil {
		return ev, false, err
	}
	encoded, err := json.Marshal(extended)
	if err != nil {
		return ev, false, fmt.Errorf("derive re-encode: %w", err)
	}
	return ds.Event{Line: string(encoded)}, true, nil
}
