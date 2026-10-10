package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// AnchoredSource serves Anchored data: items reachable only by walking
// from an edge, with no offsets and no total. Elasticsearch search_after,
// a log API filtered by timestamp, and any ?after=<token> API work this
// way. A source is Anchored when its `window:` declares `cursor:`.
//
// Like WindowedSource it still satisfies Source: Fetch returns the newest
// page, so `wrangl` sees representative data.
type AnchoredSource interface {
	Source
	FetchEdge(ctx context.Context, q EdgeQuery) (EdgePage, error)
}

// EdgeQuery asks for up to Limit items beyond Cursor: newer ones when
// Newer, older ones otherwise. An empty Cursor starts from the newest
// item (for an older walk) or the oldest (for a newer one).
type EdgeQuery struct {
	// Cursor is the cursor of the edge item, JSON-encoded as EdgePage
	// returned it.
	Cursor string
	Newer  bool
	Limit  int
	// Search and Filters mean what they do on WindowQuery.
	Search  string
	Filters map[string]string
}

// EdgePage is one edge request's answer.
type EdgePage struct {
	// Items are oldest first, whichever way the request walked.
	Items []any
	// Cursors holds each item's cursor, JSON-encoded, parallel to Items.
	Cursors []string
	// More reports whether that edge has items beyond these.
	More bool
}

// edgePage turns a reply into an EdgePage. The request asked for one more
// item than wanted: whether it came back is how More is known without an
// API-specific flag. An older walk is answered newest first (the `older:`
// request sorts descending, walking away from the cursor), so it is
// reversed; the items nearest the cursor are the ones kept either way.
func edgePage(items []any, q EdgeQuery, cursorPath string) EdgePage {
	p := EdgePage{More: len(items) > q.Limit}
	if p.More {
		items = items[:q.Limit]
	}
	if !q.Newer {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	p.Items = items
	p.Cursors = make([]string, len(items))
	for i, it := range items {
		if v := Get(it, cursorPath); v != nil {
			b, _ := json.Marshal(v)
			p.Cursors[i] = string(b)
		}
	}
	return p
}

// edgeTokens is the ${window.*} namespace for an Anchored request. limit
// is one more than wanted, for edgePage's More. cursor is the edge item's
// cursor as text: a JSON string is unquoted (most APIs take a bare token
// or timestamp), anything else stays JSON (an ES sort array).
func edgeTokens(q EdgeQuery, filters map[string]string) map[string]string {
	vals := map[string]string{
		"cursor": cursorText(q.Cursor),
		"dir":    "older",
		"limit":  strconv.Itoa(q.Limit + 1),
		"search": q.Search,
	}
	if q.Newer {
		vals["dir"] = "newer"
	}
	for title, token := range filters {
		if v, ok := q.Filters[title]; ok {
			vals["filters."+token] = v
		}
	}
	return vals
}

func cursorText(cursor string) string {
	var s string
	if err := json.Unmarshal([]byte(cursor), &s); err == nil {
		return s
	}
	return cursor
}

var wholeTokenRe = regexp.MustCompile(`^\$\{window\.([a-zA-Z0-9_.]+)\}$`)

// renderPatch resolves an `older:` / `newer:` patch against one request.
// A value that is exactly one token takes that token's typed value (the
// cursor as its JSON value, the limit as a number) and is left out when
// it resolves empty — which is how the first request, with no cursor,
// omits search_after. Tokens inside longer strings substitute as text.
func renderPatch(v any, q EdgeQuery, vals map[string]string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, sub := range x {
			if r, ok := renderPatch(sub, q, vals); ok {
				out[k] = r
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(x))
		for _, sub := range x {
			if r, ok := renderPatch(sub, q, vals); ok {
				out = append(out, r)
			}
		}
		return out, true
	case string:
		if m := wholeTokenRe.FindStringSubmatch(x); m != nil {
			switch name := m[1]; name {
			case "cursor":
				if q.Cursor == "" {
					return nil, false
				}
				var raw any
				if err := json.Unmarshal([]byte(q.Cursor), &raw); err != nil {
					return q.Cursor, true
				}
				return raw, true
			case "limit":
				return q.Limit + 1, true
			default:
				if vals[name] == "" {
					return nil, false
				}
				return vals[name], true
			}
		}
		return windowTokenRe.ReplaceAllStringFunc(x, func(tok string) string {
			return vals[windowTokenRe.FindStringSubmatch(tok)[1]]
		}), true
	}
	return v, true
}

// renderEdgeBody substitutes the request's tokens into a JSON body and
// merges the direction's patch into it. Tokens in the body text take
// JSON-safe values: the cursor as JSON, the limit as a number, text
// values escaped for use inside a JSON string.
func renderEdgeBody(body string, patch map[string]any, q EdgeQuery, vals map[string]string) (string, error) {
	text := windowTokenRe.ReplaceAllStringFunc(body, func(tok string) string {
		switch name := windowTokenRe.FindStringSubmatch(tok)[1]; name {
		case "cursor":
			if q.Cursor == "" {
				return "null"
			}
			return q.Cursor
		case "limit":
			return vals["limit"]
		default:
			b, _ := json.Marshal(vals[name])
			return strings.Trim(string(b), `"`)
		}
	})
	doc := map[string]any{}
	if strings.TrimSpace(text) != "" {
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			return "", fmt.Errorf("body: an Anchored window merges `older:` / `newer:` into the body, so it must be a JSON object: %w", err)
		}
	}
	rendered, _ := renderPatch(patch, q, vals)
	deepMerge(doc, rendered.(map[string]any))
	b, err := json.Marshal(doc)
	return string(b), err
}

// deepMerge copies src into dst, merging nested objects key by key and
// replacing everything else.
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}
