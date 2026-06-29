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
)

// newCompose builds a snapshot-only Pipeline that bundles N
// heterogeneous upstreams into a single object whose keys are the
// caller-chosen output names.
//
// Unlike union (which flattens N homogeneous iterables), compose
// preserves the separation of each child so a single addressable
// target can carry unrelated shapes — pods + deployments + services
// for a "fleet" pipeline, for instance. The output of Fetch is a
// map[string]any keyed by the output names.
//
// Subscribe returns ErrNotStreaming. "Compose of streams" is
// ambiguous — each child emits its own events; merging them into one
// composed event would need fan-in semantics (full snapshot per
// child event, or per-child delta) we haven't designed yet. For
// streaming consumers, subscribe to individual children directly.
func newCompose(name string, children map[string]ds.Source, def *cfg.Source) (*Pipeline, error) {
	parts := make([]composedChild, 0, len(def.Parts))
	for outKey, inputName := range def.Parts {
		src, ok := children[inputName]
		if !ok {
			return nil, fmt.Errorf("compose.parts.%s: child %q not resolved", outKey, inputName)
		}
		parts = append(parts, composedChild{outKey: outKey, name: inputName, src: src})
	}
	// Sort by output key so the fan-out and the resulting map
	// iteration in tests / wrangl output is deterministic.
	sort.Slice(parts, func(i, j int) bool { return parts[i].outKey < parts[j].outKey })

	cs := &composeSource{
		parts:   parts,
		onError: def.OnError,
	}
	return &Pipeline{
		name:             name,
		upstream:         cs,
		disableStreaming: true,
	}, nil
}

// composeSource implements ds.Source by fan-out-fetching every child
// in parallel and assembling the results into a map keyed by output
// name. Errors honour OnError: "" / "fail" aborts on any error;
// "skip" drops the failed child and only errors if every child
// failed.
type composeSource struct {
	parts   []composedChild
	onError string
}

type composedChild struct {
	outKey string
	name   string // upstream's name, for error messages
	src    ds.Source
}

func (c *composeSource) Refresh() time.Duration { return 0 }

func (c *composeSource) Fetch(ctx context.Context) (any, error) {
	type result struct {
		outKey string
		name   string
		data   any
		err    error
	}
	resCh := make(chan result, len(c.parts))
	var wg sync.WaitGroup
	for _, p := range c.parts {
		wg.Add(1)
		go func(p composedChild) {
			defer wg.Done()
			data, err := p.src.Fetch(ctx)
			resCh <- result{outKey: p.outKey, name: p.name, data: data, err: err}
		}(p)
	}
	wg.Wait()
	close(resCh)

	skip := c.onError == "skip"
	out := make(map[string]any, len(c.parts))
	var errs []string
	for r := range resCh {
		if r.err != nil {
			errs = append(errs, fmt.Sprintf("%s (%s): %v", r.outKey, r.name, r.err))
			if !skip {
				return nil, fmt.Errorf("compose: %s", errs[len(errs)-1])
			}
			continue
		}
		out[r.outKey] = r.data
	}
	// All children failed under skip — surface the union of errors.
	if skip && len(out) == 0 && len(errs) > 0 {
		sort.Strings(errs)
		return nil, errors.New("compose: " + strings.Join(errs, "; "))
	}
	return out, nil
}
