package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// fileSource reads a file from disk on each fetch. `refresh: 0`
// (default) reads once on screen activate; a positive `refresh:`
// re-reads on every tick — useful for fixtures that get re-generated
// out-of-band (a `kubectl get -o json > snapshot.json` cronjob, a
// teammate's lab notebook output, a generated metrics file).
//
// Use this for local testing without a network round-trip, demo
// configs that ship with their data, or composing a stable fixture
// with a live source via `merge`.
type fileSource struct {
	path    string
	format  string
	root    string
	refresh time.Duration
}

func newFile(d *cfg.DataSource) (Source, error) {
	var refresh time.Duration
	if d.Refresh != "" {
		r, err := time.ParseDuration(d.Refresh)
		if err != nil {
			return nil, fmt.Errorf("refresh: %w", err)
		}
		refresh = r
	}
	return &fileSource{
		path:    d.Path,
		format:  d.Format,
		root:    d.Root,
		refresh: refresh,
	}, nil
}

func (s *fileSource) Refresh() time.Duration { return s.refresh }

func (s *fileSource) Fetch(_ context.Context) (any, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	if s.format == "text" {
		return string(raw), nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s as json: %w", s.path, err)
	}
	return applyRoot(out, s.root), nil
}
