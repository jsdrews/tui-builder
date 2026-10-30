package gen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// The names paging, sort and search parameters go by.
var (
	pageNames   = []string{"page", "page_number", "pageNumber", "p"}
	sizeNames   = []string{"per_page", "page_size", "pageSize", "limit", "size", "count", "top", "$top"}
	offsetNames = []string{"offset", "skip", "start", "_start", "$skip"}
	totalNames  = []string{"total", "count", "total_count", "totalCount", "total_results", "numFound"}
	sortNames   = []string{"order_by", "ordering", "sort", "sort_by", "orderBy", "sortBy"}
	searchNames = []string{"q", "query", "search", "filter", "term"}
	nextNames   = []string{"next", "next_url", "nextLink", "next_link"}
	cursorNames = []string{"next_cursor", "nextCursor", "next_page_token", "nextPageToken", "cursor"}
	cursorIn    = []string{"cursor", "page_token", "pageToken", "after"}
)

// paging is how a list is fetched: a seekable window, a walk of every
// page, or one request, plus what the table can then offer.
type paging struct {
	window   *yaml.Node // window: on the source, or nil
	paginate *yaml.Node // paginate: on the source, or nil
	// filterable: a window filters only with somewhere to send it.
	filterable bool
	// sorted are the columns a window sorts (by value path); nil off a
	// window, where every column sorts locally.
	sorted map[string]bool
	// distinct are a window's enum columns' values, by value path.
	distinct map[string][]string
	note     string // why it's a walk when it looked like a window
	walk     bool
	anchored bool // the overrides made the window anchored
}

// queryParams are an operation's query parameters by name.
func queryParams(o op) map[string]*openapi3.Parameter {
	out := map[string]*openapi3.Parameter{}
	for _, p := range params(o) {
		if p.In == "query" {
			out[p.Name] = p
		}
	}
	return out
}

func paramSchema(p *openapi3.Parameter) *openapi3.Schema {
	if p == nil || p.Schema == nil || p.Schema.Value == nil {
		return nil
	}
	return unwrapNull(p.Schema.Value)
}

// named finds the parameters of a role: the first of names present, of
// the right type, and how many there were.
func named(q map[string]*openapi3.Parameter, names []string, integer bool) (string, int) {
	found, n := "", 0
	for _, name := range names {
		p := q[name]
		if p == nil {
			continue
		}
		s := paramSchema(p)
		if integer && (s == nil || !s.Type.Is("integer") && !s.Type.Is("number")) {
			continue
		}
		if !integer && s != nil && !s.Type.Is("string") && len(s.Enum) == 0 {
			continue
		}
		if found == "" {
			found = name
		}
		n++
	}
	return found, n
}

// pagingFor works out a list's paging from its parameters and response.
func pagingFor(o op, cols []column) paging {
	q := queryParams(o)
	resp, _ := okResponse(o.op)
	body := unwrapNull(responseSchema(o.op))
	page, nPage := named(q, pageNames, true)
	size, nSize := named(q, sizeNames, true)
	offset, nOffset := named(q, offsetNames, true)
	total := totalPath(body, resp)
	switch {
	case (page != "" || offset != "") && size != "" && total != "" && nPage <= 1 && nSize <= 1 && nOffset <= 1 && !(page != "" && offset != ""):
		return windowFor(o, q, page, offset, size, total, cols)
	case nextPath(body, resp) != "":
		return walk(mapping("strategy", str("link"), "next_path", str(nextPath(body, resp))), "")
	}
	if cp, param, more := cursorFor(body, q); cp != "" {
		w := mapping("strategy", str("cursor"), "cursor_path", str(cp), "cursor_param", str(param))
		if size != "" {
			add(w, "limit_param", str(size))
			add(w, "page_size", intNode(pageSize(q[size])))
		}
		if more != "" {
			add(w, "more_when", str(more))
		}
		return walk(w, "")
	}
	if page == "" && offset == "" {
		return paging{}
	}
	note := ""
	switch {
	case nPage > 1 || nSize > 1 || nOffset > 1 || page != "" && offset != "":
		note = "more than one parameter could page it, so it walks rather than windows"
	case total == "":
		note = "the response has no total, so it walks every page rather than windowing"
	case size == "":
		note = "there's no page-size parameter, so it walks rather than windows"
	}
	w := mapping()
	if page != "" {
		add(w, "strategy", str("page"))
		add(w, "page_param", str(page))
		if start := pageStart(q[page]); start != 1 {
			add(w, "page_start", intNode(start))
		}
	} else {
		add(w, "strategy", str("offset"))
		add(w, "offset_param", str(offset))
	}
	if size != "" {
		add(w, "limit_param", str(size))
		add(w, "page_size", intNode(pageSize(q[size])))
	}
	if total != "" {
		add(w, "total_path", str(total))
	} else if size != "" {
		add(w, "stop_on_short_page", boolean(true))
	}
	return walk(w, note)
}

