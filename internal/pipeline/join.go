package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// newJoin builds a snapshot-only Pipeline that enriches each row from
// the driver with results fetched per-row from one or more lookups.
//
// Execution shape:
//   - Fetch the driver once → get []any of rows.
//   - For each row, compute lookup params by evaluating the `on:`
//     expressions against the row.
//   - For each lookup (in parallel within a row), clone the lookup
//     source cfg, BindParams with the row's computed values, build a
//     concrete ds.Source from the bound cfg, Fetch.
//   - Assemble per-row output by Emit shape (separate / merged).
//   - On lookup error: fail (abort whole join) or skip (drop row).
//
// We don't subscribe — Subscribe returns ErrNotStreaming. Joining
// over a stream needs windowing we haven't designed yet.
//
// We don't cache — each driver row triggers fresh lookup fetches. An
// LRU keyed on the params tuple is a natural follow-up when N gets
// large enough to hurt; v1 favours simplicity.
//
// Lookups must be leaf-kind entries (not operator pipelines) with
// declared `parameters:`. The validator enforces. The reason: joins
// re-invoke lookups per row with new params, which leaf-source
// BindParams supports but operator pipelines don't yet expose.
func newJoin(name string, driver ds.Source, def *cfg.Source, lookupSources map[string]*cfg.Source) (*Pipeline, error) {
	// Pre-compile every lookup's `on:` expressions and snapshot its
	// *cfg.Source so per-row fetches don't re-parse or hit the
	// shared cfg.
	lookups := make([]preparedLookup, 0, len(def.Lookups))
	names := make([]string, 0, len(def.Lookups))
	for n := range def.Lookups {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic per-row fan-out + output ordering
	for _, lname := range names {
		look := def.Lookups[lname]
		src, ok := lookupSources[look.From]
		if !ok {
			return nil, fmt.Errorf("join.lookups.%s: source %q not defined", lname, look.From)
		}
		progs := make(map[string]*expr.Program, len(look.On))
		for paramName, e := range look.On {
			prog, err := expr.Compile(e)
			if err != nil {
				return nil, fmt.Errorf("join.lookups.%s.on.%s: %w", lname, paramName, err)
			}
			progs[paramName] = prog
		}
		lookups = append(lookups, preparedLookup{
			name:    lname,
			source:  src,
			onProgs: progs,
		})
	}

	js := &joinSource{
		driver:  driver,
		lookups: lookups,
		emit:    def.Emit,
		onError: def.OnError,
	}
	return &Pipeline{
		name:             name,
		upstream:         js,
		disableStreaming: true,
	}, nil
}

// preparedLookup pre-bakes everything reusable across rows: the
// lookup's name, the cfg.Source template (we clone per row before
// BindParams mutates it), and the compiled `on:` expressions.
type preparedLookup struct {
	name    string
	source  *cfg.Source
	onProgs map[string]*expr.Program
}

// joinSource implements ds.Source. Fetch fans out the per-row lookup
// invocations and assembles the output.
type joinSource struct {
	driver  ds.Source
	lookups []preparedLookup
	emit    string
	onError string
}

func (j *joinSource) Refresh() time.Duration { return 0 }

func (j *joinSource) Fetch(ctx context.Context) (any, error) {
	driverOut, err := j.driver.Fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("join driver: %w", err)
	}
	rows, ok := driverOut.([]any)
	if !ok {
		// Single-object / scalar driver — wrap as a one-element slice
		// so the rest of the join logic stays uniform. Nil → empty.
		if driverOut == nil {
			return []any{}, nil
		}
		rows = []any{driverOut}
	}

	skip := j.onError == "skip"
	out := make([]any, 0, len(rows))
	for i, row := range rows {
		enriched, err := j.enrichRow(ctx, row)
		if err != nil {
			if skip {
				continue
			}
			return nil, fmt.Errorf("join row %d: %w", i, err)
		}
		out = append(out, enriched)
	}
	return out, nil
}

// enrichRow fans out all of this row's lookups in parallel, collects
// the results, and assembles them into the output shape dictated by
// Emit.
func (j *joinSource) enrichRow(ctx context.Context, row any) (any, error) {
	type result struct {
		name string
		data any
		err  error
	}
	resCh := make(chan result, len(j.lookups))
	var wg sync.WaitGroup
	for _, look := range j.lookups {
		wg.Add(1)
		go func(look preparedLookup) {
			defer wg.Done()
			data, err := j.runLookup(ctx, look, row)
			resCh <- result{name: look.name, data: data, err: err}
		}(look)
	}
	wg.Wait()
	close(resCh)

	bucket := make(map[string]any, len(j.lookups))
	var firstErr error
	for r := range resCh {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("lookup %s: %w", r.name, r.err)
			}
			continue
		}
		bucket[r.name] = r.data
	}
	if firstErr != nil {
		return nil, firstErr
	}

	switch j.emit {
	case "merged":
		return mergeRowLookups(row, bucket)
	default: // "" / "separate"
		out := make(map[string]any, len(bucket)+1)
		out["row"] = row
		for k, v := range bucket {
			out[k] = v
		}
		return out, nil
	}
}

// runLookup evaluates the lookup's `on:` expressions against the row,
// clones the lookup source's cfg, binds the resulting params, builds
// a concrete Source from the bound cfg, and fetches.
func (j *joinSource) runLookup(ctx context.Context, look preparedLookup, row any) (any, error) {
	params := make(map[string]string, len(look.onProgs))
	for paramName, prog := range look.onProgs {
		v, err := expr.Eval(prog, row)
		if err != nil {
			return nil, fmt.Errorf("on.%s: %w", paramName, err)
		}
		params[paramName] = fmt.Sprint(v)
	}
	cloned := look.source.Clone()
	if err := cloned.BindParams(params); err != nil {
		return nil, fmt.Errorf("bind: %w", err)
	}
	src, err := ds.BuildLeaf(cloned, nil)
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	return src.Fetch(ctx)
}

// mergeRowLookups merges every lookup result into the driver row (per
// Emit: merged). Both sides must be map-shaped; non-map values
// produce an error so the user notices the shape mismatch rather
// than silently getting back a `row` key.
func mergeRowLookups(row any, lookups map[string]any) (any, error) {
	base, ok := row.(map[string]any)
	if !ok {
		return nil, errors.New("emit: merged requires the driver row to be a map")
	}
	out := make(map[string]any, len(base)+len(lookups))
	for k, v := range base {
		out[k] = v
	}
	for lookName, lookVal := range lookups {
		lm, ok := lookVal.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("emit: merged requires lookup %q to return a map (got %T)", lookName, lookVal)
		}
		for k, v := range lm {
			if _, exists := out[k]; exists {
				// Mirror derive's collision policy: later wins, but
				// surface via a namespaced key so the user can spot
				// the clobber instead of silently losing data.
				out[lookName+"_"+k] = v
				continue
			}
			out[k] = v
		}
	}
	return out, nil
}

// suppress unused-imports warning when the file is being edited in
// isolation; the real implementation above uses every import.
var _ = strings.TrimSpace
