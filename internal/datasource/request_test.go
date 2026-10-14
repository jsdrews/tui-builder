package datasource

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// captured is what a test server saw of one request.
type captured struct {
	method, query, body, contentType, userAgent string
	values                                      map[string][]string
}

// recordingServer answers every request with reply and records it.
func recordingServer(t *testing.T, reply string) (*httptest.Server, *[]captured) {
	t.Helper()
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, captured{
			method:      r.Method,
			query:       r.URL.RawQuery,
			values:      r.URL.Query(),
			body:        string(b),
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
		})
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func fetchOnce(t *testing.T, d *cfg.Source) {
	t.Helper()
	s, err := newHTTP(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPQueryOverURL(t *testing.T) {
	srv, got := recordingServer(t, `[]`)
	fetchOnce(t, &cfg.Source{
		Type: "http",
		URL:  srv.URL + "/?a=1&b=2&c=3",
		Query: map[string]cfg.QueryValue{
			"b":   {"20"},
			"tag": {"x", "", "y"},
			"c":   {""},
			"q":   {"a b&c"},
		},
	})
	want := map[string][]string{"a": {"1"}, "b": {"20"}, "tag": {"x", "y"}, "q": {"a b&c"}}
	if v := (*got)[0].values; !reflect.DeepEqual(v, want) {
		t.Errorf("query = %v, want %v", v, want)
	}
}

func TestHTTPURLSentAsWrittenWithoutQuery(t *testing.T) {
	srv, got := recordingServer(t, `[]`)
	fetchOnce(t, &cfg.Source{Type: "http", URL: srv.URL + "/?z=1&a=2"})
	if q := (*got)[0].query; q != "z=1&a=2" {
		t.Errorf("query = %q: a url without query: must go out as written", q)
	}
}

func TestHTTPJSONBody(t *testing.T) {
	srv, got := recordingServer(t, `[]`)
	fetchOnce(t, &cfg.Source{
		Type: "http",
		URL:  srv.URL,
		JSON: map[string]any{"query": map[string]any{"match": "it's"}, "size": 10},
	})
	r := (*got)[0]
	if r.method != http.MethodPost {
		t.Errorf("method = %s, want POST for a request with a body", r.method)
	}
	if r.contentType != "application/json" {
		t.Errorf("content-type = %q", r.contentType)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.body), &body); err != nil || body["size"] != float64(10) {
		t.Errorf("body = %s (%v)", r.body, err)
	}
}

func TestHTTPFormBody(t *testing.T) {
	srv, got := recordingServer(t, `[]`)
	fetchOnce(t, &cfg.Source{
		Type: "http",
		URL:  srv.URL,
		Form: map[string]cfg.QueryValue{"grant_type": {"client_credentials"}, "scope": {"a", "b"}, "drop": {""}},
	})
	r := (*got)[0]
	if r.contentType != "application/x-www-form-urlencoded" || r.method != http.MethodPost {
		t.Errorf("content-type = %q, method = %s", r.contentType, r.method)
	}
	if r.body != "grant_type=client_credentials&scope=a&scope=b" {
		t.Errorf("body = %q", r.body)
	}
}

func TestHTTPExplicitHeadersWin(t *testing.T) {
	srv, got := recordingServer(t, `[]`)
	fetchOnce(t, &cfg.Source{
		Type:    "http",
		URL:     srv.URL,
		Method:  "PUT",
		JSON:    map[string]any{"a": 1},
		Headers: map[string]string{"Content-Type": "application/vnd.api+json", "User-Agent": "mine/1"},
	})
	r := (*got)[0]
	if r.method != "PUT" || r.contentType != "application/vnd.api+json" || r.userAgent != "mine/1" {
		t.Errorf("got %+v", r)
	}
}

func TestHTTPDefaultUserAgent(t *testing.T) {
	srv, got := recordingServer(t, `[]`)
	fetchOnce(t, &cfg.Source{Type: "http", URL: srv.URL})
	if r := (*got)[0]; r.userAgent != UserAgent || r.method != http.MethodGet {
		t.Errorf("user-agent = %q, method = %s", r.userAgent, r.method)
	}
}

// An Anchored window's patch deep-merges into json:, and its tokens take
// typed values there.
func TestHTTPAnchoredPatchMergesIntoJSON(t *testing.T) {
	srv, got := recordingServer(t, `{"hits":[]}`)
	s, err := newHTTP(&cfg.Source{
		Type: "http",
		URL:  srv.URL,
		Root: "hits",
		JSON: map[string]any{"size": "${window.limit}", "query": map[string]any{"match_all": map[string]any{}}},
		Window: &cfg.WindowConfig{
			Cursor: "sort",
			Older:  map[string]any{"sort": []any{"desc"}, "search_after": "${window.cursor}", "query": map[string]any{"extra": true}},
			Newer:  map[string]any{"sort": []any{"asc"}, "search_after": "${window.cursor}"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.(AnchoredSource).FetchEdge(context.Background(), EdgeQuery{Cursor: `[5,"x"]`, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte((*got)[0].body), &body); err != nil {
		t.Fatal(err)
	}
	if body["size"] != float64(11) {
		t.Errorf("size = %#v, want the number 11", body["size"])
	}
	if !reflect.DeepEqual(body["search_after"], []any{float64(5), "x"}) {
		t.Errorf("search_after = %#v", body["search_after"])
	}
	q := body["query"].(map[string]any)
	if _, ok := q["match_all"]; !ok || q["extra"] != true {
		t.Errorf("query = %v, want the patch merged in, not replacing", q)
	}
	if (*got)[0].query != "" {
		t.Errorf("query string = %q: with a body, the patch goes into it", (*got)[0].query)
	}
}
