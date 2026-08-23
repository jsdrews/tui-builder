package datasource

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// httpSource is the http data source. Two modes mirror the exec source:
//
//   - Default (one-shot / polled): one request per Fetch; response body is
//     buffered and (when format: json) parsed before return. Refresh
//     triggers a fresh request on each tick.
//
//   - follow: true: streaming mode. One long-running request whose body
//     is read line-by-line; each line becomes an Event. Use for kube
//     `?follow=true` log endpoints, SSE event streams, NDJSON
//     change-feeds — anything that keeps the connection open and emits
//     lines as data arrives.
//
// Both modes satisfy Source; follow mode additionally satisfies
// StreamingSource so the screen / wrangl picks Subscribe over the
// poll-via-Fetch path.
type httpSource struct {
	url      string
	method   string
	headers  map[string]string
	body     string
	format   string // "json" (default) or "text"
	root     string // dot-path applied to parsed JSON before return
	follow   bool
	timeout  time.Duration
	refresh  time.Duration
	paginate *cfg.PaginateConfig // nil = single-page fetch
	window   *cfg.WindowConfig   // nil = not windowed
}

// defaultMaxPages caps a paginate walk when the user didn't set one.
// Sized to comfortably handle typical REST APIs (AWX defaults to 25/page;
// Django REST 100; GitHub 30/100) without exposing runaway walks that
// could OOM the process on a badly-configured endpoint.
const defaultMaxPages = 20

func newHTTP(d *cfg.Source) (Source, error) {
	method := strings.ToUpper(d.Method)
	if method == "" {
		method = http.MethodGet
	}
	timeout := 10 * time.Second
	if d.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(d.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout: %w", err)
		}
	}
	var refresh time.Duration
	if d.Refresh != "" {
		var err error
		refresh, err = time.ParseDuration(d.Refresh)
		if err != nil {
			return nil, fmt.Errorf("refresh: %w", err)
		}
	}
	return &httpSource{
		url:      d.URL,
		method:   method,
		headers:  d.Headers,
		body:     d.Body,
		format:   d.Format,
		root:     d.Root,
		follow:   d.Follow,
		timeout:  timeout,
		refresh:  refresh,
		paginate: d.Paginate,
		window:   d.Window,
	}, nil
}

func (s *httpSource) Refresh() time.Duration { return s.refresh }

