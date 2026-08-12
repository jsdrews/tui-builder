package datasource

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func TestParamCacheHitReturnsCachedValue(t *testing.T) {
	c, err := NewParamCache(&cfg.CacheSpec{TTL: "10m"})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	params := map[string]string{"id": "42"}
	load := func() (any, error) {
		atomic.AddInt32(&calls, 1)
		return "v", nil
	}
	if v, err := c.FetchOrLoad(params, load); err != nil || v != "v" {
		t.Fatalf("first: (%v, %v)", v, err)
	}
	if v, err := c.FetchOrLoad(params, load); err != nil || v != "v" {
		t.Fatalf("second: (%v, %v)", v, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("want 1 upstream call, got %d", calls)
	}
}

func TestParamCacheDistinctParamsMissSeparately(t *testing.T) {
	c, err := NewParamCache(&cfg.CacheSpec{TTL: "10m"})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	load := func() (any, error) {
		atomic.AddInt32(&calls, 1)
		return "v", nil
	}
	c.FetchOrLoad(map[string]string{"id": "1"}, load)
	c.FetchOrLoad(map[string]string{"id": "2"}, load)
	c.FetchOrLoad(map[string]string{"id": "1"}, load)
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("want 2 upstream calls (two distinct keys), got %d", calls)
	}
}

func TestParamCacheExpiryEvictsAndReloads(t *testing.T) {
	c, err := NewParamCache(&cfg.CacheSpec{TTL: "5ms"})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	load := func() (any, error) {
		atomic.AddInt32(&calls, 1)
		return "v", nil
	}
	params := map[string]string{"id": "1"}
	c.FetchOrLoad(params, load)
	time.Sleep(15 * time.Millisecond)
	c.FetchOrLoad(params, load)
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("want 2 upstream calls (miss after TTL), got %d", calls)
	}
}

func TestParamCacheErrorsNotCached(t *testing.T) {
	c, err := NewParamCache(&cfg.CacheSpec{TTL: "10m"})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	sentinel := errors.New("boom")
	load := func() (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, sentinel
	}
	params := map[string]string{"id": "1"}
	if _, err := c.FetchOrLoad(params, load); err != sentinel {
		t.Fatalf("want sentinel error, got %v", err)
	}
	if _, err := c.FetchOrLoad(params, load); err != sentinel {
		t.Fatalf("want sentinel again (not cached), got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("want 2 upstream calls (errors retry), got %d", calls)
	}
}

func TestParamCacheLRUEvictsOldest(t *testing.T) {
	c, err := NewParamCache(&cfg.CacheSpec{TTL: "10m", Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	load := func() (any, error) {
		atomic.AddInt32(&calls, 1)
		return "v", nil
	}
	c.FetchOrLoad(map[string]string{"id": "1"}, load)
	c.FetchOrLoad(map[string]string{"id": "2"}, load)
	c.FetchOrLoad(map[string]string{"id": "3"}, load) // evicts "1"
	c.FetchOrLoad(map[string]string{"id": "1"}, load) // miss again
	if atomic.LoadInt32(&calls) != 4 {
		t.Errorf("want 4 upstream calls (LRU evicted 1), got %d", calls)
	}
	if c.Len() != 2 {
		t.Errorf("want 2 live entries, got %d", c.Len())
	}
}

// TestParamCacheSingleFlightCollapsesConcurrent covers the "N driver
// rows land on the same params simultaneously" pattern — the join
// operator's biggest win. Only one upstream call should fire even if
// M callers race in with the same key before the first completes.
func TestParamCacheSingleFlightCollapsesConcurrent(t *testing.T) {
	c, err := NewParamCache(&cfg.CacheSpec{TTL: "10m"})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	release := make(chan struct{})
	load := func() (any, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return "v", nil
	}
	var wg sync.WaitGroup
	params := map[string]string{"id": "1"}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.FetchOrLoad(params, load)
		}()
	}
	// Give goroutines time to pile onto the same key.
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("want 1 upstream call (single-flight), got %d", got)
	}
}

func TestParamsHashDeterministicAcrossKeyOrder(t *testing.T) {
	a := ParamsHash(map[string]string{"x": "1", "y": "2"})
	b := ParamsHash(map[string]string{"y": "2", "x": "1"})
	if a != b {
		t.Errorf("hash should be order-independent, got %q vs %q", a, b)
	}
}

func TestParamsHashDistinguishesDifferentValues(t *testing.T) {
	a := ParamsHash(map[string]string{"x": "1"})
	b := ParamsHash(map[string]string{"x": "2"})
	if a == b {
		t.Errorf("hashes for distinct values should differ, both are %q", a)
	}
}
