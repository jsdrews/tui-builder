package datasource

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// pagesServer serves a list of 7 items, 3 at a time, read by whatever
// handler maps a request to its page's start index; it records each
// request's query and body.
type pagesServer struct {
	srv      *httptest.Server
	queries  []string
	bodies   []map[string]any
	requests int
}

func newPages(t *testing.T, handle func(r *http.Request, body map[string]any) string) *pagesServer {
	t.Helper()
	ps := &pagesServer{}
	ps.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.requests++
		if ps.requests > 20 {
			t.Fatal("runaway walk")
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		ps.queries = append(ps.queries, r.URL.RawQuery)
		ps.bodies = append(ps.bodies, body)
		io.WriteString(w, handle(r, body))
	}))
	t.Cleanup(ps.srv.Close)
	return ps
}

func ids(from, to int) string {
	out := "["
	for i := from; i < to; i++ {
		if i > from {
			out += ","
		}
		out += strconv.Itoa(i)
	}
	return out + "]"
}

func walk(t *testing.T, d *cfg.Source) []any {
	t.Helper()
	got, err := fetch(t, d)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := got.([]any)
	return items
}

func wantIDs(t *testing.T, got []any, n int) {
	t.Helper()
	if len(got) != n {
		t.Fatalf("got %d items %v, want %d", len(got), got, n)
	}
	for i, v := range got {
		if v != float64(i) {
			t.Fatalf("item %d = %v", i, v)
		}
	}
}

// Slack: an opaque cursor in the query string, "" at the end.
func TestPaginateCursorQuery(t *testing.T) {
	ps := newPages(t, func(r *http.Request, _ map[string]any) string {
		start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		next := ""
		if start+3 < 7 {
			next = strconv.Itoa(start + 3)
		}
		return fmt.Sprintf(`{"items":%s,"meta":{"next_cursor":%q}}`, ids(start, min(start+3, 7)), next)
	})
	got := walk(t, &cfg.Source{Type: "http", URL: ps.srv.URL, Root: "items", Paginate: &cfg.PaginateConfig{
		Strategy: "cursor", CursorPath: "meta.next_cursor", CursorParam: "cursor", LimitParam: "limit", PageSize: 3,
	}})
	wantIDs(t, got, 7)
	if ps.queries[0] != "limit=3" || ps.queries[1] != "cursor=3&limit=3" {
		t.Errorf("queries = %v", ps.queries)
	}
}

// Notion: the cursor goes in the JSON body through patch:, absent on the
// first request, and has_more says when to stop.
func TestPaginateCursorPatchIntoJSON(t *testing.T) {
	ps := newPages(t, func(_ *http.Request, body map[string]any) string {
		start := 0
		if c, ok := body["start_cursor"].(string); ok {
			start, _ = strconv.Atoi(c)
		}
		end := min(start+3, 7)
		return fmt.Sprintf(`{"results":%s,"next_cursor":"%d","has_more":%v}`, ids(start, end), end, end < 7)
	})
	got := walk(t, &cfg.Source{
		Type: "http", URL: ps.srv.URL, Root: "results",
		JSON: map[string]any{"page_size": 3, "filter": map[string]any{"a": 1}},
		Paginate: &cfg.PaginateConfig{
			Strategy: "cursor", CursorPath: "next_cursor", MoreWhen: "has_more",
			Patch: map[string]any{"start_cursor": "${paginate.cursor}"},
		},
	})
	wantIDs(t, got, 7)
	if _, ok := ps.bodies[0]["start_cursor"]; ok {
		t.Errorf("first body %v carries a cursor", ps.bodies[0])
	}
	if ps.bodies[1]["start_cursor"] != "3" || ps.bodies[1]["filter"] == nil {
		t.Errorf("second body = %v", ps.bodies[1])
	}
}

// Stripe: the cursor is the last item's id, and has_more says when to stop.
func TestPaginateCursorFromLastItem(t *testing.T) {
	ps := newPages(t, func(r *http.Request, _ map[string]any) string {
		start := 0
		if a := r.URL.Query().Get("starting_after"); a != "" {
			n, _ := strconv.Atoi(a)
			start = n + 1
		}
		end := min(start+3, 7)
		var data []map[string]int
		for i := start; i < end; i++ {
			data = append(data, map[string]int{"id": i})
		}
		b, _ := json.Marshal(map[string]any{"data": data, "has_more": end < 7})
		return string(b)
	})
	got := walk(t, &cfg.Source{Type: "http", URL: ps.srv.URL, Root: "data", Paginate: &cfg.PaginateConfig{
		Strategy: "cursor", CursorPath: "last:id", CursorParam: "starting_after", MoreWhen: "has_more",
	}})
	if len(got) != 7 || ps.requests != 3 {
		t.Errorf("got %d items in %d requests", len(got), ps.requests)
	}
}