func walk(w *yaml.Node, note string) paging {
	add(w, "max_pages", intNode(20))
	return paging{paginate: flow(w), walk: true, note: note}
}

// windowFor is a seekable window, with the sort, search and filters the
// spec documents.
func windowFor(o op, q map[string]*openapi3.Parameter, page, offset, size, total string, cols []column) paging {
	w := mapping()
	if page != "" {
		add(w, "page_param", str(page))
		if start := pageStart(q[page]); start != 1 {
			add(w, "page_start", intNode(start))
		}
	} else {
		add(w, "offset_param", str(offset))
	}
	add(w, "limit_param", str(size))
	add(w, "page_size", intNode(pageSize(q[size])))
	add(w, "total_path", str(total))
	pg := paging{window: w, sorted: map[string]bool{}, distinct: map[string][]string{}}

	if sp, _ := named(q, sortNames, false); sp != "" {
		if fields, sorts, desc := sortFields(paramSchema(q[sp]), cols); len(fields) > 0 {
			add(w, "sort_param", str(sp))
			add(w, "sort_fields", flow(strs(fields)))
			if desc {
				add(w, "sort_desc_prefix", str("-"))
			}
			if len(sorts.Content) > 0 {
				add(w, "sorts", sorts)
			}
			for _, f := range fields {
				pg.sorted[f] = true
			}
			for i := 0; i+1 < len(sorts.Content); i += 2 {
				for _, c := range cols {
					if c.title == sorts.Content[i].Value {
						pg.sorted[c.path] = true
					}
				}
			}
		}
	}
	if search, _ := named(q, searchNames, false); search != "" {
		add(w, "search_param", str(search))
		pg.filterable = true
	}
	filters := flow(mapping())
	used := map[string]bool{page: true, offset: true, size: true}
	for _, c := range cols {
		if p := q[c.path]; p != nil && !used[c.path] {
			add(filters, c.title, str(c.path))
			used[c.path] = true
			continue
		}
		if lo, hi := rangePair(q, c.path); lo != "" {
			add(filters, c.title, flow(mapping("gte", str(lo), "lte", str(hi))))
			used[lo], used[hi] = true, true
		}
	}
	if len(filters.Content) > 0 {
		add(w, "filters", filters)
		pg.filterable = true
	}
	return pg
}

// totalPath is where a response says how many items there are: a
// top-level integer named like a total, meta.total / meta.count, or an
// X-Total-Count header.
func totalPath(body *openapi3.Schema, resp *openapi3.Response) string {
	if body != nil && isObject(body) {
		_, props := properties(body)
		for _, n := range totalNames {
			if p := props[n]; p != nil && p.Type.Is("integer") {
				return n
			}
		}
		if meta := props["meta"]; meta != nil {
			_, mp := properties(meta)
			for _, n := range []string{"total", "count"} {
				if p := mp[n]; p != nil && p.Type.Is("integer") {
					return "meta." + n
				}
			}
		}
	}
	if resp != nil {
		for h := range resp.Headers {
			if strings.EqualFold(h, "X-Total-Count") {
				return "header:" + h
			}
		}
	}
	return ""
}

