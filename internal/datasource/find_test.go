package datasource

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// An Anchored find: the patch's tokens become query parameters, and the
// match's cursor comes back JSON-encoded like an edge's.
func TestFindItemOnAnAnchoredGetAPI(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
			map[string]any{"id": "evt_4", "msg": "deploy web"},
		}})
	}))
	defer srv.Close()
	s, err := newHTTP(&cfg.Source{
		Type: "http", URL: srv.URL + "/events", Root: "data",
		Window: &cfg.WindowConfig{
			Cursor: "id", LimitParam: "limit", SearchParam: "q",
			Find: &cfg.FindConfig{Params: map[string]any{
				"text": "${window.term}", "before": "${window.cursor}", "dir": "${window.dir}", "limit": "${window.limit}",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.(FindingSource).FindItem(context.Background(), FindQuery{Term: "deploy", Cursor: `"evt_9"`, Search: "web"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"text=deploy", "before=evt_9", "dir=older", "limit=1", "q=web"} {
		if !strings.Contains(got, want) {
			t.Errorf("request %q is missing %s", got, want)
		}
	}
	if !res.Found || res.Cursor != `"evt_4"` {
		t.Errorf("result = %+v, want the match's cursor \"evt_4\"", res)
	}
}

// A Seekable find posts its patch merged into the body and reads the
// match's position; an empty reply is no match.
func TestFindItemOnASeekablePostAPI(t *testing.T) {
	var body map[string]any
	empty := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		hits := []any{map[string]any{"title": "The Hobbit", "rank": 41}}
		if empty {
			hits = []any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
	}))
	defer srv.Close()
	s, err := newHTTP(&cfg.Source{
		Type: "http", Method: "POST", URL: srv.URL + "/search", Root: "hits",
		Body: `{"index": "books"}`,
		Window: &cfg.WindowConfig{
			OffsetParam: "offset", LimitParam: "limit", SearchParam: "q",
			Find: &cfg.FindConfig{
				Body:     map[string]any{"match": "${window.term}", "after": "${window.from}"},
				Position: "rank",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := s.(FindingSource)
	res, err := f.FindItem(context.Background(), FindQuery{Term: "hobbit", Newer: true, From: 12})
	if err != nil {
		t.Fatal(err)
	}
	if body["index"] != "books" || body["match"] != "hobbit" || body["after"] != "12" {
		t.Errorf("body = %v, want the patch merged into the source's body", body)
	}
	if !res.Found || res.Position != 41 {
		t.Errorf("result = %+v, want position 41", res)
	}
	empty = true
	if res, err := f.FindItem(context.Background(), FindQuery{Term: "nope"}); err != nil || res.Found {
		t.Errorf("an empty reply should be no match, got %+v, %v", res, err)
	}
}
