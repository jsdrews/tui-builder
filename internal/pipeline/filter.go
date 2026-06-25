package pipeline

import (
	"encoding/json"
	"fmt"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// newFilter builds a Pipeline that drops items not matching def.Where.
//
// The predicate is compiled once at Build time; per-item evaluation
// reuses the compiled program so a high-volume stream doesn't pay the
// parse cost on every event.
//
// Behavior by upstream shape:
//   - Snapshot []any: returns the subset that passes the predicate.
//   - Snapshot single object / scalar: returns the value if it passes,
//     else nil. (Consumers of one-of-X data — wrangl --pretty,
//     inspector binding — see "filtered out" as a clean nil rather
//     than an empty slice.)
//   - Streaming Event: the event is parsed as JSON (so dot-path field
//     access works); a non-JSON line is evaluated against the raw
//     string with `item` as the binding. Events that fail the
//     predicate drop silently; events with errors surface as Event{Err}.
func newFilter(name string, upstream ds.Source, def *cfg.FilterOp, params map[string]string) (*Pipeline, error) {
	prog, err := expr.Compile(def.Where)
	if err != nil {
		return nil, fmt.Errorf("filter: %w", err)
	}
	p := &Pipeline{name: name, upstream: upstream}
	p.transformSnapshot = func(data any) (any, error) {
		return filterSnapshot(data, prog, params)
	}
	p.transformEvent = func(ev ds.Event) (ds.Event, bool, error) {
		return filterEvent(ev, prog, params)
	}
	return p, nil
}

func filterSnapshot(data any, prog *expr.Program, params map[string]string) (any, error) {
	switch x := data.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			keep, err := expr.EvalBoolWithParams(prog, item, params)
			if err != nil {
				return nil, err
			}
			if keep {
				out = append(out, item)
			}
		}
		return out, nil
	default:
		keep, err := expr.EvalBoolWithParams(prog, x, params)
		if err != nil {
			return nil, err
		}
		if keep {
			return x, nil
		}
		return nil, nil
	}
}

// filterEvent decides keep/drop for one streaming event. JSON-shaped
// payloads parse so the predicate gets typed field access
// (`status.phase == 'Running'`). Non-JSON payloads (kube log lines,
// follow-mode text frames) fall through to string-as-item evaluation
// — the predicate then references the line as `item`
// (`item contains 'ERROR'`).
func filterEvent(ev ds.Event, prog *expr.Program, params map[string]string) (ds.Event, bool, error) {
	if ev.Err != nil {
		// Pass upstream errors through unchanged. The pipeline's
		// transform isn't about diagnostics.
		return ev, true, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(ev.Line), &parsed); err != nil {
		parsed = ev.Line
	}
	keep, err := expr.EvalBoolWithParams(prog, parsed, params)
	if err != nil {
		return ev, false, err
	}
	return ev, keep, nil
}
