package datasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// mergeSource fans out to N children concurrently, unions their results
// into a single slice, and optionally tags each map-shaped item with
// which child source it came from. The composer for cross-cluster /
// cross-account / cross-anything views.
//
// Result shape: always `[]any`. Children that return slices are
// flattened in; children that return a single object are appended as
// one element. Children whose result is not slice-or-map are appended
// untagged (the TagField is meaningless for scalars).
//
// Children are resolved at Build time (Datasource.Build wires them in)
// so cycles are impossible by then.
type mergeSource struct {
	children []namedChild // stable order from cfg.Sources
	tagField string
	onError  errorMode
	root     string
	refresh  time.Duration
}

type namedChild struct {
	name string
	src  Source
}

type errorMode int

const (
	failFast errorMode = iota
	skipBroken
)

func newMerge(d *cfg.DataSource, children map[string]Source) (Source, error) {
	// Preserve the cfg.Sources order — children render in the order the
	// user wrote, not Go's map iteration order.
	ordered := make([]namedChild, 0, len(d.Sources))
	for _, name := range d.Sources {
		s, ok := children[name]
		if !ok {
			return nil, fmt.Errorf("merge child %q missing from registry", name)
		}
		ordered = append(ordered, namedChild{name: name, src: s})
	}
	mode := failFast
	if d.OnError == "skip" {
		mode = skipBroken
	}
	var refresh time.Duration
	if d.Refresh != "" {
		r, err := time.ParseDuration(d.Refresh)
		if err != nil {
			return nil, fmt.Errorf("refresh: %w", err)
		}
		refresh = r
	}
	return &mergeSource{
		children: ordered,
		tagField: d.TagField,
		onError:  mode,
		root:     d.Root,
		refresh:  refresh,
	}, nil
}

func (m *mergeSource) Refresh() time.Duration { return m.refresh }

// Subscribe fans events from every streaming child into one merged
// channel. When all children implement StreamingSource, merge IS
// streaming — the screen takes the streaming path (no polling). When
// any child is not streaming, Subscribe returns an error; the screen
// falls back to merge.Fetch (polling fan-out).
//
// Each child's events keep their child name written into the merged
// item via TagField (same as for snapshot merges), so downstream
// bindings can identify which stream contributed which frame. Errors
// from individual children become Events with Err set; the merged
// stream survives until ctx cancels or all children's streams close.
func (m *mergeSource) Subscribe(ctx context.Context) (<-chan Event, error) {
	// If any child can't stream, the whole merge can't either. Return
	// the sentinel so the screen falls back to Fetch (polling), which
	// is what existing snapshot-merge configs depend on.
	streamers := make([]StreamingSource, len(m.children))
	for i, c := range m.children {
		s, ok := c.src.(StreamingSource)
		if !ok {
			return nil, ErrNotStreaming
		}
		streamers[i] = s
	}

	out := make(chan Event, 256)
	var wg sync.WaitGroup
	for i, c := range m.children {
		childCh, err := streamers[i].Subscribe(ctx)
		if err != nil {
			// Drain anything that already subscribed by cancelling
			// ctx (parent will too on return), then surface the error.
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		wg.Add(1)
		go func(name string, in <-chan Event) {
			defer wg.Done()
			for ev := range in {
				if ev.Err != nil {
					select {
					case out <- Event{Err: fmt.Errorf("%s: %w", name, ev.Err)}:
					case <-ctx.Done():
						return
					}
					continue
				}
				// Tag-inject for JSON map frames so downstream
				// bindings can see which child a frame came from
				// (same convention as snapshot merges). Non-map
				// payloads and non-JSON lines pass through untouched.
				line := tagInline(ev.Line, m.tagField, name)
				select {
				case out <- Event{Line: line}:
				case <-ctx.Done():
					return
				}
			}
		}(c.name, childCh)
	}
	go func() { wg.Wait(); close(out) }()
	return out, nil
}

// tagInline rewrites a top-level JSON object's payload to include
// {<tagField>: <source>} so consumers can identify the origin. Falls
// back to returning the line unchanged when:
//   - tagField is empty (caller doesn't want tagging)
//   - the line isn't a JSON object (e.g. an array, a string, or the
//     synthetic "(connecting…)" diagnostic from streaming sources)
//
// Tagging is purely additive — existing fields in the payload survive.
func tagInline(line, tagField, source string) string {
	if tagField == "" {
		return line
	}
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return line
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return line
	}
	obj[tagField] = source
	b, err := json.Marshal(obj)
	if err != nil {
		return line
	}
	return string(b)
}

