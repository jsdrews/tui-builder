package pipeline

import (
	"fmt"
	"sort"
	"strings"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// sortStrings sorts a slice of strings in place. Wrapping the stdlib
// call here so callers in pipeline.go (Registry.Names) don't have to
// pull in `sort` themselves.
func sortStrings(s []string) { sort.Strings(s) }

// newSort builds a snapshot-only Pipeline that orders the upstream
// iterable by a per-item key expression. The key is evaluated once
// per item via expr; sort.SliceStable preserves the input order on
// ties so repeated fetches produce deterministic output.
//
// Subscribe returns ErrNotStreaming — sorting a true event stream
// needs windowing semantics we don't have yet, and silently
// degrading to "sort the first N events" would surprise users.
func newSort(name string, upstream ds.Source, def *cfg.Source, params map[string]string) (*Pipeline, error) {
	prog, err := expr.Compile(def.By)
	if err != nil {
		return nil, fmt.Errorf("sort: %w", err)
	}
	desc := strings.EqualFold(def.Order, "desc")
	p := &Pipeline{
		name:             name,
		upstream:         upstream,
		disableStreaming: true,
	}
	p.transformSnapshot = func(data any) (any, error) {
		return sortSnapshot(data, prog, desc, params)
	}
	return p, nil
}

func sortSnapshot(data any, prog *expr.Program, desc bool, params map[string]string) (any, error) {
	items, ok := data.([]any)
	if !ok {
		// Non-iterable upstream: nothing to sort. Return as-is so
		// downstream consumers see the same shape sort got.
		return data, nil
	}
	keys := make([]any, len(items))
	for i, it := range items {
		k, err := expr.EvalWithParams(prog, it, params)
		if err != nil {
			return nil, fmt.Errorf("sort key at index %d: %w", i, err)
		}
		keys[i] = k
	}
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		cmp := compareKeys(keys[idx[a]], keys[idx[b]])
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
	out := make([]any, len(items))
	for i, j := range idx {
		out[i] = items[j]
	}
	return out, nil
}

// compareKeys orders two arbitrary expression results. Handles bool,
// number (int / int64 / float64), string, and time.Time. Mixed-type
// pairs fall back to comparing their string representations so a
// heterogeneous list still produces *some* deterministic order
// rather than panicking.
//
// nil sorts BEFORE other values so missing keys cluster at the top
// in ascending order (and at the bottom in descending) — matches
// the kubectl / jq convention.
func compareKeys(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			switch {
			case af < bf:
				return -1
			case af > bf:
				return 1
			default:
				return 0
			}
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return strings.Compare(as, bs)
		}
	}
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			switch {
			case ab == bb:
				return 0
			case !ab && bb:
				return -1
			default:
				return 1
			}
		}
	}
	if at, ok := a.(time.Time); ok {
		if bt, ok := b.(time.Time); ok {
			switch {
			case at.Before(bt):
				return -1
			case at.After(bt):
				return 1
			default:
				return 0
			}
		}
	}
	// Mixed-type fallback — at least produces *some* deterministic order.
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	}
	return 0, false
}
