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

// Build constructs every pipeline declared in cfg.Pipelines on top of
// the already-built source registry. Pipelines may reference each
// other; topological order is resolved recursively (cycles are
// rejected upstream by cfg.Config.Validate, so the recursion always
// terminates).
//
// Returns a Registry that consumers (TUI binding, wrangl CLI) can
// look names up against without caring whether they're sources or
// pipelines underneath.
// Build constructs every pipeline declared in cfg.Pipelines.
//
// Arguments:
//   - sources: live, already-built data sources (from ds.Build).
//   - sourceDefs: the raw cfg.DataSource map. The join operator needs
//     it to clone + bind + build per-row lookup sources. Other
//     operators ignore it. Pass c.DataSources from the same Config.
//     Nil is allowed when no pipeline uses join.
//   - defs: cfg.Pipelines from the same Config.
//   - boundParams: caller-resolved parameter values per pipeline (e.g.
//     from wrangl --param); pipelines without an entry (or with nil)
//     run with all-default / required-blocked params, which the
//     operator's expressions will see as either the declared default
//     or an empty string. Pass nil when no caller binding is
//     happening (TUI initial construction, tests that don't exercise
//     params).
func Build(sources map[string]ds.Source, sourceDefs map[string]*cfg.DataSource, defs map[string]*cfg.Pipeline, boundParams map[string]map[string]string) (*Registry, error) {
	reg := &Registry{
		sources:   sources,
		pipelines: make(map[string]*Pipeline, len(defs)),
	}
	var resolve func(name string) (ds.Source, error)
	resolve = func(name string) (ds.Source, error) {
		if p, ok := reg.pipelines[name]; ok {
			return p, nil
		}
		if s, ok := sources[name]; ok {
			return s, nil
		}
		def, ok := defs[name]
		if !ok {
			return nil, fmt.Errorf("pipeline %q references undefined name", name)
		}
		// Resolve every upstream the operator needs. Single-input
		// operators get a one-element map; union gets N children.
		upstreams, err := resolveAll(resolve, upstreamsOf(def))
		if err != nil {
			return nil, fmt.Errorf("pipelines.%s: %w", name, err)
		}

		// Resolve this pipeline's bound parameters against its schema
		// (apply defaults, check required, reject typos). Result is
		// the env value visible to operator expressions as `params.X`.
		params, err := cfg.ResolveParams(def.Parameters, boundParams[name])
		if err != nil {
			return nil, fmt.Errorf("pipelines.%s: %w", name, err)
		}

		// Construct the operator-specific pipeline.
		var p *Pipeline
		switch {
		case def.Filter != nil:
			p, err = newFilter(name, upstreams[def.Filter.From], def.Filter, params)
		case def.Project != nil:
			p, err = newProject(name, upstreams[def.Project.From], def.Project, params)
		case def.Derive != nil:
			p, err = newDerive(name, upstreams[def.Derive.From], def.Derive, params)
		case def.Sort != nil:
			p, err = newSort(name, upstreams[def.Sort.From], def.Sort, params)
		case def.Union != nil:
			p, err = newUnion(name, upstreams, def.Union)
		case def.Compose != nil:
			p, err = newCompose(name, upstreams, def.Compose)
		case def.Join != nil:
			p, err = newJoin(name, upstreams[def.Join.Driver.From], def.Join, sourceDefs)
		case def.Cache != nil:
			p, err = newCache(name, upstreams[def.Cache.From], def.Cache)
		default:
			// Passthrough — From was the upstream, no transformation.
			p = &Pipeline{name: name, upstream: upstreams[def.From]}
		}
		if err != nil {
			return nil, fmt.Errorf("pipelines.%s: %w", name, err)
		}
		reg.pipelines[name] = p
		return p, nil
	}
	for name := range defs {
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

// upstreamsOf returns every source / pipeline an operator reads
// from. Single-input operators (passthrough, filter, project,
// derive, sort) return a one-element slice; multi-input operators
// (union) return the full child list. Build uses this to resolve
// dependencies in topological order.
func upstreamsOf(def *cfg.Pipeline) []string {
	switch {
	case def.Filter != nil:
		return []string{def.Filter.From}
	case def.Project != nil:
		return []string{def.Project.From}
	case def.Derive != nil:
		return []string{def.Derive.From}
	case def.Sort != nil:
		return []string{def.Sort.From}
	case def.Union != nil:
		out := make([]string, 0, len(def.Union.Sources)+len(def.Union.Children))
		out = append(out, def.Union.Sources...)
		for _, ch := range def.Union.Children {
			out = append(out, ch.Source)
		}
		return out
	case def.Compose != nil:
		out := make([]string, 0, len(def.Compose.Parts))
		for _, in := range def.Compose.Parts {
			out = append(out, in)
		}
		return out
	case def.Join != nil:
		// Only the driver is an upstream the operator wraps as a
		// Source. Lookups are re-invoked per row via cfg+BindParams
		// inside joinSource — they're resolved from the cfg, not
		// from the pipeline registry.
		return []string{def.Join.Driver.From}
	case def.Cache != nil:
		return []string{def.Cache.From}
	default:
		return []string{def.From}
	}
}
