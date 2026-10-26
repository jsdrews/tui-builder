package datasource

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// stubPaged answers `{total, entries}` to ?page=N&per_page=M, pages from
// 1, capping per_page at maxPer.
func stubPaged(t *testing.T, total, maxPer int, seen *[]url.Values) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*seen = append(*seen, q)
		page, per := atoiOr(q.Get("page"), 1), min(atoiOr(q.Get("per_page"), 10), maxPer)
		entries := []any{}
		for i := (page - 1) * per; i < page*per && i < total; i++ {
			entries = append(entries, map[string]any{"n": i})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": total, "entries": entries})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pagedSource(url string, pageSize int) *cfg.Source {
	return &cfg.Source{Type: "http", URL: url, Root: "entries", Window: &cfg.WindowConfig{
		PageParam: "page", LimitParam: "per_page", PageSize: pageSize, TotalPath: "total",
	}}
}

func ns(items []any) string {
	var out []string
	for _, it := range items {
		out = append(out, fmt.Sprint(it.(map[string]any)["n"]))
	}
	return strings.Join(out, ",")
}

// A window spanning two pages is fetched as two page requests, joined
// and trimmed to the rows asked for.
func TestPagedWindowSpansPages(t *testing.T) {
	var seen []url.Values
	ws := newWindowSource(t, pagedSource(stubPaged(t, 25, 100, &seen).URL, 10))
	page, err := ws.FetchWindow(t.Context(), WindowQuery{Offset: 10, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 25 || len(page.Items) != 15 || ns(page.Items[:2]) != "10,11" {
		t.Errorf("total %d, items %s", page.Total, ns(page.Items))
	}
	if len(seen) != 2 || seen[0].Get("page") != "2" || seen[1].Get("page") != "3" || seen[0].Get("per_page") != "10" {
		t.Errorf("requests = %v", seen)
	}
}

// page_start: 0 numbers pages from zero; an unaligned offset drops the
// rows before it.
func TestPagedWindowPageStartAndUnaligned(t *testing.T) {
	var seen []url.Values
	src := pagedSource(stubPaged(t, 50, 100, &seen).URL, 10)
	zero := 0
	src.Window.PageStart = &zero
	ws := newWindowSource(t, src)
	// The stub pages from 1, so page 0 reads as page 1; what matters is
	// the number sent.
	page, err := ws.FetchWindow(t.Context(), WindowQuery{Offset: 13, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if seen[0].Get("page") != "1" || len(page.Items) != 4 {
		t.Errorf("page sent %q, %d items", seen[0].Get("page"), len(page.Items))
	}
}

// A server that caps per_page below page_size is said once.
func TestPagedWindowClampNotice(t *testing.T) {
	notices := captureNotices(t)
	var seen []url.Values
	ws := newWindowSource(t, pagedSource(stubPaged(t, 500, 50, &seen).URL, 100))
	for range 2 {
		if _, err := ws.FetchWindow(t.Context(), WindowQuery{Offset: 0, Limit: 100}); err != nil {
			t.Fatal(err)
		}
	}
	if len(*notices) != 1 || !strings.HasSuffix((*notices)[0], "page_size 100 exceeds what the server returns (50)") {
		t.Errorf("notices = %q", *notices)
	}
}

// A total_path the response doesn't carry is said once.
func TestWindowMissingTotalNotice(t *testing.T) {
	notices := captureNotices(t)
	var seen []url.Values
	src := pagedSource(stubPaged(t, 5, 100, &seen).URL, 10)
	src.Window.TotalPath = "meta.total"
	ws := newWindowSource(t, src)
	for range 2 {
		page, err := ws.FetchWindow(t.Context(), WindowQuery{Offset: 0, Limit: 10})
		if err != nil || page.Total != -1 {
			t.Fatalf("total %d, err %v", page.Total, err)
		}
	}
	if len(*notices) != 1 || !strings.Contains((*notices)[0], `total_path "meta.total" not found`) {
		t.Errorf("notices = %q", *notices)
	}
}

// A comparing term goes out as the param FilterParams names for its
// column and comparison, beside the contains filters; one with no param
// is never sent.
func TestWindowComparesBecomeParams(t *testing.T) {
	var seen []url.Values
	srv := stubPaged(t, 5, 100, &seen)
	src := pagedSource(srv.URL, 10)
	src.Window.Filters = map[string]string{"Name": "name__icontains"}
	src.Window.FilterParams = map[string]map[string]string{
		"Name": {"contains": "name__icontains"},
		"Year": {"gte": "year__gte", "lt": "year__lt"},
	}
	ws := newWindowSource(t, src)
	_, err := ws.FetchWindow(t.Context(), WindowQuery{Limit: 10,
		Filters:  map[string]string{"Name": "tolk"},
		Compares: []Comparison{{Title: "Year", Op: "gte", Value: "1950"}, {Title: "Year", Op: "eq", Value: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := seen[len(seen)-1]
	if q.Get("name__icontains") != "tolk" || q.Get("year__gte") != "1950" {
		t.Errorf("query = %v", q)
	}
	for k := range q {
		if strings.HasPrefix(k, "year") && k != "year__gte" {
			t.Errorf("unexpected param %s in %v", k, q)
		}
	}
}