// json-server: offset and limit, with the total in a header.
func TestPaginateOffsetWithHeaderTotal(t *testing.T) {
	ps := newPages(t, nil)
	ps.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.requests++
		start, _ := strconv.Atoi(r.URL.Query().Get("_start"))
		w.Header().Set("X-Total-Count", "7")
		io.WriteString(w, ids(start, min(start+3, 7)))
	})
	got := walk(t, &cfg.Source{Type: "http", URL: ps.srv.URL, Paginate: &cfg.PaginateConfig{
		Strategy: "offset", OffsetParam: "_start", LimitParam: "_limit", PageSize: 3, TotalPath: "header:X-Total-Count",
	}})
	wantIDs(t, got, 7)
	if ps.requests != 3 {
		t.Errorf("%d requests: the total should end the walk without an empty page", ps.requests)
	}
}

// Algolia: zero-based pages, stopping on an expression.
func TestPaginatePageZeroBased(t *testing.T) {
	ps := newPages(t, func(r *http.Request, _ map[string]any) string {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		return fmt.Sprintf(`{"hits":%s,"page":%d,"nbPages":3}`, ids(page*3, min(page*3+3, 7)), page)
	})
	zero := 0
	got := walk(t, &cfg.Source{Type: "http", URL: ps.srv.URL, Root: "hits", Paginate: &cfg.PaginateConfig{
		Strategy: "page", PageParam: "page", PageStart: &zero, MoreWhen: "page + 1 < nbPages",
	}})
	wantIDs(t, got, 7)
}

// An API that clamps the page answers the last page again: the repeat
// guard ends the walk without the duplicate.
func TestPaginateRepeatGuard(t *testing.T) {
	ps := newPages(t, func(r *http.Request, _ map[string]any) string {
		page, _ := strconv.Atoi(r.URL.Query().Get("_page"))
		page = min(page, 3)
		return ids((page-1)*3, min(page*3, 7))
	})
	got := walk(t, &cfg.Source{Type: "http", URL: ps.srv.URL, Paginate: &cfg.PaginateConfig{
		Strategy: "page", PageParam: "_page",
	}})
	wantIDs(t, got, 7)
}

// Discord: stop on a page shorter than asked for.
func TestPaginateStopOnShortPage(t *testing.T) {
	ps := newPages(t, func(r *http.Request, _ map[string]any) string {
		start, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		return ids(start, min(start+3, 7))
	})
	walk(t, &cfg.Source{Type: "http", URL: ps.srv.URL, Paginate: &cfg.PaginateConfig{
		Strategy: "offset", OffsetParam: "offset", PageSize: 3, StopOnShortPage: true,
	}})
	if ps.requests != 3 {
		t.Errorf("%d requests, want 3 (the short third page ends it)", ps.requests)
	}
}

// A cursor that comes back unchanged ends the walk rather than looping.
func TestPaginateRepeatedCursorStops(t *testing.T) {
	ps := newPages(t, func(*http.Request, map[string]any) string { return `{"items":[1],"next":"same"}` })
	walk(t, &cfg.Source{Type: "http", URL: ps.srv.URL, Root: "items", Paginate: &cfg.PaginateConfig{
		Strategy: "cursor", CursorPath: "next", CursorParam: "c",
	}})
	if ps.requests != 2 {
		t.Errorf("%d requests, want 2", ps.requests)
	}
}

func TestRenderWalkPatchTypes(t *testing.T) {
	got := renderWalkPatch(map[string]any{
		"search_after": "${paginate.cursor}",
		"from":         "${paginate.offset}",
		"label":        "p${paginate.page}",
	}, walkPos{cursor: []any{float64(5), "x"}, offset: 30, page: 4})
	want := map[string]any{"search_after": []any{float64(5), "x"}, "from": 30, "label": "p4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}
