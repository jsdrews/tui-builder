package datasource

// Retries, rate-limit waits and the response size cap: the parts of
// sending a request that decide whether to send it again. See
// plans/http-source.md §8.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Notify, when set, receives a source's passing notices: a retry, a
// walk stopped early. tui-builder posts them to the output console;
// wrangl prints them to stderr. Nil drops them.
var Notify func(msg string)

func notify(format string, args ...any) {
	if Notify != nil {
		Notify(fmt.Sprintf(format, args...))
	}
}

// retryPolicy is a source's resolved retry: block.
type retryPolicy struct {
	max        int
	backoff    time.Duration
	maxBackoff time.Duration
	maxWait    time.Duration
	on         map[int]bool
}

func newRetryPolicy(r *cfg.RetrySpec) *retryPolicy {
	if r == nil {
		return nil
	}
	p := &retryPolicy{
		max:        r.Max,
		backoff:    duration(r.Backoff, cfg.DefaultRetryBackoff),
		maxBackoff: duration(r.MaxBackoff, cfg.DefaultRetryMaxBackoff),
		maxWait:    duration(r.MaxWait, cfg.DefaultRetryMaxWait),
		on:         map[int]bool{},
	}
	if p.max == 0 {
		p.max = cfg.DefaultRetryMax
	}
	on := r.On
	if len(on) == 0 {
		on = cfg.DefaultRetryOn
	}
	for _, code := range on {
		p.on[code] = true
	}
	return p
}

func duration(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && s != "" {
		return d
	}
	return def
}

// send issues one request against url with body, retrying per retry:.
// Each attempt gets the source's whole timeout; the waits between them
// don't count against it.
func (s *httpSource) send(ctx context.Context, url, body string) (*response, error) {
	return s.sendWith(ctx, url, body, nil)
}

func (s *httpSource) sendWith(ctx context.Context, url, body string, extra http.Header) (*response, error) {
	for attempt := 1; ; attempt++ {
		r, err := s.sendOnce(ctx, url, body, extra)
		p := s.retry
		if p == nil || ctx.Err() != nil {
			return r, err
		}
		retry, status := p.retryable(r, err)
		if !retry || attempt >= p.max {
			return r, err
		}
		wait, fromServer := serverWait(r)
		if !fromServer {
			wait = p.jitter(attempt)
		}
		if wait > p.maxWait {
			return nil, fmt.Errorf("rate limited until %s (HTTP %d)", time.Now().Add(wait).Format("15:04:05"), status)
		}
		why := "HTTP " + strconv.Itoa(status)
		if err != nil {
			why = err.Error()
		}
		notify("%s: retrying %d/%d in %s — %s", s.label(), attempt+1, p.max, wait.Round(100*time.Millisecond), why)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// retryable reports whether an attempt's outcome is worth another try,
// and the status to report (0 for a network error). A transport failure
// or timeout always is; a status is when it's in the policy's set, and a
// 403 with no rate limit remaining counts as a 429. 401 never is.
func (p *retryPolicy) retryable(r *response, err error) (bool, int) {
	if err != nil {
		var ue *neturl.Error
		return errors.As(err, &ue), 0
	}
	switch {
	case r.status == http.StatusUnauthorized:
		return false, r.status
	case r.status == http.StatusForbidden && rateLimitExhausted(r.header):
		return true, http.StatusTooManyRequests
	}
	return p.on[r.status], r.status
}

// jitter is the attempt's backoff: full jitter over an exponential curve
// from backoff, capped at maxBackoff.
func (p *retryPolicy) jitter(attempt int) time.Duration {
	ceiling := p.backoff << (attempt - 1)
	if ceiling > p.maxBackoff || ceiling <= 0 {
		ceiling = p.maxBackoff
	}
	return time.Duration(rand.Int64N(int64(ceiling) + 1))
}

// rateLimitPairs are the reset / remaining header pairs read, in order.
var rateLimitPairs = [][2]string{
	{"X-RateLimit-Reset", "X-RateLimit-Remaining"},
	{"X-Rate-Limit-Reset", "X-Rate-Limit-Remaining"},
	{"RateLimit-Reset", "RateLimit-Remaining"},
}

func rateLimitExhausted(h http.Header) bool {
	for _, pair := range rateLimitPairs {
		if strings.TrimSpace(h.Get(pair[1])) == "0" {
			return true
		}
	}
	return false
}

// serverWait is how long the server asked to be left alone: Retry-After
// (seconds, fractional allowed, or an HTTP date), else a rate-limit
// reset when no requests remain. A value over 10⁹ is a Unix time;
// anything smaller is seconds from now.
func serverWait(r *response) (time.Duration, bool) {
	if r == nil {
		return 0, false
	}
	if ra := strings.TrimSpace(r.header.Get("Retry-After")); ra != "" {
		if secs, err := strconv.ParseFloat(ra, 64); err == nil {
			return max(0, time.Duration(secs*float64(time.Second))), true
		}
		if t, err := http.ParseTime(ra); err == nil {
			return max(0, time.Until(t)), true
		}
	}
	for _, pair := range rateLimitPairs {
		if strings.TrimSpace(r.header.Get(pair[1])) != "0" {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(r.header.Get(pair[0])), 64)
		if err != nil {
			continue
		}
		if v > 1e9 {
			return max(0, time.Until(time.Unix(int64(v), 0))), true
		}
		return max(0, time.Duration(v*float64(time.Second))), true
	}
	return 0, false
}

// label names a source in a notice: its URL without the query string,
// secrets masked.
func (s *httpSource) label() string {
	u, err := neturl.Parse(s.url)
	if err != nil {
		return s.auth.redact(s.url)
	}
	return s.auth.redact(u.Host + u.Path)
}
