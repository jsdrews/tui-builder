package pipeline

import (
	"fmt"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// newUnion builds a Pipeline that composes N upstream iterables into
// a single union, optionally tagging each row with per-child
// metadata. Semantically identical to the merge SOURCE — same
// streaming-when-all-children-stream behavior, same on_error
// handling, same per-child tag injection under meta_key — but lives
// in the pipeline layer so children can be other pipelines, not just
// leaf sources.
//
// The implementation delegates to internal/datasource.NewMerge so
// there's exactly one composer codepath. Union and Merge share the
// same field shape on cfg.Source (Sources / Children / TagField /
// MetaKey / OnError) so we pass the source directly — the merge
// builder doesn't care that the Type field says "union".
func newUnion(name string, children map[string]ds.Source, def *cfg.Source) (*Pipeline, error) {
	merged, err := ds.NewMerge(def, children)
	if err != nil {
		return nil, fmt.Errorf("union: %w", err)
	}
	return &Pipeline{name: name, upstream: merged}, nil
}
