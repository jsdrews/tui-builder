package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// newCache builds a Pipeline that memoises its upstream's Fetch
// result for the configured TTL. Reads within the TTL return the
// cached snapshot; the first read after expiry re-fetches.
//
// Errors are deliberately NOT cached: a failing upstream is retried
// on the next call rather than returning a stale error for the rest
// of the TTL.
//
// Streaming passes through to the upstream unchanged — caching
// event streams isn't meaningful (events are incremental updates,
// not snapshots).
func newCache(name string, upstream ds.Source, def *cfg.CacheOp) (*Pipeline, error) {
	ttl, err := time.ParseDuration(def.TTL)
	if err != nil {
		return nil, fmt.Errorf("cache: invalid ttl %q: %w", def.TTL, err)
	}
	cs := &cacheSource{upstream: upstream, ttl: ttl}
	return &Pipeline{name: name, upstream: cs}, nil
}

// cacheSource wraps an upstream source with TTL-bounded Fetch
// memoisation. Thread-safe; concurrent readers serialise on the
// mutex but only one calls the upstream per TTL window (and only if
// the previous result expired).
type cacheSource struct {
	upstream ds.Source
	ttl      time.Duration

	mu       sync.Mutex
	cached   any
	cachedAt time.Time
	hasValue bool
}

func (c *cacheSource) Refresh() time.Duration { return c.upstream.Refresh() }

func (c *cacheSource) Fetch(ctx context.Context) (any, error) {
	c.mu.Lock()
	if c.hasValue && time.Since(c.cachedAt) < c.ttl {
		v := c.cached
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	// Release the lock for the actual fetch — concurrent waiters will
	// race here, but the next caller to acquire mu post-fetch will
	// see the freshly cached value and return it. The cost: a brief
	// window where multiple goroutines may hit the upstream
	// concurrently if they all observed a stale cache. For the
	// common case (sequential consumers within a tick) this is fine;
	// upgrade to single-flight if real workloads need stricter
	// dedup.
	data, err := c.upstream.Fetch(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cached = data
	c.cachedAt = time.Now()
	c.hasValue = true
	c.mu.Unlock()
	return data, nil
}

// Subscribe passes through to the upstream unchanged. Streaming
// events are inherently incremental; caching them doesn't fit the
// snapshot-TTL model. If the upstream doesn't stream, this returns
// ErrNotStreaming via the upstream — same fallback behaviour
// consumers already expect.
func (c *cacheSource) Subscribe(ctx context.Context) (<-chan ds.Event, error) {
	if streamer, ok := c.upstream.(ds.StreamingSource); ok {
		return streamer.Subscribe(ctx)
	}
	return nil, ds.ErrNotStreaming
}
