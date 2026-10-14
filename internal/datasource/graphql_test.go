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

const reposQuery = `query($after: String) { viewer { repositories(first: 2, after: $after) {
  nodes { name } pageInfo { endCursor hasNextPage } } } }`

// relayServer serves 5 repositories, 2 per page, Relay-style, and
// records each request's body.
func relayServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request: %s %q %s", r.Method, r.Header.Get("Content-Type"), b)
		}
		bodies = append(bodies, body)
		start := 0
		if vars, ok := body["variables"].(map[string]any); ok {
			if after, ok := vars["after"].(string); ok {
				start, _ = strconv.Atoi(after)
			}
		}
		end := min(start+2, 5)
		var nodes []map[string]string
		for i := start; i < end; i++ {
			nodes = append(nodes, map[string]string{"name": fmt.Sprintf("repo%d", i)})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"viewer": map[string]any{"repositories": map[string]any{
			"nodes":    nodes,
			"pageInfo": map[string]any{"endCursor": strconv.Itoa(end), "hasNextPage": end < 5},
		}}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

// The spec's worked example: Relay pagination with nothing
// GraphQL-specific beyond the shorthand.
func TestGraphQLRelayWalk(t *testing.T) {
	srv, bodies := relayServer(t)
	got, err := fetch(t, &cfg.Source{
		Type:    "http",
		URL:     srv.URL,
		GraphQL: &cfg.GraphQLSpec{Query: reposQuery},
		Root:    "data.viewer.repositories.nodes",
		Paginate: &cfg.PaginateConfig{
			Strategy:   "cursor",
			CursorPath: "data.viewer.repositories.pageInfo.endCursor",
			MoreWhen:   "data.viewer.repositories.pageInfo.hasNextPage",
			Patch:      map[string]any{"variables": map[string]any{"after": "${paginate.cursor}"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got.([]any)); n != 5 || len(*bodies) != 3 {
		t.Fatalf("%d repos in %d requests", n, len(*bodies))
	}
	first, second := (*bodies)[0], (*bodies)[1]
	vars, _ := first["variables"].(map[string]any)
	if first["query"] != reposQuery || vars["after"] != nil {
		t.Errorf("first body = %v: the query as written, and no cursor yet", first)
	}
	if !reflect.DeepEqual(second["variables"], map[string]any{"after": "2"}) {
		t.Errorf("second variables = %v", second["variables"])
	}
}

// A 200 with errors in it is an error; the data that came with it is kept.
func TestGraphQLErrorsOn200(t *testing.T) {
	body := `{"data":{"viewer":{"login":"me"},"repo":null},"errors":[{"message":"Could not resolve to a Repository"}]}`
	srv := replyWith(t, 200, nil, body)
	got, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Root: "data.viewer", GraphQL: &cfg.GraphQLSpec{Query: "{ viewer { login } }"}})
	if err == nil || err.Error() != "HTTP 200 OK — Could not resolve to a Repository" {
		t.Errorf("err = %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"login": "me"}) {
		t.Errorf("partial data = %v", got)
	}

	// An explicit error_path replaces the default.
	_, err = fetch(t, &cfg.Source{Type: "http", URL: srv.URL, ErrorPath: "extensions.code", GraphQL: &cfg.GraphQLSpec{Query: "{ x }"}})
	if err != nil {
		t.Errorf("err = %v, want none: error_path points elsewhere", err)
	}
}
