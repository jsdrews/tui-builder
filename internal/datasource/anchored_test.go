package datasource

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// esStub answers like Elasticsearch _search with search_after: 50 log
// docs with ts 1..50, a sort array [ts, id] as each hit's cursor.
type esStub struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
	query  []string
}

func newESStub() *esStub {
	s := &esStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.query = append(s.query, r.URL.RawQuery)
		s.mu.Unlock()

		desc := strings.Contains(string(raw), `"desc"`)
		size := int(body["size"].(float64))
		var after float64 = -1
		if sa, ok := body["search_after"].([]any); ok {
			after = sa[0].(float64)
		}
		var hits []any
		for i := 1; i <= 50; i++ {
			ts := float64(i)
			if after >= 0 && ((desc && ts >= after) || (!desc && ts <= after)) {
				continue
			}
			hits = append(hits, map[string]any{"_id": i, "msg": "line " + strings.Repeat("x", i%3), "sort": []any{ts, float64(i)}})
		}
		sort.SliceStable(hits, func(a, b int) bool {
			ta := hits[a].(map[string]any)["sort"].([]any)[0].(float64)
			tb := hits[b].(map[string]any)["sort"].([]any)[0].(float64)
			if desc {
				return ta > tb
			}
			return ta < tb
		})
		if len(hits) > size {
			hits = hits[:size]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
	}))
	return s
}

func esSource(t *testing.T, url string) AnchoredSource {
	t.Helper()
	s, err := newHTTP(&cfg.Source{
		Type: "http", Method: "POST", URL: url + "/logs/_search", Root: "hits.hits",
		Body: `{"query": {"match_all": {}}, "size": ${window.limit}}`,
		Window: &cfg.WindowConfig{
			Cursor: "sort",
			Older:  map[string]any{"sort": []any{map[string]any{"ts": "desc"}}, "search_after": "${window.cursor}"},
			Newer:  map[string]any{"sort": []any{map[string]any{"ts": "asc"}}, "search_after": "${window.cursor}"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.(AnchoredSource)
}

func TestFetchEdgeWalksFromTheNewest(t *testing.T) {
	srv := newESStub()
	defer srv.Close()
	src := esSource(t, srv.URL)

	page, err := src.FetchEdge(context.Background(), EdgeQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 10 || !page.More {
		t.Fatalf("got %d items, more=%v; want 10 and more", len(page.Items), page.More)
	}
	if got := page.Cursors[0]; got != "[41,41]" {
		t.Errorf("oldest item's cursor = %s, want [41,41] (the page is the newest 10, oldest first)", got)
	}
	if got := page.Cursors[9]; got != "[50,50]" {
		t.Errorf("newest item's cursor = %s, want [50,50]", got)
	}
	first := srv.bodies[0]
	if _, ok := first["search_after"]; ok {
		t.Error("the first request has no cursor, so search_after should be left out")
	}
	if first["size"] != float64(11) {
		t.Errorf("size = %v, want 11 (one more than the page, to learn whether there's more)", first["size"])
	}
	if _, ok := first["query"]; !ok {
		t.Error("the body's own fields were lost in the merge")
	}

	older, err := src.FetchEdge(context.Background(), EdgeQuery{Limit: 10, Cursor: page.Cursors[0]})
	if err != nil {
		t.Fatal(err)
	}
	if older.Cursors[0] != "[31,31]" || older.Cursors[9] != "[40,40]" {
		t.Errorf("older page = %s..%s, want [31,31]..[40,40]", older.Cursors[0], older.Cursors[9])
	}
	sa, _ := srv.bodies[1]["search_after"].([]any)
	if len(sa) != 2 || sa[0] != float64(41) {
		t.Errorf("search_after = %v, want the edge cursor [41, 41] as raw JSON", srv.bodies[1]["search_after"])
	}

	newer, err := src.FetchEdge(context.Background(), EdgeQuery{Limit: 10, Newer: true, Cursor: page.Cursors[9]})
	if err != nil {
		t.Fatal(err)
	}
	if len(newer.Items) != 0 || newer.More {
		t.Errorf("past the newest: %d items, more=%v; want none", len(newer.Items), newer.More)
	}
}

func TestFetchEdgeOnAGetAPIUsesQueryParams(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()
	s, err := newHTTP(&cfg.Source{
		Type: "http", URL: srv.URL + "/events?kind=deploy", Root: "data",
		Window: &cfg.WindowConfig{
			Cursor: "id", LimitParam: "limit", SearchParam: "q",
			Older: map[string]any{"before": "${window.cursor}", "order": "desc"},
			Newer: map[string]any{"after": "${window.cursor}", "order": "asc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	src := s.(AnchoredSource)
	if _, err := src.FetchEdge(context.Background(), EdgeQuery{Limit: 20, Search: "web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.FetchEdge(context.Background(), EdgeQuery{Limit: 20, Cursor: `"evt_9"`}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind=deploy", "limit=21", "order=desc", "q=web"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("first request %q is missing %s", got[0], want)
		}
	}
	if strings.Contains(got[0], "before=") {
		t.Errorf("first request %q sent an empty cursor", got[0])
	}
	if !strings.Contains(got[1], "before=evt_9") {
		t.Errorf("second request %q should send the string cursor unquoted", got[1])
	}
}

func TestFetchEdgeOverExec(t *testing.T) {
	s, err := newExec(&cfg.Source{
		Type: "exec",
		Command: []string{"sh", "-c",
			`printf '{"items":[{"t":"c","q":"%s"},{"t":"b"},{"t":"a"}]}' "${window.dir}:${window.cursor}:${window.limit}:${window.search}"`},
		Root:   "items",
		Window: &cfg.WindowConfig{Cursor: "t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.(AnchoredSource).FetchEdge(context.Background(), EdgeQuery{Limit: 2, Cursor: `"d"`, Search: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !page.More || len(page.Items) != 2 {
		t.Fatalf("got %d items, more=%v; want 2 and more (3 came back for a limit of 2)", len(page.Items), page.More)
	}
	if page.Cursors[0] != `"b"` || page.Cursors[1] != `"c"` {
		t.Errorf("cursors = %v, want [\"b\" \"c\"]: the two nearest the cursor, oldest first", page.Cursors)
	}
	if q := page.Items[1].(map[string]any)["q"]; q != "older:d:3:x" {
		t.Errorf("command saw %q, want older:d:3:x", q)
	}
}
