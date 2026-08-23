package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// stubWindowed is an Open-Library-shaped endpoint: `{numFound, docs}`,
// paged by ?offset/&limit, filtered by ?q and ?author, sorted by ?sort.
// It records every request's query so tests can assert on the URL the
// source built, not just the rows that came back.
func stubWindowed(t *testing.T, total int, seen *[]url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if seen != nil {
			*seen = append(*seen, q)
		}
		offset := atoiOr(q.Get("offset"), 0)
		limit := atoiOr(q.Get("limit"), 10)
		docs := []any{}
		for i := offset; i < offset+limit && i < total; i++ {
			docs = append(docs, map[string]any{
				"title": fmt.Sprintf("Book %04d", i),
				"n":     i,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"numFound": total,
			"docs":     docs,
		})
	}))
}

func atoiOr(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	return n
}

func newWindowSource(t *testing.T, s *cfg.Source) WindowedSource {
	t.Helper()
	built, err := BuildLeaf(s, nil)
	if err != nil {
		t.Fatalf("BuildLeaf: %v", err)
	}
	ws, ok := built.(WindowedSource)
	if !ok {
		t.Fatalf("http source with window: does not implement WindowedSource")
	}
	return ws
}

// TestFetchWindowSlicesAndTotals is the core contract: one request per
// window, items sliced by root, total read from the raw envelope.
func TestFetchWindowSlicesAndTotals(t *testing.T) {
	var seen []url.Values
	srv := stubWindowed(t, 250, &seen)
	defer srv.Close()

	src := newWindowSource(t, cfg.NewEntry(&cfg.Source{
		Type: "http",
		URL:  srv.URL + "/search",
		Root: "docs",
		Window: &cfg.WindowConfig{
			OffsetParam: "offset",
			LimitParam:  "limit",
			TotalPath:   "numFound",
			SearchParam: "q",
		},
	}))

	page, err := src.FetchWindow(context.Background(), WindowQuery{Offset: 100, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 250 {
		t.Errorf("Total = %d, want 250", page.Total)
	}
	if len(page.Items) != 50 {
		t.Fatalf("len(Items) = %d, want 50", len(page.Items))
	}
	if got := String(page.Items[0], "title"); got != "Book 0100" {
		t.Errorf("first item title = %q, want %q", got, "Book 0100")
	}
	if len(seen) != 1 {
		t.Fatalf("made %d requests, want exactly 1 — a window is one request", len(seen))
	}
	if seen[0].Get("offset") != "100" || seen[0].Get("limit") != "50" {
		t.Errorf("query = %v, want offset=100 limit=50", seen[0])
	}
}

// TestFetchWindowShortLastPage: the tail of the set returns fewer rows
// than asked for, and that is not an error.
func TestFetchWindowShortLastPage(t *testing.T) {
	srv := stubWindowed(t, 120, nil)
	defer srv.Close()

	src := newWindowSource(t, cfg.NewEntry(&cfg.Source{
		Type: "http", URL: srv.URL, Root: "docs",
		Window: &cfg.WindowConfig{OffsetParam: "offset", LimitParam: "limit", TotalPath: "numFound", SearchParam: "q"},
	}))

	page, err := src.FetchWindow(context.Background(), WindowQuery{Offset: 100, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 20 {
		t.Errorf("len(Items) = %d, want 20 (rows 100–119 of 120)", len(page.Items))
	}
	if page.Total != 120 {
		t.Errorf("Total = %d, want 120", page.Total)
	}
}

// TestFetchWindowFiltersAndSort checks the query translation lands in the
// URL: bare text on search_param, scoped columns on their mapped param,
// sort through the sorts: map with the descending prefix applied.
func TestFetchWindowFiltersAndSort(t *testing.T) {
	var seen []url.Values
	srv := stubWindowed(t, 10, &seen)
	defer srv.Close()

	src := newWindowSource(t, cfg.NewEntry(&cfg.Source{
		Type: "http", URL: srv.URL, Root: "docs",
		Window: &cfg.WindowConfig{
			OffsetParam:    "offset",
			LimitParam:     "limit",
			TotalPath:      "numFound",
			SearchParam:    "q",
			Filters:        map[string]string{"Author": "author"},
			SortParam:      "ordering",
			Sorts:          map[string]string{"Year": "first_publish_year"},
			SortDescPrefix: "-",
		},
	}))

	if _, err := src.FetchWindow(context.Background(), WindowQuery{
		Offset:  0,
		Limit:   10,
		Search:  "ring",
		Filters: map[string]string{"Author": "tolkien"},
		Sort:    "Year",
		Desc:    true,
	}); err != nil {
		t.Fatal(err)
	}

	got := seen[0]
	for param, want := range map[string]string{
		"q":        "ring",
		"author":   "tolkien",
		"ordering": "-first_publish_year",
		"offset":   "0",
		"limit":    "10",
	} {
		if got.Get(param) != want {
			t.Errorf("query %s = %q, want %q (full: %v)", param, got.Get(param), want, got)
		}
	}
}

// TestFetchWindowAscendingSortOmitsPrefix — the prefix is descending-only.
func TestFetchWindowAscendingSortOmitsPrefix(t *testing.T) {
	var seen []url.Values
	srv := stubWindowed(t, 10, &seen)
	defer srv.Close()

	src := newWindowSource(t, cfg.NewEntry(&cfg.Source{
		Type: "http", URL: srv.URL, Root: "docs",
		Window: &cfg.WindowConfig{
			OffsetParam: "offset", LimitParam: "limit", SearchParam: "q",
			SortParam: "ordering", SortDescPrefix: "-",
		},
	}))

	if _, err := src.FetchWindow(context.Background(), WindowQuery{Limit: 10, Sort: "Name"}); err != nil {
		t.Fatal(err)
	}
	// Unmapped column: the title travels as-is.
	if got := seen[0].Get("ordering"); got != "Name" {
		t.Errorf("ordering = %q, want %q", got, "Name")
	}
}

// TestFetchWindowPreservesConfiguredQueryParams: params pinned on the
// source URL (an API key, a field mask) must survive into every window.
func TestFetchWindowPreservesConfiguredQueryParams(t *testing.T) {
	var seen []url.Values
	srv := stubWindowed(t, 10, &seen)
	defer srv.Close()

	src := newWindowSource(t, cfg.NewEntry(&cfg.Source{
		Type: "http", URL: srv.URL + "/search?fields=title,n&q=default", Root: "docs",
		Window: &cfg.WindowConfig{OffsetParam: "offset", LimitParam: "limit", SearchParam: "q"},
	}))

	// No search: the URL's own q= is the default and stays.
	if _, err := src.FetchWindow(context.Background(), WindowQuery{Limit: 5}); err != nil {
		t.Fatal(err)
	}
	if got := seen[0].Get("fields"); got != "title,n" {
		t.Errorf("fields = %q, want it preserved from the configured URL", got)
	}
	if got := seen[0].Get("q"); got != "default" {
		t.Errorf("q = %q, want the URL's default preserved when the filter is empty", got)
	}

	// A committed filter replaces the default rather than appending.
	if _, err := src.FetchWindow(context.Background(), WindowQuery{Limit: 5, Search: "hobbit"}); err != nil {
		t.Fatal(err)
	}
	if got := seen[1]["q"]; len(got) != 1 || got[0] != "hobbit" {
		t.Errorf("q = %v, want exactly [hobbit] — a filter replaces the default, it doesn't stack", got)
	}
}

// TestFetchWindowUnknownTotal: no total_path (or a total the response
// doesn't carry) yields -1, which means "can't say", not an error.
func TestFetchWindowUnknownTotal(t *testing.T) {
	srv := stubWindowed(t, 30, nil)
	defer srv.Close()

	for name, w := range map[string]*cfg.WindowConfig{
		"no total_path":      {OffsetParam: "offset", LimitParam: "limit", SearchParam: "q"},
		"total_path missing": {OffsetParam: "offset", LimitParam: "limit", SearchParam: "q", TotalPath: "meta.count"},
	} {
		t.Run(name, func(t *testing.T) {
			src := newWindowSource(t, cfg.NewEntry(&cfg.Source{
				Type: "http", URL: srv.URL, Root: "docs", Window: w,
			}))
			page, err := src.FetchWindow(context.Background(), WindowQuery{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != -1 {
				t.Errorf("Total = %d, want -1", page.Total)
			}
			if len(page.Items) != 10 {
				t.Errorf("len(Items) = %d, want 10 — an unknown total must not block the rows", len(page.Items))
			}
		})
	}
}

// TestWindowedFetchReturnsFirstPage: Fetch still works on a windowed
// source, so wrangl and any non-table consumer get data rather than an
// error. It returns one page — the whole set doesn't exist here.
func TestWindowedFetchReturnsFirstPage(t *testing.T) {
	var seen []url.Values
	srv := stubWindowed(t, 500, &seen)
	defer srv.Close()

	built, err := BuildLeaf(cfg.NewEntry(&cfg.Source{
		Type: "http", URL: srv.URL, Root: "docs",
		Window: &cfg.WindowConfig{OffsetParam: "offset", LimitParam: "limit", PageSize: 25, SearchParam: "q"},
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := built.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := Iter(data)
	if len(items) != 25 {
		t.Errorf("Fetch returned %d items, want 25 (one page_size)", len(items))
	}
	if len(seen) != 1 {
		t.Errorf("Fetch made %d requests, want 1 — it must not walk every page", len(seen))
	}
}

// TestFetchWindowHTTPError surfaces a non-2xx rather than swallowing it
// into an empty page, which would read as "no matching rows".
func TestFetchWindowHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	src := newWindowSource(t, cfg.NewEntry(&cfg.Source{
		Type: "http", URL: srv.URL, Root: "docs",
		Window: &cfg.WindowConfig{OffsetParam: "offset", LimitParam: "limit", SearchParam: "q"},
	}))
	if _, err := src.FetchWindow(context.Background(), WindowQuery{Limit: 10}); err == nil {
		t.Fatal("FetchWindow succeeded on HTTP 429, want an error")
	}
}

// ---- exec windowing ----

// execWindowSource builds a windowed exec source whose command is a
// shell script echoing back the argv it received alongside a page of
// rows, so a test can assert on both what was requested and what landed.
func execWindowSource(t *testing.T, script string, w *cfg.WindowConfig) WindowedSource {
	t.Helper()
	return newWindowSource(t, cfg.NewEntry(&cfg.Source{
		Type:    "exec",
		Command: []string{"sh", "-c", script},
		Root:    "rows",
		Window:  w,
	}))
}

// TestExecFetchWindowRendersArgv is the core of the exec path: offset
// and limit reach the command, and the rows and total come back.
func TestExecFetchWindowRendersArgv(t *testing.T) {
	// Emits {total, rows:[{n: <offset>}]} so the test can read back the
	// offset the command actually saw.
	src := execWindowSource(t,
		`printf '{"total":500,"rows":[{"n":%s,"lim":%s}]}' "${window.offset}" "${window.limit}"`,
		&cfg.WindowConfig{TotalPath: "total", Filters: map[string]string{"Name": "name"}},
	)
	page, err := src.FetchWindow(context.Background(), WindowQuery{Offset: 300, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 500 {
		t.Errorf("Total = %d, want 500", page.Total)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(page.Items))
	}
	if got := String(page.Items[0], "n"); got != "300" {
		t.Errorf("command saw offset %q, want 300", got)
	}
	if got := String(page.Items[0], "lim"); got != "50" {
		t.Errorf("command saw limit %q, want 50", got)
	}
}

// TestExecFetchWindowFiltersAndSort: a scoped term reaches the command
// under its mapped token, bare text under ${window.search}, and the sort
// arrives split into column and direction.
func TestExecFetchWindowFiltersAndSort(t *testing.T) {
	src := execWindowSource(t,
		`printf '{"rows":[{"a":"%s","s":"%s","sort":"%s","dir":"%s"}]}' `+
			`"${window.filters.author}" "${window.search}" "${window.sort}" "${window.sort_dir}"`,
		&cfg.WindowConfig{Filters: map[string]string{"Author": "author"}},
	)
	page, err := src.FetchWindow(context.Background(), WindowQuery{
		Offset: 0, Limit: 10,
		Search:  "ring",
		Filters: map[string]string{"Author": "tolkien"},
		Sort:    "Year",
		Desc:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	it := page.Items[0]
	for path, want := range map[string]string{
		"a": "tolkien", "s": "ring", "sort": "Year", "dir": "desc",
	} {
		if got := String(it, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

// TestRenderWindowArgvDropsEmptyElements covers the drop rule directly —
// it's the part of the design most likely to surprise, so pin it.
func TestRenderWindowArgvDropsEmptyElements(t *testing.T) {
	filters := map[string]string{"Author": "author", "Title": "title"}
	argv := []string{
		"myquery",
		"--offset=${window.offset}",
		"--limit=${window.limit}",
		"--search=${window.search}",
		"--author=${window.filters.author}",
		"--title=${window.filters.title}",
		"--always-here",
	}

	t.Run("empty query drops every optional flag", func(t *testing.T) {
		got := renderWindowArgv(argv, WindowQuery{Offset: 0, Limit: 100}, filters)
		want := []string{"myquery", "--offset=0", "--limit=100", "--always-here"}
		if !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("only the scoped column's flag survives", func(t *testing.T) {
		got := renderWindowArgv(argv, WindowQuery{
			Offset: 100, Limit: 50, Filters: map[string]string{"Author": "tolkien"},
		}, filters)
		want := []string{"myquery", "--offset=100", "--limit=50", "--author=tolkien", "--always-here"}
		if !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("a mixed element survives on its non-empty token", func(t *testing.T) {
		// The `sh -c` shape: one element carrying both an always-present
		// token and an empty one. Dropping it would delete the query.
		mixed := []string{"sh", "-c", "SELECT … LIMIT ${window.limit} AND n LIKE '%${window.search}%'"}
		got := renderWindowArgv(mixed, WindowQuery{Limit: 10}, nil)
		want := []string{"sh", "-c", "SELECT … LIMIT 10 AND n LIKE '%%'"}
		if !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})
}

// An offset of 0 is a real value, not an absence — the first page must
// not lose its flag to the drop rule.
func TestRenderWindowArgvKeepsZeroOffset(t *testing.T) {
	got := renderWindowArgv([]string{"q", "--offset=${window.offset}"}, WindowQuery{Offset: 0, Limit: 10}, nil)
	want := []string{"q", "--offset=0"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q — offset 0 is a value, not an empty token", got, want)
	}
}

// An unmapped filter column contributes nothing: the screen folds those
// terms into Search before they get here, so there's no token to fill.
func TestRenderWindowArgvIgnoresUnmappedFilters(t *testing.T) {
	got := renderWindowArgv(
		[]string{"q", "--limit=${window.limit}", "--author=${window.filters.author}"},
		WindowQuery{Limit: 10, Filters: map[string]string{"Publisher": "penguin"}},
		map[string]string{"Author": "author"},
	)
	want := []string{"q", "--limit=10"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestExecWindowedFetchReturnsFirstPage — same contract as http: Fetch
// still works and yields one page, so wrangl isn't broken.
func TestExecWindowedFetchReturnsFirstPage(t *testing.T) {
	built, err := BuildLeaf(cfg.NewEntry(&cfg.Source{
		Type:    "exec",
		Command: []string{"sh", "-c", `printf '{"rows":[{"n":%s},{"n":2}]}' "${window.offset}"`},
		Root:    "rows",
		Window:  &cfg.WindowConfig{PageSize: 25, Filters: map[string]string{"N": "n"}},
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := built.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(Iter(data)); got != 2 {
		t.Errorf("Fetch returned %d items, want 2", got)
	}
}

// A command that exits non-zero must surface its stderr, not an empty
// page that reads as "no matching rows".
func TestExecFetchWindowCommandFailure(t *testing.T) {
	src := execWindowSource(t,
		`echo "offset ${window.offset} rejected" >&2; exit 3`,
		&cfg.WindowConfig{Filters: map[string]string{"N": "n"}},
	)
	_, err := src.FetchWindow(context.Background(), WindowQuery{Limit: 10})
	if err == nil {
		t.Fatal("FetchWindow succeeded on a failing command")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error %q does not carry the command's stderr", err)
	}
}
