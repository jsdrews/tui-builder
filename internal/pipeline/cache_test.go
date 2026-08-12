package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// countingSource is a fakeSource variant that tracks how many times
// Fetch has been called. Used to assert caching actually elides
// upstream calls.
type countingSource struct {
	data  any
	err   error
	calls int64
}

func (c *countingSource) Fetch(_ context.Context) (any, error) {
	atomic.AddInt64(&c.calls, 1)
	return c.data, c.err
}
func (c *countingSource) Refresh() time.Duration { return 0 }

func TestCacheHitsWithinTTL(t *testing.T) {
	up := &countingSource{data: "snapshot"}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"cached": cfg.NewEntry(&cfg.Source{Type: "cache", From: "src", TTL: "1m"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		got, err := reg.Get("cached").Fetch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got != "snapshot" {
			t.Errorf("call %d: got %v, want snapshot", i, got)
		}
	}
	if c := atomic.LoadInt64(&up.calls); c != 1 {
		t.Errorf("upstream Fetch should be called once across 5 cached reads; got %d", c)
	}
}

func TestCacheMissesAfterTTL(t *testing.T) {
	up := &countingSource{data: "v"}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			// Tight TTL so the test can observe the boundary without
			// waiting long.
			"cached": cfg.NewEntry(&cfg.Source{Type: "cache", From: "src", TTL: "50ms"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := reg.Get("cached").Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Get("cached").Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	// After two quick calls, only one upstream Fetch.
	if c := atomic.LoadInt64(&up.calls); c != 1 {
		t.Fatalf("expected 1 upstream call before TTL expiry, got %d", c)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := reg.Get("cached").Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if c := atomic.LoadInt64(&up.calls); c != 2 {
		t.Errorf("expected 2 upstream calls after TTL expiry, got %d", c)
	}
}

func TestCacheDoesNotCacheErrors(t *testing.T) {
	up := &countingSource{err: errors.New("boom")}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"cached": cfg.NewEntry(&cfg.Source{Type: "cache", From: "src", TTL: "1m"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// First call errors; cache stays empty (no successful snapshot).
	if _, err := reg.Get("cached").Fetch(ctx); err == nil {
		t.Errorf("expected upstream error to propagate")
	}
	// Recover.
	up.err = nil
	up.data = "ok now"
	got, err := reg.Get("cached").Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok now" {
		t.Errorf("after error recovery, expected fresh value; got %v", got)
	}
	if c := atomic.LoadInt64(&up.calls); c != 2 {
		t.Errorf("expected 2 upstream calls (one failed, one recovered); got %d", c)
	}
}

func TestCacheValidatorRejectsMissingTTL(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"src": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),

		"bad": cfg.NewEntry(&cfg.Source{Type: "cache", From: "src"})}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"x": {Type: "list", Items: []string{"x"}},
	},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject missing ttl")
	}
}

func TestCacheValidatorRejectsBadTTL(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"src": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),

		"bad": cfg.NewEntry(&cfg.Source{Type: "cache", From: "src", TTL: "forever"})}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"x": {Type: "list", Items: []string{"x"}},
	},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject malformed ttl")
	}
}

func TestCacheUnionDedup(t *testing.T) {
	// The motivating case: union of two pipelines that both feed from
	// the same source. Caching the source means the union fetches it
	// once even though TWO children would otherwise pull it.
	up := &countingSource{data: []any{
		map[string]any{"n": "x"},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"cached_src": cfg.NewEntry(&cfg.Source{Type: "cache", From: "src", TTL: "1m"}),
			"a":          cfg.NewEntry(&cfg.Source{Type: "passthrough", From: "cached_src"}),
			"b":          cfg.NewEntry(&cfg.Source{Type: "passthrough", From: "cached_src"}),
			"both":       cfg.NewEntry(&cfg.Source{Type: "union", Sources: []string{"a", "b"}, TagField: "via"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("both").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if items := got.([]any); len(items) != 2 {
		t.Errorf("union of [a, b] over cached source: want 2 items (1 from each child), got %d", len(items))
	}
	if c := atomic.LoadInt64(&up.calls); c != 1 {
		t.Errorf("cached upstream should be called exactly once across union's fan-out; got %d", c)
	}
}
