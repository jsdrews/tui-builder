// Package datasource fetches arbitrary data and hands it to bound
// components. Sources are construct-once, fetch-many — the same Source
// is reused across polling ticks.
package datasource

import (
	"context"
	"fmt"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Source fetches data on demand. Implementations are responsible for any
// per-fetch context (HTTP request, subprocess invocation) but should be
// safe to call concurrently — the caller usually serializes via tea.Cmd
// anyway.
type Source interface {
	Fetch(ctx context.Context) (any, error)
	// Refresh is the polling interval, or 0 for fetch-once.
	Refresh() time.Duration
}

// New constructs a Source from its config.
func New(d *cfg.DataSource) (Source, error) {
	switch d.Type {
	case "http":
		return newHTTP(d)
	}
	return nil, fmt.Errorf("unknown data source type %q", d.Type)
}
