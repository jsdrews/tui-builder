package pipeline

import (
	"context"
	"fmt"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// Pipeline is the data layer's named, addressable unit. It wraps a
// single upstream (source or other pipeline) and either passes Fetch
// / Subscribe through unchanged (passthrough) or applies a per-item
// transformation declared by an operator (filter today; project /
// derive / sort to come).
//
// Pipeline satisfies datasource.Source / StreamingSource by
// delegation, so the existing binding code in internal/build doesn't
// need to special-case pipelines vs sources.
//
// Operator hooks:
//   - transformSnapshot rewrites the value returned by Fetch (one-shot
//     and polled). nil = identity (passthrough).
//   - transformEvent rewrites or drops a single event from Subscribe.
//     Returns the rewritten event, a `keep` flag (false drops the
//     event silently), and an error to surface as a stream error.
//     nil = identity.
//
// Both hooks default to identity so the zero-value Pipeline is the
// passthrough we shipped first; operators just override them.
type Pipeline struct {
	name     string
	upstream ds.Source

	transformSnapshot func(any) (any, error)
	transformEvent    func(ds.Event) (ds.Event, bool, error)

	// disableStreaming makes Subscribe return ErrNotStreaming
	// unconditionally. Used by snapshot-only operators (sort) where
	// per-event semantics don't apply — consumers fall back to
	// polling via Fetch.
	disableStreaming bool
}

// Name returns the config-declared name. Used by introspection
// (--list, --explain).
func (p *Pipeline) Name() string { return p.name }

// Refresh delegates to the upstream. A passthrough pipeline takes
// its lifecycle from whatever it wraps.
func (p *Pipeline) Refresh() time.Duration { return p.upstream.Refresh() }

// Fetch delegates to the upstream's Fetch, then applies the
// pipeline's snapshot transform (if any). Passthrough pipelines have
// no transform and return the upstream value unchanged.
func (p *Pipeline) Fetch(ctx context.Context) (any, error) {
	data, err := p.upstream.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	if p.transformSnapshot == nil {
		return data, nil
	}
	return p.transformSnapshot(data)
}

// Subscribe delegates when the upstream is a StreamingSource.
// Pipelines wrapping non-streaming sources return ErrNotStreaming so
// the screen falls back to polling, same convention as other
// composers.
//
// When a transformEvent hook is set, every upstream event is passed
// through it: kept events forward to consumers, dropped events
// silently disappear, errors surface as Event{Err}. Without a hook,
// events forward unchanged (the passthrough case).
func (p *Pipeline) Subscribe(ctx context.Context) (<-chan ds.Event, error) {
	if p.disableStreaming {
		return nil, ds.ErrNotStreaming
	}
	streamer, ok := p.upstream.(ds.StreamingSource)
	if !ok {
		return nil, ds.ErrNotStreaming
	}
	upCh, err := streamer.Subscribe(ctx)
	if err != nil {
		return nil, err
	}
	if p.transformEvent == nil {
		return upCh, nil
	}
	out := make(chan ds.Event, 64)
	go func() {
		defer close(out)
		for ev := range upCh {
			transformed, keep, err := p.transformEvent(ev)
			if err != nil {
				select {
				case out <- ds.Event{Err: err}:
				case <-ctx.Done():
					return
				}
				continue
			}
			if !keep {
				continue
			}
			select {
			case out <- transformed:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Registry holds built pipelines indexed by name, alongside the live
// sources they sit on top of. Callers (screen.New, wrangl main) look
// up by name and treat sources and pipelines uniformly via the
// SourceLike interface.
type Registry struct {
	sources   map[string]ds.Source
	pipelines map[string]*Pipeline
}

// Sources returns the underlying source registry. Pipelines wrap
// these; both kinds are addressable by name via Get.
func (r *Registry) Sources() map[string]ds.Source { return r.sources }

// Pipelines returns the built pipeline registry.
func (r *Registry) Pipelines() map[string]*Pipeline { return r.pipelines }

// Get resolves a name to its live ds.Source — pipeline first (named
// pipelines shadow sources of the same name, though the config
// validator prevents that collision), then source. Returns nil if
// the name isn't defined.
func (r *Registry) Get(name string) ds.Source {
	if p, ok := r.pipelines[name]; ok {
		return p
	}
	if s, ok := r.sources[name]; ok {
		return s
	}
	return nil
}

// Names returns every defined name (sources + pipelines), sorted.
// Used by --list and friends.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.sources)+len(r.pipelines))
	for name := range r.sources {
		out = append(out, name)
	}
	for name := range r.pipelines {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

// Kind reports what a name resolves to in the registry. "" if missing.
func (r *Registry) Kind(name string) string {
	if _, ok := r.pipelines[name]; ok {
		return "pipeline"
	}
	if _, ok := r.sources[name]; ok {
		return "source"
	}
	return ""
}

// Build constructs every entry in the unified data.sources map.
// Walks the graph topologically (cycle detection happens at
// validate-time so the recursive resolve can't loop) and dispatches
// per-kind via the source's Type string:
//
//   - Leaf kinds → ds.BuildLeaf, with merge children pre-resolved.
//   - Operator kinds → the per-operator constructor in this package
//     (newFilter, newProject, …), with upstreams resolved from the
//     same sources map.
//
// Arguments:
//   - prebuilt: pre-constructed ds.Source values registered by name
//     before resolution. Production callers pass nil. Test fixtures
//     pass pre-built fakeSource values so operators wired on top of
//     them don't need to go through ds.BuildLeaf.
//   - sources: the unified sources map. Resolved recursively;
//     operator kinds dispatch through the per-op constructor.
//   - boundParams: caller-bound parameter values keyed by source
//     name; entries without a key in this map run with all-default
//     / required-blocked params.
//
// The returned Registry exposes one Get(name) lookup that resolves
// any entry — leaves and operators share the namespace.
func Build(prebuilt map[string]ds.Source, sources map[string]*cfg.Source, boundParams map[string]map[string]string) (*Registry, error) {
	reg := &Registry{
		sources:   make(map[string]ds.Source, len(sources)+len(prebuilt)),
		pipelines: make(map[string]*Pipeline),
	}
	for name, s := range prebuilt {
		reg.sources[name] = s
	}

	var resolve func(name string) (ds.Source, error)
	resolve = func(name string) (ds.Source, error) {
		if p, ok := reg.pipelines[name]; ok {
			return p, nil
		}
		if s, ok := reg.sources[name]; ok {
			return s, nil
		}
		s, ok := sources[name]
		if !ok {
			return nil, fmt.Errorf("entry %q not defined", name)
		}

		// Resolve every upstream this source references. Leaves
		// usually have none (merge being the exception); operators
		// have one or more (driver + lookups, parts, etc.).
		upstreams, err := resolveAll(resolve, s.Upstreams())
		if err != nil {
			return nil, fmt.Errorf("data.sources.%s: %w", name, err)
		}

		// Leaf branch — hand off to internal/datasource. Merge gets
		// its children from upstreams; the other leaves ignore the
		// map.
		if s.IsLeaf() {
			built, err := ds.BuildLeaf(s, upstreams)
			if err != nil {
				return nil, fmt.Errorf("data.sources.%s: %w", name, err)
			}
			reg.sources[name] = built
			return built, nil
		}

		// Operator branch — resolve parameters, then dispatch by
		// Type to the matching constructor.
		params, err := cfg.ResolveParams(s.Parameters, boundParams[name])
		if err != nil {
			return nil, fmt.Errorf("data.sources.%s: %w", name, err)
		}

		var p *Pipeline
		switch s.Type {
		case "passthrough":
			p = &Pipeline{name: name, upstream: upstreams[s.From]}
		case "filter":
			p, err = newFilter(name, upstreams[s.From], s, params)
		case "project":
			p, err = newProject(name, upstreams[s.From], s, params)
		case "derive":
			p, err = newDerive(name, upstreams[s.From], s, params)
		case "sort":
			p, err = newSort(name, upstreams[s.From], s, params)
		case "union":
			p, err = newUnion(name, upstreams, s)
		case "compose":
			p, err = newCompose(name, upstreams, s)
		case "join":
			p, err = newJoin(name, upstreams[s.Driver.From], s, sources)
		case "cache":
			p, err = newCache(name, upstreams[s.From], s)
		default:
			return nil, fmt.Errorf("data.sources.%s: unknown operator kind %q", name, s.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("data.sources.%s: %w", name, err)
		}
		reg.pipelines[name] = p
		return p, nil
	}

	for name := range sources {
		if _, err := resolve(name); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// resolveAll runs resolve over a list of upstream names, deduplicated,
// and returns a map indexed by name. Used by Build to satisfy
// single- and multi-input operators with one code path.
func resolveAll(resolve func(string) (ds.Source, error), names []string) (map[string]ds.Source, error) {
	out := make(map[string]ds.Source, len(names))
	for _, n := range names {
		if _, ok := out[n]; ok {
			continue
		}
		s, err := resolve(n)
		if err != nil {
			return nil, err
		}
		out[n] = s
	}
	return out, nil
}
