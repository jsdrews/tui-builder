package datasource

// The paginate walk: every page of an http source fetched up front and
// concatenated. Four strategies, each with its own request side and its
// own stop signal (plans/http-source.md §6):
//
//	link    follow the next URL the response names, exactly as sent
//	cursor  send the cursor the response names
//	offset  count items: offset = items so far
//	page    count pages: page_start, page_start+1, …

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"reflect"
	"regexp"
	"strconv"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// defaultMaxPages caps a paginate walk when the user didn't set one.
// Sized to comfortably handle typical REST APIs (AWX defaults to 25/page;
// Django REST 100; GitHub 30/100) without exposing runaway walks that
// could OOM the process on a badly-configured endpoint.
const defaultMaxPages = 20

// walkPos is where a walk is: the ${paginate.*} tokens.
type walkPos struct {
	cursor any // the next cursor, as the response gave it; nil on the first page
	offset int
	page   int
	limit  int
}

// fetchPaginated walks the configured strategy until its stop signal,
// more_when turning false, a short page (with stop_on_short_page), a
// page repeating the last, or max_pages. A mid-walk error ends it per
// on_page_error. Each page's items (after root:) are concatenated.
//
// Response-level values (next_path, cursor_path, total_path, more_when)
// read the raw response, before root:.
func (s *httpSource) fetchPaginated(ctx context.Context) (any, error) {
	acc, cut, err := s.walkPages(ctx)
	if err != nil {
		return nil, err
	}
	// A walk cut short is said once, not on every refresh; a complete
	// one clears it, so the next cut is said again.
	if cut != "" {
		s.notices.post("walk", s.label()+": "+cut)
	} else {
		s.notices.clear("walk")
	}
	return acc, nil
}

// walkPages does the walk. cut says why it stopped short of the end —
// max_pages with more to come, or a page skipped on error — and is ""
// when the walk ran to its stop signal.
func (s *httpSource) walkPages(ctx context.Context) ([]any, string, error) {
	p := s.paginate
	max := p.MaxPages
	if max == 0 {
		max = defaultMaxPages
	}
	skipOnErr := p.OnPageError == "skip"
	var more *expr.Program
	if p.MoreWhen != "" {
		var err error
		if more, err = expr.Compile(p.MoreWhen); err != nil {
			return nil, "", fmt.Errorf("paginate.more_when: %w", err)
		}
	}
	pos := walkPos{limit: s.walkPageSize(), page: 1}
	if p.PageStart != nil {
		pos.page = *p.PageStart
	}

	var acc, prevItems []any
	var prevNext string
	lastLen, total := 0, -1
	// A link walk's first page is the configured request; later ones
	// follow the next URL exactly as the server sent it, without
	// re-applying query:.
	next := s.requestURL()
	for n := 0; n < max; n++ {
		url, body := next, s.body
		if p.Strategy != "link" {
			var err error
			if url, body, err = s.pageRequest(pos); err != nil {
				return nil, "", err
			}
		}
		r, err := s.send(ctx, url, body)
		var parsed any
		if err == nil {
			parsed, err = s.parse(r)
		}
		if err != nil {
			if skipOnErr && n > 0 {
				// Return what we have; the caller sees a partial result
				// rather than losing every page we already succeeded on.
				why := err.Error()
				var he *HTTPError
				if errors.As(err, &he) {
					why = fmt.Sprintf("HTTP %d", he.Status)
				}
				kept := fmt.Sprintf("the first %d pages", n)
				if n == 1 {
					kept = "the first page"
				}
				return acc, fmt.Sprintf("page %d failed (%s) — kept %s (%d rows)", n+1, why, kept, len(acc)), nil
			}
			return nil, "", fmt.Errorf("page %d: %w", n+1, err)
		}
		// Non-array pages pass through as one element — matches the
		// non-paginated Fetch's behaviour on single-object responses.
		items := Iter(applyRoot(parsed, s.root))

		// The repeat guard: an API that ignores the page parameter, or
		// clamps it (json-server v1), answers the same page forever.
		if p.Strategy == "offset" || p.Strategy == "page" {
			if len(items) > 0 && reflect.DeepEqual(items, prevItems) {
				notify("%s: page %d repeated page %d — stopped the walk there", s.label(), n+1, n)
				return acc, "", nil
			}
			prevItems = items
		}
		acc = append(acc, items...)
		lastLen = len(items)

		if more != nil {
			ok, err := expr.EvalBool(more, parsed)
			if err != nil {
				return nil, "", fmt.Errorf("page %d: more_when: %w", n+1, err)
			}
			if !ok {
				return acc, "", nil
			}
		}
		if p.StopOnShortPage && len(items) < pos.limit {
			return acc, "", nil
		}

		switch p.Strategy {
		case "link":
			// Missing / null / empty all mean "no more pages" — Django
			// REST returns null for `next` on the last page. A relative
			// URL resolves against the page that sent it.
			nextURL, ok := s.locate(p.NextPath, parsed, r, items).(string)
			if !ok || nextURL == "" {
				return acc, "", nil
			}
			next = resolveRef(r.url, nextURL)
			if next == prevNext {
				notify("%s: page %d named the same next URL as page %d — stopped the walk there", s.label(), n+1, n)
				return acc, "", nil
			}
			prevNext = next
		case "cursor":
			// An empty page doesn't end a cursor walk (Graph, Google,
			// DynamoDB and Slack send them mid-walk); the cursor does.
			c := s.locate(p.CursorPath, parsed, r, items)
			if !present(c) {
				return acc, "", nil
			}
			if reflect.DeepEqual(c, pos.cursor) {
				notify("%s: page %d gave the same cursor as page %d — stopped the walk there", s.label(), n+1, n)
				return acc, "", nil
			}
			pos.cursor = c
		case "offset", "page":
			if len(items) == 0 {
				return acc, "", nil
			}
			if p.TotalPath != "" {
				if t, ok := toInt(s.locate(p.TotalPath, parsed, r, items)); ok {
					if total = t; len(acc) >= total {
						return acc, "", nil
					}
				}
			}
			pos.offset += len(items)
			pos.page++
		}
	}
	// Hit max_pages. Every stop check ran on the last page, so more
	// remains — unless an offset / page walk with no total ended on a
	// short page, which is probably the end. The cap is a safety
	// measure, not an error.
	numbered := p.Strategy == "offset" || p.Strategy == "page"
	if numbered && p.TotalPath == "" && lastLen < pos.limit {
		return acc, "", nil
	}
	hint := "raise paginate.max_pages or use window:"
	if total >= 0 {
		return acc, fmt.Sprintf("stopped at max_pages (%d) — %d of %d rows; %s", max, len(acc), total, hint), nil
	}
	return acc, fmt.Sprintf("stopped at max_pages (%d) — %d rows, more remain; %s", max, len(acc), hint), nil
}

