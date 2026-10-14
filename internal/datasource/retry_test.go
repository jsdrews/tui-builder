package datasource

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// flaky answers with each status in turn (and the matching headers), then
// 200 for good.
func flaky(t *testing.T, steps ...func(w http.ResponseWriter)) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits <= len(steps) {
			steps[hits-1](w)
			return
		}
		io.WriteString(w, `[1]`)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func status(code int, header ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(code)
	}
}

func fast(max int, on ...int) *cfg.RetrySpec {
	return &cfg.RetrySpec{Max: max, Backoff: "1ms", MaxBackoff: "5ms", On: on}
}

func captureNotices(t *testing.T) *[]string {
	t.Helper()
	var got []string
	Notify = func(m string) { got = append(got, m) }
	t.Cleanup(func() { Notify = nil })
	return &got
}

func TestRetryRecovers(t *testing.T) {
	notices := captureNotices(t)
	srv, hits := flaky(t, status(503), status(502))
	got, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL + "/x?k=v", Retry: fast(3)})
	if err != nil || *hits != 3 || len(got.([]any)) != 1 {
		t.Fatalf("got %v, %v after %d hits", got, err, *hits)
	}
	if len(*notices) != 2 || !strings.Contains((*notices)[0], "/x: retrying 2/3 in") || !strings.HasSuffix((*notices)[0], "— HTTP 503") {
		t.Errorf("notices = %q", *notices)
	}
}

func TestRetryGivesUpAfterMax(t *testing.T) {
	srv, hits := flaky(t, status(500), status(500), status(500), status(500))
	_, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Retry: fast(3)})
	if err == nil || !strings.HasPrefix(err.Error(), "HTTP 500") || *hits != 3 {
		t.Errorf("err = %v after %d hits", err, *hits)
	}
}

func TestNoRetryWithoutRetryBlock(t *testing.T) {
	srv, hits := flaky(t, status(503))
	if _, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL}); err == nil || *hits != 1 {
		t.Errorf("err = %v after %d hits", err, *hits)
	}
}

func TestRetryNeverOn401(t *testing.T) {
	srv, hits := flaky(t, status(401))
	if _, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Retry: fast(3)}); err == nil || *hits != 1 {
		t.Errorf("err = %v after %d hits", err, *hits)
	}
}

func TestRetryOnReplacesStatuses(t *testing.T) {
	srv, hits := flaky(t, status(503))
	if _, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Retry: fast(3, 500)}); err == nil || *hits != 1 {
		t.Errorf("503 isn't in on: [500]: err = %v after %d hits", err, *hits)
	}
}

// GitHub's primary rate limit: a 403 with nothing remaining is a 429.
func TestRetry403RateLimited(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Unix(), 10)
	srv, hits := flaky(t, status(403, "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", reset))
	if _, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Retry: fast(2)}); err != nil || *hits != 2 {
		t.Errorf("err = %v after %d hits", err, *hits)
	}
}

func TestRetryLongWaitFailsAtOnce(t *testing.T) {
	srv, hits := flaky(t, status(429, "Retry-After", "3600"))
	_, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Retry: fast(3)})
	if err == nil || !strings.HasPrefix(err.Error(), "rate limited until ") || !strings.HasSuffix(err.Error(), "(HTTP 429)") || *hits != 1 {
		t.Errorf("err = %v after %d hits", err, *hits)
	}
}

func TestRetryNetworkError(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	if _, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Retry: fast(2)}); err != nil || hits != 2 {
		t.Errorf("err = %v after %d hits", err, hits)
	}
}

func TestServerWait(t *testing.T) {
	future := time.Now().Add(90 * time.Second)
	cases := []struct {
		header []string
		want   time.Duration
		ok     bool
	}{
		{[]string{"Retry-After", "2.5"}, 2500 * time.Millisecond, true},
		{[]string{"Retry-After", future.UTC().Format(http.TimeFormat)}, 90 * time.Second, true},
		{[]string{"X-RateLimit-Remaining", "0", "X-RateLimit-Reset", strconv.FormatInt(future.Unix(), 10)}, 90 * time.Second, true},
		{[]string{"RateLimit-Remaining", "0", "RateLimit-Reset", "12"}, 12 * time.Second, true},
		{[]string{"X-RateLimit-Remaining", "5", "X-RateLimit-Reset", "12"}, 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		h := http.Header{}
		for i := 0; i+1 < len(c.header); i += 2 {
			h.Set(c.header[i], c.header[i+1])
		}
		got, ok := serverWait(&response{header: h})
		if ok != c.ok || (got-c.want).Abs() > 2*time.Second {
			t.Errorf("%v: got %v, %v; want %v, %v", c.header, got, ok, c.want, c.ok)
		}
	}
}

func TestJitterStaysUnderTheCurve(t *testing.T) {
	p := newRetryPolicy(&cfg.RetrySpec{Backoff: "100ms", MaxBackoff: "1s"})
	for attempt, ceiling := range map[int]time.Duration{1: 100 * time.Millisecond, 3: 400 * time.Millisecond, 10: time.Second} {
		for range 50 {
			if d := p.jitter(attempt); d < 0 || d > ceiling {
				t.Fatalf("attempt %d: %v over %v", attempt, d, ceiling)
			}
		}
	}
}

// A 304 keeps the last value; the request carried what the last answer
// said about itself.
func TestConditionalGET(t *testing.T) {
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("If-None-Match"))
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		io.WriteString(w, `[{"n":1}]`)
	}))
	defer srv.Close()
	s, _ := newHTTP(&cfg.Source{Type: "http", URL: srv.URL})
	first, _ := s.Fetch(t.Context())
	second, err := s.Fetch(t.Context())
	if err != nil || len(second.([]any)) != 1 || len(first.([]any)) != 1 {
		t.Fatalf("second = %v, %v", second, err)
	}
	if sent[0] != "" || sent[1] != `"v1"` {
		t.Errorf("If-None-Match sent = %q", sent)
	}

	off := false
	s, _ = newHTTP(&cfg.Source{Type: "http", URL: srv.URL, Conditional: &off})
	s.Fetch(t.Context())
	s.Fetch(t.Context())
	if sent[3] != "" {
		t.Errorf("conditional: false still sent %q", sent[3])
	}
}

func TestMaxBody(t *testing.T) {
	srv := replyWith(t, 200, nil, `["`+strings.Repeat("x", 2048)+`"]`)
	_, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, MaxBody: "1KB"})
	if err == nil || err.Error() != "response larger than 1KB (max_body)" {
		t.Errorf("err = %v", err)
	}
}