func (m *mergeSource) Fetch(ctx context.Context) (any, error) {
	type result struct {
		idx  int
		name string
		data any
		err  error
	}

	// Concurrent fan-out — one goroutine per child. The slowest child
	// caps total latency; child timeouts cap each branch independently.
	resCh := make(chan result, len(m.children))
	var wg sync.WaitGroup
	for i, c := range m.children {
		wg.Add(1)
		go func(i int, c namedChild) {
			defer wg.Done()
			data, err := c.src.Fetch(ctx)
			resCh <- result{idx: i, name: c.name, data: data, err: err}
		}(i, c)
	}
	wg.Wait()
	close(resCh)

	// Collect into a slice indexed by child position so ordering matches
	// the cfg.Sources order regardless of which goroutine finished first.
	results := make([]result, len(m.children))
	for r := range resCh {
		results[r.idx] = r
	}

	var (
		out      []any
		errs     []string
		succeeds int
	)
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.name, r.err))
			if m.onError == failFast {
				return nil, fmt.Errorf("merge: %s", errs[len(errs)-1])
			}
			continue
		}
		succeeds++
		appendChild(&out, r.data, m.tagField, r.name)
	}
	// In skip mode, only fail when every child failed. Otherwise return
	// the partial union — surfacing partial errors via the Source
	// interface would require a richer return type; for v1 we eat the
	// per-child errors and trust the data shape to flag missing
	// clusters (rows with that tag will simply be absent).
	if succeeds == 0 && len(errs) > 0 {
		sort.Strings(errs)
		return nil, errors.New("merge: " + strings.Join(errs, "; "))
	}
	// Children already have their own roots applied (each source slices
	// before returning), so the union is the useful list. Apply merge's
	// own root on top in case the user wants to slice further.
	merged := applyRoot([]any(out), m.root)
	if len(errs) > 0 {
		// Skip mode with partial failure: return BOTH the surviving
		// data AND a non-nil error so the screen can render the union
		// while surfacing which children went bad. Otherwise these
		// failures are invisible — a real problem when one cluster is
		// unreachable but the rest aren't.
		sort.Strings(errs)
		return merged, errors.New("merge (partial): " + strings.Join(errs, "; "))
	}
	return merged, nil
}

// appendChild adds one child's data to the accumulating result slice.
// Slices flatten in (with per-item tagging); single objects append as
// one element; everything else passes through untouched.
func appendChild(out *[]any, data any, tagField, source string) {
	switch x := data.(type) {
	case []any:
		for _, item := range x {
			*out = append(*out, tagItem(item, tagField, source))
		}
	case nil:
		// nothing to merge
	default:
		*out = append(*out, tagItem(data, tagField, source))
	}
}

// tagItem injects {tagField: source} into a map-shaped item. Non-map
// items pass through — the tag is silently dropped because there's
// nowhere coherent to put it. Mutates a copy, not the original.
func tagItem(item any, tagField, source string) any {
	if tagField == "" {
		return item
	}
	m, ok := item.(map[string]any)
	if !ok {
		return item
	}
	cp := make(map[string]any, len(m)+1)
	for k, v := range m {
		cp[k] = v
	}
	cp[tagField] = source
	return cp
}
