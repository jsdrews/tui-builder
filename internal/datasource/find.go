package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"strconv"
)

// FindingSource answers a search across a remote set, not only the items
// a view holds: where the first item matching a term is, before or after
// a position. A windowed http source implements it when its `window:`
// declares `find:`; without it a search covers only what's loaded.
//
// It's an optional extension like StreamingSource: a separate question
// (where is it?) from the one Fetch / FetchWindow / FetchEdge answer
// (what's there?).
type FindingSource interface {
	FindItem(ctx context.Context, q FindQuery) (FindResult, error)
}

// FindQuery asks for the first item matching Term after the current item
// when Newer, before it otherwise, within the view's filter.
type FindQuery struct {
	Term  string
	Newer bool
	// From is the current item's position (Seekable data only).
	From int
	// Cursor is the current item's cursor, JSON-encoded as EdgePage
	// returns it. Empty when nothing is focused.
	Cursor string
	// Search and Filters are the view's filter, as on WindowQuery.
	Search   string
	Filters  map[string]string
	Compares []Comparison
}

// FindResult is a search's answer.
type FindResult struct {
	Found bool
	// Item is the matching item.
	Item any
	// Position is its position in the set (Seekable data).
	Position int
	// Cursor is its cursor, JSON-encoded (Anchored data).
	Cursor string
}

// FindItem implements FindingSource: the source's request with the
// `find:` patch applied, asking for the first match.
func (s *httpSource) FindItem(ctx context.Context, q FindQuery) (FindResult, error) {
	if s.window == nil || s.window.Find == nil {
		return FindResult{}, ErrNotWindowed
	}
	w, f := s.window, s.window.Find
	dir := "older"
	if q.Newer {
		dir = "newer"
	}
	eq := EdgeQuery{Cursor: q.Cursor, Search: q.Search, Filters: q.Filters}
	vals := edgeTokens(eq, w.Filters)
	vals["term"], vals["dir"], vals["from"] = q.Term, dir, strconv.Itoa(q.From)
	vals["limit"] = "1"

	u, err := neturl.Parse(s.url)
	if err != nil {
		return FindResult{}, fmt.Errorf("url: %w", err)
	}
	params := u.Query()
	s.applyQuery(params)
	// The view's filter applies to the search as to every request.
	if q.Search != "" && w.SearchParam != "" {
		params.Set(w.SearchParam, q.Search)
	}
	if s.bodyKind == "" {
		for title, val := range q.Filters {
			if param, ok := w.Filters[title]; ok {
				params.Set(param, val)
			}
		}
		s.setCompares(params, q.Compares)
	}
	rendered, _ := renderPatch(f.Params, eq, vals)
	for k, v := range rendered.(map[string]any) {
		if str, ok := v.(string); ok {
			params.Set(k, str)
			continue
		}
		b, _ := json.Marshal(v)
		params.Set(k, cursorText(string(b)))
	}
	u.RawQuery = params.Encode()
	body := s.body
	if len(f.Body) > 0 {
		if body, err = s.edgeBody(f.Body, eq, vals); err != nil {
			return FindResult{}, fmt.Errorf("find: %w", err)
		}
	}

	r, err := s.send(ctx, u.String(), body)
	if err != nil {
		return FindResult{}, err
	}
	parsed, err := s.parse(r)
	if err != nil {
		return FindResult{}, err
	}
	items := Iter(applyRoot(parsed, s.root))
	if len(items) == 0 {
		return FindResult{}, nil
	}
	res := FindResult{Found: true, Item: items[0]}
	if f.Position != "" {
		n, ok := toInt(Get(items[0], f.Position))
		if !ok {
			return FindResult{}, fmt.Errorf("find: the match has no position at %q", f.Position)
		}
		res.Position = n
	}
	if w.Cursor != "" {
		if v := Get(items[0], w.Cursor); v != nil {
			b, _ := json.Marshal(v)
			res.Cursor = string(b)
		}
	}
	return res, nil
}
