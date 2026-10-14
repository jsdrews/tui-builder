package config

// `retry:`, conditional GET and `max_body:` on an http source. See
// plans/http-source.md §8.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RetrySpec retries a request that failed in a way worth trying again.
// Opt-in: a source without retry: tries each request once.
type RetrySpec struct {
	// Max is the number of attempts, the first included. Default 3.
	Max int `yaml:"max,omitempty"`
	// Backoff is the base of the exponential curve. Default 500ms.
	Backoff string `yaml:"backoff,omitempty"`
	// MaxBackoff caps the curve. Default 30s.
	MaxBackoff string `yaml:"max_backoff,omitempty"`
	// MaxWait is the longest server-requested wait (Retry-After, a
	// rate-limit reset) to sit out; a longer one fails at once. Default 60s.
	MaxWait string `yaml:"max_wait,omitempty"`
	// On replaces the retried statuses (408, 429, 500, 502, 503, 504).
	// Network errors and timeouts are always retried; 401 never is.
	On []int `yaml:"on,omitempty"`
}

// Retry defaults.
const (
	DefaultRetryMax        = 3
	DefaultRetryBackoff    = 500 * time.Millisecond
	DefaultRetryMaxBackoff = 30 * time.Second
	DefaultRetryMaxWait    = 60 * time.Second
	DefaultMaxBody         = 50 << 20
)

// DefaultRetryOn is the statuses retried unless `on:` says otherwise.
var DefaultRetryOn = []int{408, 429, 500, 502, 503, 504}

func (r *RetrySpec) Clone() *RetrySpec {
	if r == nil {
		return nil
	}
	out := *r
	out.On = append([]int(nil), r.On...)
	return &out
}

func (r *RetrySpec) validate(path string) error {
	if r.Max < 0 {
		return fmt.Errorf("%s.max: must be >= 1 (attempts, the first included)", path)
	}
	for field, v := range map[string]string{"backoff": r.Backoff, "max_backoff": r.MaxBackoff, "max_wait": r.MaxWait} {
		if v == "" {
			continue
		}
		if d, err := time.ParseDuration(v); err != nil || d < 0 {
			return fmt.Errorf("%s.%s: invalid duration %q", path, field, v)
		}
	}
	for i, code := range r.On {
		switch {
		case code < 100 || code > 599:
			return fmt.Errorf("%s.on[%d]: %d isn't an HTTP status", path, i, code)
		case code == 401:
			return fmt.Errorf("%s.on[%d]: 401 is never retried — a bad credential stays bad (oauth2 refetches its token on a 401 by itself)", path, i)
		case code/100 == 2 || code/100 == 3:
			return fmt.Errorf("%s.on[%d]: %d isn't a failure", path, i, code)
		}
	}
	return nil
}

// ParseSize reads a size: a number of bytes, or one with a KB, MB or GB
// suffix (binary: 1MB is 1024 KB).
func ParseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(t, u.suffix) {
			t, mult = strings.TrimSpace(strings.TrimSuffix(t, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q (want e.g. 50MB, 512KB)", s)
	}
	return n * mult, nil
}

// validateResilience checks retry:, conditional: and max_body:.
func (s *Source) validateResilience(path string) error {
	if s.Type != "http" {
		for field, set := range map[string]bool{"retry": s.Retry != nil, "conditional": s.Conditional != nil, "max_body": s.MaxBody != ""} {
			if set {
				return fmt.Errorf("%s.%s: only applies to type: http", path, field)
			}
		}
		return nil
	}
	if s.Retry != nil {
		if err := s.Retry.validate(path + ".retry"); err != nil {
			return err
		}
	}
	if s.MaxBody != "" {
		if _, err := ParseSize(s.MaxBody); err != nil {
			return fmt.Errorf("%s.max_body: %w", path, err)
		}
	}
	return nil
}
