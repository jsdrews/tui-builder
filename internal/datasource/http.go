package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// httpSource is the http data source — a single GET (or configured method)
// with optional headers, body, and refresh polling.
type httpSource struct {
	url     string
	method  string
	headers map[string]string
	body    string
	format  string // "json" (default) or "text"
	root    string // dot-path applied to parsed JSON before return
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
		timeout: timeout,
		refresh: refresh,
	}, nil
}

func (s *httpSource) Refresh() time.Duration { return s.refresh }

func (s *httpSource) Fetch(ctx context.Context) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

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
	resp, err := http.DefaultClient.Do(req)
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