func (s *httpSource) Fetch(ctx context.Context) (any, error) {
	if s.follow {
		// Streaming sources hand back an empty snapshot so the bound
		// logview shows a clean empty state before Subscribe takes
		// over. Matches the exec follow contract.
		return "", nil
	}
	if s.paginate != nil {
		return s.fetchPaginated(ctx)
	}
	if s.window != nil {
		// A windowed source has no "everything" to hand back — that's the
		// point of it. Return the first page so `wrangl` and any
		// non-table consumer see representative data instead of an
		// error; the bound table goes through FetchWindow instead.
		page, err := s.FetchWindow(ctx, WindowQuery{Offset: 0, Limit: s.pageSize()})
		if err != nil {
			return nil, err
		}
		return page.Items, nil
	}

	raw, err := s.fetchOne(ctx, s.url)
	if err != nil {
		return nil, err
	}
	if s.format == "text" {
		return string(raw), nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return applyRoot(out, s.root), nil
}

// fetchOne issues one request against url and returns the raw body.
// Shared between the single-page path and the paginate walk so both
// use the same timeout / auth / non-2xx handling.
func (s *httpSource) fetchOne(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	resp, err := s.doURL(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return raw, nil
}

// fetchPaginated walks the configured paginate strategy until either
// there's no next page, MaxPages is reached, or a mid-walk error kills
// the walk (respecting OnPageError). Each page's Root-sliced items are
// concatenated into one []any.
//
// The walk exclusively uses the RAW (pre-Root) response for
// next-link lookup — Root sees the items, next_path sees the envelope
// (both live at the same level in Django REST / AWX responses:
// `{next: "...", results: [...]}`).
func (s *httpSource) fetchPaginated(ctx context.Context) (any, error) {
	max := s.paginate.MaxPages
	if max == 0 {
		max = defaultMaxPages
	}
	skipOnErr := s.paginate.OnPageError == "skip"

	var acc []any
	url := s.url
	for page := 0; page < max; page++ {
		raw, err := s.fetchOne(ctx, url)
		if err != nil {
			if skipOnErr && page > 0 {
				// Return what we have; the caller sees a partial result
				// rather than losing every page we already succeeded on.
				return acc, nil
			}
			return nil, fmt.Errorf("page %d: %w", page+1, err)
		}
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			if skipOnErr && page > 0 {
				return acc, nil
			}
			return nil, fmt.Errorf("page %d: parse json: %w", page+1, err)
		}
		// Extract this page's items via Root, then flatten into acc.
		// Non-array pages pass through as one element — matches the
		// non-paginated Fetch's behaviour on single-object responses.
		items := applyRoot(parsed, s.root)
		acc = append(acc, Iter(items)...)

		// Find the next page's URL. Missing / null / empty string all
		// mean "no more pages" — Django REST returns null for `next`
		// on the last page, and that's how we know to stop.
		next := Get(parsed, s.paginate.NextPath)
		nextStr, ok := next.(string)
		if !ok || nextStr == "" {
			return acc, nil
		}
		url = nextStr
	}
	// Hit max_pages with more pages potentially available. Return what
	// we have; the cap is a safety measure, not an error. If callers
	// need to know we truncated they can set on_page_error: fail and
	// bump max_pages, or add a follow-up feature (a "truncated" flag
	// on the result) if this becomes a real complaint.
	return acc, nil
}

// pageSize is the configured window size, or the default.
func (s *httpSource) pageSize() int {
	if s.window == nil || s.window.PageSize <= 0 {
		return cfg.DefaultWindowPageSize
	}
	return s.window.PageSize
}

// FetchWindow implements WindowedSource: one request for one window,
// with the filter and sort pushed into the query string so the server
// answers them across the whole set rather than the page.
//
// Unlike fetchPaginated this makes exactly one request. That is the
// trade: the caller re-invokes as the user scrolls, so latency is paid
// per screenful instead of all at once up front.
func (s *httpSource) FetchWindow(ctx context.Context, q WindowQuery) (WindowPage, error) {
	if s.window == nil {
		return WindowPage{}, ErrNotWindowed
	}
	url, err := s.windowURL(q)
	if err != nil {
		return WindowPage{}, err
	}
	raw, err := s.fetchOne(ctx, url)
	if err != nil {
		return WindowPage{}, err
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return WindowPage{}, fmt.Errorf("parse json: %w", err)
	}
	// Root sees the items, total_path sees the envelope — the same
	// split fetchPaginated uses for next_path, and for the same reason:
	// in `{numFound: N, docs: [...]}` both live at the top level.
	page := WindowPage{Items: Iter(applyRoot(parsed, s.root)), Total: -1}
	if p := s.window.TotalPath; p != "" {
		if n, ok := toInt(Get(parsed, p)); ok {
			page.Total = n
		}
		// A missing or non-numeric total is not an error: -1 means
		// "can't say", and the table degrades to treating the end of
		// what has loaded as the end of the set.
	}
	return page, nil
}

// windowURL renders q into s.url's query string. Any parameters already
// on the configured URL (`?fields=title,author`) survive — only the
// window's own parameters are set, so a source can pin API options in
// the URL and let the window drive the rest.
func (s *httpSource) windowURL(q WindowQuery) (string, error) {
	u, err := neturl.Parse(s.url)
	if err != nil {
		return "", fmt.Errorf("url: %w", err)
	}
	w := s.window
	vals := u.Query()
	vals.Set(w.OffsetParam, strconv.Itoa(q.Offset))
	vals.Set(w.LimitParam, strconv.Itoa(q.Limit))

	if q.Search != "" && w.SearchParam != "" {
		vals.Set(w.SearchParam, q.Search)
	}
	for title, val := range q.Filters {
		if param, ok := w.Filters[title]; ok {
			vals.Set(param, val)
		}
		// A scoped term whose column has no mapping was already folded
		// into Search by the caller, so there's nothing to do here.
	}
	if q.Sort != "" && w.SortParam != "" {
		field := q.Sort
		if mapped, ok := w.Sorts[field]; ok {
			field = mapped
		}
		if q.Desc {
			field = w.SortDescPrefix + field
		}
		vals.Set(w.SortParam, field)
	}
	u.RawQuery = vals.Encode()
	return u.String(), nil
}

// toInt coerces a JSON number (always float64 out of encoding/json) or a
// numeric string into an int. Returns ok=false for anything else, which
// the caller treats as "total unknown" rather than an error.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	}
	return 0, false
}

// Subscribe implements StreamingSource for follow mode. Holds the
// request open and pushes one Event per line read from the response
// body. The caller's ctx threads through net/http — cancelling it
// closes the connection and ends the read loop. Non-follow sources
// return ErrNotStreaming so the screen / output layer falls back to
// the polling path.
//
// We deliberately don't apply s.timeout here — follow streams are
// inherently long-running; only ctx cancellation should stop them.
func (s *httpSource) Subscribe(ctx context.Context) (<-chan Event, error) {
	if !s.follow {
		return nil, ErrNotStreaming
	}
	resp, err := s.do(ctx)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		// Allow long lines (some log endpoints emit JSON blobs per line).
		// Cap the buffer so a malformed stream can't grow it without
		// limit.
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case ch <- Event{Line: sc.Text()}:
			case <-ctx.Done():
				return
			}
		}
		// Scanner stopped — either EOF (server hung up) or read error.
		// EOF is a normal end-of-stream; surface read errors so the
		// caller can show them.
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			select {
			case ch <- Event{Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return ch, nil
}

// do builds a request against s.url, applies headers, and dispatches
// it. Shared between the non-paginated Fetch and Subscribe (streaming).
func (s *httpSource) do(ctx context.Context) (*http.Response, error) {
	return s.doURL(ctx, s.url)
}

// doURL is the URL-override form used by the paginate walker to hit
// successive pages. Body / method / headers / Accept default all
// carry over from the original request — Django REST's `next` URLs
// are meant to be called with the same auth headers, so this is
// correct-by-default.
func (s *httpSource) doURL(ctx context.Context, url string) (*http.Response, error) {
	var bodyReader io.Reader
	if s.body != "" {
		bodyReader = strings.NewReader(s.body)
	}
	req, err := http.NewRequestWithContext(ctx, s.method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	return http.DefaultClient.Do(req)
}
