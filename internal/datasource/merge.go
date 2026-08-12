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
// per-child metadata. The composer for cross-cluster / cross-account /
// cross-anything views.
//
// Result shape: always `[]any`. Children that return slices are
// flattened in; children that return a single object are appended as
// one element. Children whose result is not slice-or-map are appended
// untagged (tag injection is meaningless for scalars).
//
// Two configuration shapes feed the same internal model:
//
//   - Shorthand: cfg.Sources + cfg.TagField → one synthetic tag per
//     child, value = child source name. The legacy shape.
//   - Long form: cfg.Children — each entry declares an arbitrary tags
//     map; merge writes every key/value into rows from that child.
//
// Children are resolved at Build time so cycles are impossible by then.
type mergeSource struct {
	children []namedChild // stable order
	metaKey  string       // empty = flat top-level injection (legacy)
	onError  errorMode
	root     string
	refresh  time.Duration
}

// defaultMetaKey is the JSON key merge writes tag maps under when the
// config doesn't explicitly choose one. Conventional `_` prefix marks
// "framework-injected, not user data."
const defaultMetaKey = "_meta"

type namedChild struct {
	name string
	src  Source
	// tags get injected into every map-shaped row this child
	// produces. nil = no injection. The merge composer reads this
	// per-child rather than holding a single global TagField, so
	// both schema shapes share one code path.
	tags map[string]string
}

type errorMode int

const (
	failFast errorMode = iota
	skipBroken
)

// NewMerge constructs a merge composer from a cfg.MergeSource and a
// map of resolved child sources. Exposed so the pipeline package can
// reuse this proven implementation for its `union` operator without
// duplicating the streaming / snapshot / per-child-tag machinery.
//
// The caller is responsible for resolving children from
// cfg.Sources or cfg.Children (whichever shape the config uses)
// into the map before calling.
func NewMerge(d *cfg.Source, children map[string]Source) (Source, error) {
	return newMerge(d, children)
}

func newMerge(d *cfg.Source, children map[string]Source) (Source, error) {
	// Compose the internal child list from whichever schema shape the
	// user wrote. We collapse both into the same []namedChild so the
	// downstream Fetch/Subscribe code is shape-agnostic.
	var ordered []namedChild
	switch {
	case len(d.Children) > 0:
		// Long form: each MergeChild carries its own tags map.
		ordered = make([]namedChild, 0, len(d.Children))
		for _, ch := range d.Children {
			s, ok := children[ch.Source]
			if !ok {
				return nil, fmt.Errorf("merge child %q missing from registry", ch.Source)
			}
			ordered = append(ordered, namedChild{
				name: ch.Source,
				src:  s,
				tags: copyTags(ch.Tags),
			})
		}
	default:
		// Shorthand: each child gets ONE synthetic tag whose key is the
		// shared TagField and whose value is the source name. Same
		// effect as the legacy behavior, expressed in the new model.
		ordered = make([]namedChild, 0, len(d.Sources))
		for _, name := range d.Sources {
			s, ok := children[name]
			if !ok {
				return nil, fmt.Errorf("merge child %q missing from registry", name)
			}
			var tags map[string]string
			if d.TagField != "" {
				tags = map[string]string{d.TagField: name}
			}
			ordered = append(ordered, namedChild{name: name, src: s, tags: tags})
		}
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
	// MetaKey: default → "_meta", explicit empty → flat (opt-out).
	metaKey := defaultMetaKey
	if d.MetaKey != nil {
		metaKey = *d.MetaKey
	}
	return &mergeSource{
		children: ordered,
		metaKey:  metaKey,
		onError:  mode,
		root:     d.Root,
		refresh:  refresh,
	}, nil
}

// copyTags returns an independent map so downstream mutations on the
// merge's runtime state don't leak back into the parsed cfg.
func copyTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
		go func(name string, tags map[string]string, metaKey string, in <-chan Event) {
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
				line := tagInline(ev.Line, tags, metaKey)
				select {
				case out <- Event{Line: line}:
				case <-ctx.Done():
					return
				}
			}
		}(c.name, c.tags, m.metaKey, childCh)
	}
	go func() { wg.Wait(); close(out) }()
	return out, nil
}

// tagInline writes every key in tags into the JSON object on line —
// nested under metaKey when set, flat at the top level when metaKey
// is empty. Returns line unchanged when:
//   - tags is nil / empty (caller doesn't want tagging)
//   - the line isn't a JSON object (e.g. an array, a string, or the
//     synthetic "(connecting…)" diagnostic from streaming sources)
func tagInline(line string, tags map[string]string, metaKey string) string {
	if len(tags) == 0 {
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
	injectTags(obj, tags, metaKey)
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
		appendChild(&out, r.data, m.children[r.idx].tags, m.metaKey)
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
func appendChild(out *[]any, data any, tags map[string]string, metaKey string) {
	switch x := data.(type) {
	case []any:
		for _, item := range x {
			*out = append(*out, tagItem(item, tags, metaKey))
		}
	case nil:
		// nothing to merge
	default:
		*out = append(*out, tagItem(data, tags, metaKey))
	}
}

// tagItem writes every key/value in tags into a map-shaped item — nested
// under metaKey when set, flat at the top level when metaKey is empty.
// Non-map items pass through — tags are silently dropped because
// there's nowhere coherent to put them. Mutates a copy, not the
// original.
func tagItem(item any, tags map[string]string, metaKey string) any {
	if len(tags) == 0 {
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
	injectTags(cp, tags, metaKey)
	return cp
}

// injectTags is the shared writer used by both the snapshot (tagItem)
// and streaming (tagInline) paths. Centralised so the nesting rule
// stays consistent across both code paths.
func injectTags(obj map[string]any, tags map[string]string, metaKey string) {
	if metaKey == "" {
		for k, v := range tags {
			obj[k] = v
		}
		return
	}
	// Nested under metaKey. If the upstream payload already has a
	// value at that key (very unlikely for the conventional `_meta`),
	// MERGE it so we don't blow away preexisting metadata.
	nested := map[string]any{}
	if existing, ok := obj[metaKey].(map[string]any); ok {
		for k, v := range existing {
			nested[k] = v
		}
	}
	for k, v := range tags {
		nested[k] = v
	}
	obj[metaKey] = nested
}
