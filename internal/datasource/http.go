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
	"github.com/jsdrews/tui-builder/internal/version"
)

// UserAgent is sent on every http request whose headers don't set one.
// GitHub rejects requests without one, and some APIs throttle generic
// ones. wrangl sets its own name at startup.
var UserAgent = "tui-builder/" + version.String()

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
	url         string
	method      string
	headers     map[string]string
	body        string                    // the encoded body: raw, JSON or URL-encoded
	bodyKind    string                    // "", "raw", "json" or "form"
	json        any                       // json:, kept as a tree so patches merge into it
	form        map[string]cfg.QueryValue // form:
	query       map[string]cfg.QueryValue // query:, laid over the url's own
	format      string                    // "json" (default) or "text"
	auth        *authState                // auth: and the secrets to mask
	retry       *retryPolicy              // nil: one attempt per request
	maxBody     int64                     // the largest response read, per page
	conditional bool                      // send If-None-Match / If-Modified-Since
	validators  *validators               // what conditional GET remembers
	emptyOn     map[int]bool              // statuses answered with the empty result
	errorPath   string                    // locator for an error's message
	root        string                    // dot-path applied to parsed JSON before return
	follow      bool
	timeout     time.Duration
	refresh     time.Duration
	paginate    *cfg.PaginateConfig // nil = single-page fetch
	window      *cfg.WindowConfig   // nil = not windowed
}