// walkPageSize is the walk's limit: page_size, or the window default.
func (s *httpSource) walkPageSize() int {
	if s.paginate.PageSize > 0 {
		return s.paginate.PageSize
	}
	return cfg.DefaultWindowPageSize
}

// pageRequest builds one cursor / offset / page request at pos: the
// strategy's own parameters and the patch, over the configured request.
func (s *httpSource) pageRequest(pos walkPos) (string, string, error) {
	p := s.paginate
	u, err := neturl.Parse(s.url)
	if err != nil {
		return "", "", fmt.Errorf("url: %w", err)
	}
	params := u.Query()
	s.applyQuery(params)
	set := func(key, val string) {
		if key != "" && val != "" {
			params.Set(key, val)
		}
	}
	set(p.LimitParam, strconv.Itoa(pos.limit))
	switch p.Strategy {
	case "cursor":
		set(p.CursorParam, tokenText(pos.cursor))
	case "offset":
		set(p.OffsetParam, strconv.Itoa(pos.offset))
	case "page":
		set(p.PageParam, strconv.Itoa(pos.page))
	}

	body := s.body
	if len(p.Patch) > 0 {
		patch := renderWalkPatch(p.Patch, pos).(map[string]any)
		switch s.bodyKind {
		case "":
			for k, v := range patch {
				params.Set(k, tokenText(v))
			}
		case "json":
			doc, ok := cloneTree(s.json).(map[string]any)
			if !ok {
				return "", "", fmt.Errorf("json: must be an object for paginate.patch to merge into")
			}
			deepMerge(doc, patch)
			b, err := json.Marshal(doc)
			if err != nil {
				return "", "", err
			}
			body = string(b)
		case "form":
			vals := formValues(s.form)
			for k, v := range patch {
				vals.Set(k, tokenText(v))
			}
			body = vals.Encode()
		}
	}
	u.RawQuery = params.Encode()
	return u.String(), body, nil
}

var walkTokenRe = regexp.MustCompile(`\$\{paginate\.(cursor|offset|page|limit)\}`)

// renderWalkPatch resolves ${paginate.*} in a patch. A value that is
// exactly one token takes its typed value (a cursor as the response gave
// it, numbers as numbers) and drops out when empty — so the first
// request carries no cursor. A token inside a longer string is text.
func renderWalkPatch(v any, pos walkPos) any {
	vals := map[string]any{"cursor": pos.cursor, "offset": pos.offset, "page": pos.page, "limit": pos.limit}
	var render func(v any) (any, bool)
	render = func(v any) (any, bool) {
		switch x := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, sub := range x {
				if r, ok := render(sub); ok {
					out[k] = r
				}
			}
			return out, true
		case []any:
			out := make([]any, 0, len(x))
			for _, sub := range x {
				if r, ok := render(sub); ok {
					out = append(out, r)
				}
			}
			return out, true
		case string:
			if m := walkTokenRe.FindStringSubmatch(x); m != nil && m[0] == x {
				val := vals[m[1]]
				return val, present(val)
			}
			return walkTokenRe.ReplaceAllStringFunc(x, func(tok string) string {
				return tokenText(vals[walkTokenRe.FindStringSubmatch(tok)[1]])
			}), true
		}
		return v, true
	}
	out, _ := render(v)
	return out
}

// tokenText is a value as request text: a string as itself, nil as "",
// anything else as compact JSON.
func tokenText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// cloneTree deep-copies a decoded JSON value.
func cloneTree(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, sub := range x {
			out[k] = cloneTree(sub)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, sub := range x {
			out[i] = cloneTree(sub)
		}
		return out
	}
	return v
}
