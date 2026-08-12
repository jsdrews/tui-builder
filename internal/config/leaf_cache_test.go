package config

import (
	"strings"
	"testing"
)

// Tests for the per-leaf `cache:` field. Feature G's config surface —
// join lookup + future cursor-driven refetch both drive param-aware
// LRU caches. `cache: {ttl: ..., size: ...}` on a leaf source is the
// user-visible knob.

func TestValidateLeafCacheAcceptsValidTTL(t *testing.T) {
	s := &Source{
		Type:  "http",
		URL:   "http://x",
		Cache: &CacheSpec{TTL: "30s", Size: 100},
	}
	if err := s.Validate("data.sources.x"); err != nil {
		t.Errorf("valid cache spec should pass, got %v", err)
	}
}

func TestValidateLeafCacheRejectsMissingTTL(t *testing.T) {
	s := &Source{
		Type:  "http",
		URL:   "http://x",
		Cache: &CacheSpec{}, // no TTL
	}
	err := s.Validate("data.sources.x")
	if err == nil || !strings.Contains(err.Error(), "cache: `ttl:` is required") {
		t.Errorf("want ttl-required error, got %v", err)
	}
}

func TestValidateLeafCacheRejectsBadTTL(t *testing.T) {
	s := &Source{
		Type:  "http",
		URL:   "http://x",
		Cache: &CacheSpec{TTL: "not-a-duration"},
	}
	err := s.Validate("data.sources.x")
	if err == nil || !strings.Contains(err.Error(), "invalid ttl") {
		t.Errorf("want invalid-ttl error, got %v", err)
	}
}