// nextPath is where a response names the next page's URL: a top-level
// string named next (or links.next), or a Link header.
func nextPath(body *openapi3.Schema, resp *openapi3.Response) string {
	if body != nil && isObject(body) {
		_, props := properties(body)
		for _, n := range nextNames {
			if p := props[n]; p != nil && p.Type.Is("string") {
				return n
			}
		}
		if links := props["links"]; links != nil {
			if _, lp := properties(links); lp["next"] != nil {
				return "links.next"
			}
		}
	}
	if resp != nil {
		for h := range resp.Headers {
			if strings.EqualFold(h, "Link") {
				return "link:next"
			}
		}
	}
	return ""
}

// cursorFor finds a cursor walk: a cursor the response names and the
// query parameter it goes back in. Stripe-style lists (starting_after
// and has_more) walk from the last item's id.
func cursorFor(body *openapi3.Schema, q map[string]*openapi3.Parameter) (path, param, more string) {
	var props map[string]*openapi3.Schema
	if body != nil && isObject(body) {
		_, props = properties(body)
	}
	if q["starting_after"] != nil && props["has_more"] != nil {
		return "last:id", "starting_after", "has_more"
	}
	in, _ := named(q, cursorIn, false)
	if in == "" {
		return "", "", ""
	}
	for _, n := range cursorNames {
		if p := props[n]; p != nil && p.Type.Is("string") {
			return n, in, ""
		}
	}
	if meta := props["meta"]; meta != nil {
		if _, mp := properties(meta); mp["next_cursor"] != nil {
			return "meta.next_cursor", in, ""
		}
	}
	return "", "", ""
}

// pageSize is what a size parameter allows: its maximum up to 100, else
// its default, else 100.
func pageSize(p *openapi3.Parameter) int {
	s := paramSchema(p)
	if s == nil {
		return 100
	}
	if s.Max != nil {
		return min(int(*s.Max), 100)
	}
	if d, ok := s.Default.(float64); ok && d > 0 {
		return min(int(d), 100)
	}
	return 100
}

// pageStart is a page parameter's first page: its minimum, else 1.
func pageStart(p *openapi3.Parameter) int {
	if s := paramSchema(p); s != nil && s.Min != nil {
		return int(*s.Min)
	}
	return 1
}

// sortFields reads a sort parameter's enum: the fields a column sorts by
// (its value path), sorts: entries for values that match a column only
// once snake and camel case are ignored, and whether "-" means
// descending. Values that match no column are dropped.
func sortFields(s *openapi3.Schema, cols []column) ([]string, *yaml.Node, bool) {
	sorts := flow(mapping())
	if s == nil || len(s.Enum) == 0 {
		return nil, sorts, false
	}
	var fields []string
	desc := false
	for _, e := range s.Enum {
		v, ok := e.(string)
		if !ok {
			continue
		}
		if strings.HasPrefix(v, "-") {
			desc = true
			v = v[1:]
		}
		for _, c := range cols {
			switch {
			case c.path == v:
				if !slices.Contains(fields, v) {
					fields = append(fields, v)
				}
			case normal(c.path) == normal(v) && get(sorts, c.title) == nil:
				add(sorts, c.title, str(v))
				if !slices.Contains(fields, v) {
					fields = append(fields, v)
				}
			}
		}
	}
	return fields, sorts, desc
}

// normal is a name with case and underscores ignored: createdAt and
// created_at are the same.
func normal(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

// rangePair finds a column's from/to parameters: created_after and
// created_before, min_size and max_size, size_gte and size_lte.
func rangePair(q map[string]*openapi3.Parameter, path string) (string, string) {
	bases := []string{path, strings.TrimSuffix(path, "_at")}
	for _, b := range bases {
		for _, pair := range [][2]string{
			{b + "_after", b + "_before"}, {"min_" + b, "max_" + b}, {b + "_gte", b + "_lte"},
			{b + "_from", b + "_to"}, {b + "_min", b + "_max"}, {b + "_since", b + "_until"},
		} {
			if q[pair[0]] != nil && q[pair[1]] != nil {
				return pair[0], pair[1]
			}
		}
	}
	return "", ""
}

// enumValues are a property's enum values as strings.
func enumValues(s *openapi3.Schema) []string {
	if s == nil {
		return nil
	}
	s = unwrapNull(s)
	var out []string
	for _, e := range s.Enum {
		out = append(out, fmt.Sprint(e))
	}
	return out
}