func newHTTP(d *cfg.Source) (Source, error) {
	method := strings.ToUpper(d.Method)
	if method == "" {
		method = http.MethodGet
		if d.HasBody() {
			method = http.MethodPost
		}
	}
	maxBody := int64(cfg.DefaultMaxBody)
	if d.MaxBody != "" {
		n, err := cfg.ParseSize(d.MaxBody)
		if err != nil {
			return nil, fmt.Errorf("max_body: %w", err)
		}
		maxBody = n
	}
	// Conditional GET is for a source that asks one question and asks it
	// again: not a walk, a window or a stream, and not a POST.
	conditional := (d.Conditional == nil || *d.Conditional) && d.Paginate == nil &&
		d.Window == nil && !d.Follow && method == http.MethodGet
	emptyOn := map[int]bool{}
	for _, code := range d.EmptyOn {
		emptyOn[code] = true
	}
	body, kind := d.Body, ""
	jsonBody, errorPath := d.JSON, d.ErrorPath
	if d.GraphQL != nil {
		// graphql: is a json: body of {query, variables}, and a 200 with
		// errors in it is an error.
		jsonBody = d.GraphQL.Body()
		if errorPath == "" {
			errorPath = cfg.GraphQLErrorPath
		}
	}
	switch {
	case jsonBody != nil:
		b, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, fmt.Errorf("json: %w", err)
		}
		body, kind = string(b), "json"
	case len(d.Form) > 0:
		body, kind = formValues(d.Form).Encode(), "form"
	case d.Body != "":
		kind = "raw"
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
		url:         d.URL,
		method:      method,
		headers:     d.Headers,
		body:        body,
		bodyKind:    kind,
		json:        jsonBody,
		form:        d.Form,
		query:       d.Query,
		auth:        newAuthState(d.Auth, d.URL, d.Headers),
		retry:       newRetryPolicy(d.Retry),
		maxBody:     maxBody,
		conditional: conditional,
		validators:  &validators{},
		emptyOn:     emptyOn,
		errorPath:   errorPath,
		format:      d.Format,
		root:        d.Root,
		follow:      d.Follow,
		timeout:     timeout,
		refresh:     refresh,
		paginate:    d.Paginate,
		window:      d.Window,
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
	if s.window != nil && s.window.Cursor != "" {
		// An Anchored source's first page is its newest.
		page, err := s.FetchEdge(ctx, EdgeQuery{Limit: s.pageSize()})
		if err != nil {
			return nil, err
		}
		return page.Items, nil
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

	url := s.requestURL()
	key := url + "\x00" + s.body
	var extra http.Header
	if s.conditional {
		extra = s.validators.headers(key)
	}
	r, err := s.sendWith(ctx, url, s.body, extra)
	if err != nil {
		return nil, err
	}
	if s.conditional && r.status == http.StatusNotModified {
		// Nothing changed: the last answer stands.
		if v, ok := s.validators.last(key); ok {
			return v, nil
		}
	}
	var out any
	if s.format == "text" {
		switch {
		case s.empty(r):
			out = ""
		case !r.ok():
			return nil, s.httpError(r, nil)
		default:
			out = string(r.raw)
		}
	} else {
		parsed, err := s.parse(r)
		if err != nil {
			// Whatever root: still finds is kept, and the error reported too.
			return s.partial(parsed), err
		}
		out = applyRoot(parsed, s.root)
	}
	if s.conditional {
		s.validators.remember(key, r, out)
	}
	return out, nil
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
	r, err := s.send(ctx, url, s.body)
	if err != nil {
		return WindowPage{}, err
	}
	parsed, err := s.parse(r)
	if err != nil {
		return WindowPage{}, err
	}
	// Root sees the items, total_path sees the envelope (or a header) —
	// the same split fetchPaginated uses for next_path, and for the same
	// reason: in `{numFound: N, docs: [...]}` both live at the top level.
	items := Iter(applyRoot(parsed, s.root))
	page := WindowPage{Items: items, Total: -1}
	if p := s.window.TotalPath; p != "" {
		if n, ok := toInt(s.locate(p, parsed, r, items)); ok {
			page.Total = n
		}
		// A missing or non-numeric total is not an error: -1 means
		// "can't say", and the table degrades to treating the end of
		// what has loaded as the end of the set.
	}
	return page, nil
}

// FetchEdge implements AnchoredSource: one request for the items beyond
// an edge. The direction's patch (`older:` / `newer:`) is merged into the
// body, or, for a request with no body, set as query parameters. The
// filter goes where a windowed source's does: search_param / filters on
// the query string, or ${window.search} / ${window.filters.*} tokens.
func (s *httpSource) FetchEdge(ctx context.Context, q EdgeQuery) (EdgePage, error) {
	if s.window == nil || s.window.Cursor == "" {
		return EdgePage{}, ErrNotWindowed
	}
	w := s.window
	vals := edgeTokens(q, w.Filters)
	patch := w.Older
	if q.Newer {
		patch = w.Newer
	}
	u, err := neturl.Parse(s.url)
	if err != nil {
		return EdgePage{}, fmt.Errorf("url: %w", err)
	}
	params := u.Query()
	s.applyQuery(params)
	if w.LimitParam != "" {
		params.Set(w.LimitParam, vals["limit"])
	}
	if q.Search != "" && w.SearchParam != "" {
		params.Set(w.SearchParam, q.Search)
	}
	// With no body, `filters:` names query parameters, as on a windowed
	// source. With one, they're ${window.filters.*} tokens in the body,
	// and strict APIs (Elasticsearch among them) reject parameters they
	// don't know.
	if s.bodyKind == "" {
		for title, val := range q.Filters {
			if param, ok := w.Filters[title]; ok {
				params.Set(param, val)
			}
		}
	}
	body := s.body
	if s.bodyKind == "" {
		rendered, _ := renderPatch(patch, q, vals)
		for k, v := range rendered.(map[string]any) {
			if str, ok := v.(string); ok {
				params.Set(k, str)
				continue
			}
			b, _ := json.Marshal(v)
			params.Set(k, cursorText(string(b)))
		}
	} else if body, err = s.edgeBody(patch, q, vals); err != nil {
		return EdgePage{}, err
	}
	u.RawQuery = params.Encode()

	r, err := s.send(ctx, u.String(), body)
	if err != nil {
		return EdgePage{}, err
	}
	parsed, err := s.parse(r)
	if err != nil {
		return EdgePage{}, err
	}
	return edgePage(Iter(applyRoot(parsed, s.root)), q, w.Cursor), nil
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
	s.applyQuery(vals)
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
	return s.SubscribeSince(ctx, "")
}

// SubscribeSince implements ResumableSource: the stream, with
// ${resume.since} in the URL, headers and body set to since.
func (s *httpSource) SubscribeSince(ctx context.Context, since string) (<-chan Event, error) {
	c := *s
	c.url, c.body, c.headers = resumeOne(s.url, since), resumeOne(s.body, since), resumeMap(s.headers, since)
	return c.subscribe(ctx)
}

func (s *httpSource) subscribe(ctx context.Context) (<-chan Event, error) {
	if !s.follow {
		return nil, ErrNotStreaming
	}
	resp, err := s.do(ctx)
	if err != nil {
		return nil, s.auth.redactErr(err)
	}
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		r := answered(resp, raw)
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		return nil, s.httpError(r, parsed)
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
	return s.doURL(ctx, s.requestURL())
}

// doURL is the URL-override form used by the paginate walker to hit
// successive pages. Body / method / headers / Accept default all
// carry over from the original request — Django REST's `next` URLs
// are meant to be called with the same auth headers, so this is
// correct-by-default.
func (s *httpSource) doURL(ctx context.Context, url string) (*http.Response, error) {
	return s.doWith(ctx, url, s.body, nil)
}

// doWith is doURL with the body overridden — an Anchored window renders a
// body per request.
func (s *httpSource) doWith(ctx context.Context, url, body string, extra http.Header) (*http.Response, error) {
	req, err := s.newRequest(ctx, url, body, extra)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	// An OAuth 2 token the server no longer accepts is fetched afresh,
	// and the request tried once more. This is separate from retry:.
	if err == nil && resp.StatusCode == http.StatusUnauthorized && s.auth.spec.Scheme() == "oauth2" {
		resp.Body.Close()
		s.auth.forgetToken()
		if req, err = s.newRequest(ctx, url, body, extra); err != nil {
			return nil, err
		}
		resp, err = http.DefaultClient.Do(req)
	}
	return resp, err
}

// newRequest builds one request: headers:, the defaults, then auth:.
func (s *httpSource) newRequest(ctx context.Context, url, body string, extra http.Header) (*http.Request, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
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
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		switch s.bodyKind {
		case "json":
			req.Header.Set("Content-Type", "application/json")
		case "form":
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	for k, vs := range extra {
		req.Header[k] = vs
	}
	if err := s.auth.apply(ctx, req.Header, req.URL, s.timeout); err != nil {
		return nil, err
	}
	return req, nil
}

// requestURL is s.url with query: laid over its own query string. A
// source without query: sends its url exactly as written.
func (s *httpSource) requestURL() string {
	if len(s.query) == 0 {
		return s.url
	}
	u, err := neturl.Parse(s.url)
	if err != nil {
		return s.url
	}
	vals := u.Query()
	s.applyQuery(vals)
	u.RawQuery = vals.Encode()
	return u.String()
}

// applyQuery lays query: over vals: each key it sets replaces the url's
// values for that key. Empty values are dropped, and a key whose values
// are all empty is left out entirely.
func (s *httpSource) applyQuery(vals neturl.Values) {
	for k, vs := range s.query {
		vals.Del(k)
		for _, v := range vs {
			if v != "" {
				vals.Add(k, v)
			}
		}
	}
}

// formValues encodes form: the way applyQuery does query:.
func formValues(form map[string]cfg.QueryValue) neturl.Values {
	vals := neturl.Values{}
	for k, vs := range form {
		for _, v := range vs {
			if v != "" {
				vals.Add(k, v)
			}
		}
	}
	return vals
}

// edgeBody renders the body for one window request (an Anchored edge or
// a find) and merges patch into it: deep into json:, as top-level fields
// into form:, and into a raw body: that parses as a JSON object.
func (s *httpSource) edgeBody(patch map[string]any, q EdgeQuery, vals map[string]string) (string, error) {
	switch s.bodyKind {
	case "json":
		rendered, _ := renderPatch(s.json, q, vals)
		doc, ok := rendered.(map[string]any)
		if !ok {
			if len(patch) > 0 {
				return "", fmt.Errorf("json: must be an object for the window's patches to merge into")
			}
			b, err := json.Marshal(rendered)
			return string(b), err
		}
		p, _ := renderPatch(patch, q, vals)
		deepMerge(doc, p.(map[string]any))
		b, err := json.Marshal(doc)
		return string(b), err
	case "form":
		out := neturl.Values{}
		for k, vs := range s.form {
			for _, v := range vs {
				v = windowTokenRe.ReplaceAllStringFunc(v, func(tok string) string {
					return vals[windowTokenRe.FindStringSubmatch(tok)[1]]
				})
				if v != "" {
					out.Add(k, v)
				}
			}
		}
		p, _ := renderPatch(patch, q, vals)
		for k, v := range p.(map[string]any) {
			if str, ok := v.(string); ok {
				out.Set(k, str)
				continue
			}
			b, _ := json.Marshal(v)
			out.Set(k, cursorText(string(b)))
		}
		return out.Encode(), nil
	}
	return renderEdgeBody(s.body, patch, q, vals)
}
