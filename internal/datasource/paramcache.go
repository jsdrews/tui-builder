package datasource

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// ParamCacheDefaultSize is the entry cap when CacheSpec.Size is
// unset. 100 is enough for the "cursor drifts over a table of a few
// dozen rows re-visiting the same detail" pattern without ballooning
// memory for a lookup table that keeps growing.
const ParamCacheDefaultSize = 100

// ParamCache is a bounded LRU with per-entry TTL, keyed on a string
// hash of the params tuple. Concurrent-safe. Never caches errors — a
// failing fetch retries on the next call so a stale error doesn't
// lock a user out for the rest of the TTL. Uses single-flight so N
// concurrent callers with the same key issue one upstream load, not N.
//
// Not a ds.Source itself: the parameter-varying call sites (join
// lookup fan-out, cursor-driven detail refetch) know their params
// tuple at call time and drive the cache directly. Wrapping the
// leaf source would require plumbing the params through Fetch,
// which the interface doesn't carry.
type ParamCache struct {
	ttl  time.Duration
	size int
	// -1 → unbounded (paramsHash cardinality itself bounds memory)

	mu    sync.Mutex
	items map[string]*list.Element // paramsHash -> element in order
	order *list.List               // MRU at Front, LRU at Back

	// pending tracks in-flight fetches per paramsHash so N concurrent
	// callers with the same params issue one upstream fetch, not N —
	// the single-flight pattern. Without this, a cursor sweep across
	// a table that repeatedly hits the same detail row can pile up
	// duplicate in-flight requests before the first result lands.
	pending map[string]*inflight
}

type paramCacheEntry struct {
	key    string
	value  any
	filled time.Time
}

// inflight lets concurrent callers hitching to the same paramsHash
// wait on the same upstream fetch. done is closed once val + err
// have been written by the leading caller.
type inflight struct {
	done chan struct{}
	val  any
	err  error
}

// NewParamCache constructs an empty LRU from a *cfg.CacheSpec. Zero-
// value Size becomes ParamCacheDefaultSize; negative Size means
// unbounded. Nil spec (or empty TTL) yields (nil, error) — callers
// that want to run without caching should not call this.
func NewParamCache(spec *cfg.CacheSpec) (*ParamCache, error) {
	if spec == nil {
		return nil, fmt.Errorf("NewParamCache: nil spec")
	}
	ttl, err := time.ParseDuration(spec.TTL)
	if err != nil {
		return nil, fmt.Errorf("cache: invalid ttl %q: %w", spec.TTL, err)
	}
	size := spec.Size
	if size == 0 {
		size = ParamCacheDefaultSize
	}
	return &ParamCache{
		ttl:     ttl,
		size:    size,
		items:   map[string]*list.Element{},
		order:   list.New(),
		pending: map[string]*inflight{},
	}, nil
}

// FetchOrLoad returns the cached value for the given params tuple if
// present and fresh. Otherwise it invokes load exactly once per
// (params, in-flight window): concurrent callers with the same
// params block on the shared inflight and receive whatever the
// winning caller got. Errors are propagated but not cached — the
// next call retries.
func (c *ParamCache) FetchOrLoad(params map[string]string, load func() (any, error)) (any, error) {
	key := ParamsHash(params)
	if v, ok := c.get(key); ok {
		return v, nil
	}
	c.mu.Lock()
	if fl, ok := c.pending[key]; ok {
		c.mu.Unlock()
		<-fl.done
		return fl.val, fl.err
	}
	fl := &inflight{done: make(chan struct{})}
	c.pending[key] = fl
	c.mu.Unlock()

	val, err := load()

	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()

	if err == nil {
		c.put(key, val)
	}
	fl.val, fl.err = val, err
	close(fl.done)
	return val, err
}

// Len returns the current number of live entries. Exposed for tests
// and future stats reporting. Cheap; takes the mutex briefly.
func (c *ParamCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *ParamCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	ent := el.Value.(*paramCacheEntry)
	if time.Since(ent.filled) >= c.ttl {
		c.order.Remove(el)
		delete(c.items, key)
		return nil, false
	}
	c.order.MoveToFront(el)
	return ent.value, true
}

func (c *ParamCache) put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		ent := el.Value.(*paramCacheEntry)
		ent.value = value
		ent.filled = time.Now()
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&paramCacheEntry{key: key, value: value, filled: time.Now()})
	c.items[key] = el
	if c.size > 0 && c.order.Len() > c.size {
		back := c.order.Back()
		if back != nil {
			ent := back.Value.(*paramCacheEntry)
			c.order.Remove(back)
			delete(c.items, ent.key)
		}
	}
}

// ParamsHash produces a deterministic string key from a params map.
// Sorted keys, `=` and `\x00` separators, SHA-1 hex → 40 chars.
// Cryptographic strength is not required — we're just avoiding
// collisions between different (k=v) tuples. SHA-1 is cheap and its
// collision domain is more than enough for realistic caches.
func ParamsHash(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
		b.WriteByte(0)
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
