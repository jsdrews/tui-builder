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
// there's exactly one composer codepath. We translate the UnionOp
// into a synthetic cfg.DataSource{Type: "merge", ...} for the
// existing merge constructor; the resulting Source becomes this
// Pipeline's upstream and the Pipeline acts as a thin passthrough
// over it.
func newUnion(name string, children map[string]ds.Source, def *cfg.UnionOp) (*Pipeline, error) {
	// Translate the operator config into the wire shape NewMerge
	// expects. Identical field semantics — see cfg.UnionOp and the
	// merge source documentation.
	fake := &cfg.DataSource{
		Type:     "merge",
		Sources:  append([]string(nil), def.Sources...),
		TagField: def.TagField,
		Children: append([]cfg.MergeChild(nil), def.Children...),
		OnError:  def.OnError,
		MetaKey:  def.MetaKey,
	}
	merged, err := ds.NewMerge(fake, children)
	if err != nil {
		return nil, fmt.Errorf("union: %w", err)
	}
	return &Pipeline{name: name, upstream: merged}, nil
}
