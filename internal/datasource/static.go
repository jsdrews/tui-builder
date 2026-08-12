package datasource

import (
	"context"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// staticSource is the inline-data source. Holds whatever shape the
// YAML `data:` field deserialized into — list of objects, list of
// scalars, single object, single scalar — and returns it unchanged
// from Fetch.
//
// One-shot lifecycle: no refresh, no streaming. Useful as:
//   - Fixtures for pipeline development without network.
//   - Lookup tables (region codes → names, status codes → labels).
//   - Demo data when offline.
//   - Test scaffolds for filter / project / sort against a known set.
type staticSource struct {
	data any
}

func newStatic(d *cfg.Source) (Source, error) {
	// applyRoot lets `root:` slice into nested YAML the same way it
	// does for http / exec / file responses. Useful when the user
	// declares a wrapper object inline and wants the iterable bucket
	// to come out directly.
	return &staticSource{data: applyRoot(d.Data, d.Root)}, nil
}

func (s *staticSource) Fetch(_ context.Context) (any, error) { return s.data, nil }
func (s *staticSource) Refresh() time.Duration               { return 0 }
