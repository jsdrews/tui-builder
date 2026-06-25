package datasource

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	url     string
	method  string
	headers map[string]string
	body    string
	format  string // "json" (default) or "text"
	root    string // dot-path applied to parsed JSON before return
	follow  bool
	timeout time.Duration
	refresh time.Duration
}

func newHTTP(d *cfg.DataSource) (Source, error) {
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
		url:     d.URL,
		method:  method,
		headers: d.Headers,
		body:    d.Body,
		format:  d.Format,
		root:    d.Root,
		follow:  d.Follow,
		timeout: timeout,
		refresh: refresh,
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

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	resp, err := s.do(ctx)
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
	if s.format == "text" {
		return string(raw), nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return applyRoot(out, s.root), nil
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

// do builds the request, applies headers, and dispatches it. Shared
// between Fetch (one-shot) and Subscribe (streaming).
func (s *httpSource) do(ctx context.Context) (*http.Response, error) {
	var bodyReader io.Reader
	if s.body != "" {
		bodyReader = strings.NewReader(s.body)
	}
	req, err := http.NewRequestWithContext(ctx, s.method, s.url, bodyReader)
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
