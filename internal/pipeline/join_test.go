package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// TestJoinSeparateEmit drives users → per-user posts via a lookup
// source parameterized by user_id. Each driver row becomes a
// {row, posts} pair in the output.
func TestJoinSeparateEmit(t *testing.T) {
	srv := stubPostsServer(t, map[int][]string{
		1: {"hello"},
		2: {"first", "second"},
	})
	defer srv.Close()

	driver := &fakeSource{data: []any{
		map[string]any{"id": 1, "name": "Ada"},
		map[string]any{"id": 2, "name": "Grace"},
	}}
	sourceDefs := map[string]*cfg.Source{
		"posts": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{"user_id": {Type: "int", Required: true}}, URL: srv.URL + "/users/${params.user_id}/posts"}),
	}
	defs := map[string]*cfg.Source{
		"users_with_posts": cfg.NewEntry(&cfg.Source{Type: "join", Driver: cfg.JoinDriver{From: "drv"},
			Lookups: map[string]cfg.JoinLookup{
				"posts": {From: "posts", On: map[string]string{"user_id": "id"}},
			}},
		),
	}
	reg, err := Build(map[string]ds.Source{"drv": driver}, mergeEntries(sourceDefs, defs), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("users_with_posts").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := got.([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 enriched rows, got %d (%v)", len(items), items)
	}
	first := items[0].(map[string]any)
	if first["row"].(map[string]any)["name"] != "Ada" {
		t.Errorf("first.row.name = %v, want Ada", first["row"])
	}
	posts, _ := first["posts"].([]any)
	if len(posts) != 1 || posts[0] != "hello" {
		t.Errorf("first.posts = %v, want [hello]", first["posts"])
	}
	second := items[1].(map[string]any)
	posts2, _ := second["posts"].([]any)
	if len(posts2) != 2 {
		t.Errorf("second.posts = %v, want 2 items", second["posts"])
	}
}

// TestJoinMergedEmit returns one flat object per row — lookup fields
// merge into the driver row.
func TestJoinMergedEmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a JSON object (not a list) so merged is well-defined.
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/1") {
			_, _ = w.Write([]byte(`{"title":"Hello","views":42}`))
			return
		}
		_, _ = w.Write([]byte(`{"title":"World","views":7}`))
	}))
	defer srv.Close()

	driver := &fakeSource{data: []any{
		map[string]any{"id": 1, "name": "Ada"},
	}}
	sourceDefs := map[string]*cfg.Source{
		"profile": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{"user_id": {Type: "int", Required: true}}, URL: srv.URL + "/users/${params.user_id}"}),
	}
	defs := map[string]*cfg.Source{
		"merged": cfg.NewEntry(&cfg.Source{Type: "join", Driver: cfg.JoinDriver{From: "drv"},
			Lookups: map[string]cfg.JoinLookup{
				"profile": {From: "profile", On: map[string]string{"user_id": "id"}},
			},
			Emit: "merged"},
		),
	}
	reg, err := Build(map[string]ds.Source{"drv": driver}, mergeEntries(sourceDefs, defs), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("merged").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := got.([]any)
	row := items[0].(map[string]any)
	// Driver fields preserved
	if row["name"] != "Ada" {
		t.Errorf("merged row missing driver field: %v", row)
	}
	// Lookup fields injected
	if row["title"] != "Hello" {
		t.Errorf("merged row missing lookup field: %v", row)
	}
}

// TestJoinFailsOnLookupErrorByDefault — when a lookup errors and
// on_error is unset (= fail), the whole Fetch errors.
func TestJoinFailsOnLookupErrorByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // every call fails
	}))
	defer srv.Close()

	driver := &fakeSource{data: []any{map[string]any{"id": 1}}}
	sourceDefs := map[string]*cfg.Source{
		"flaky": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{"id": {Type: "int", Required: true}}, URL: srv.URL + "/${params.id}"}),
	}
	defs := map[string]*cfg.Source{
		"j": cfg.NewEntry(&cfg.Source{Type: "join", Driver: cfg.JoinDriver{From: "drv"},
			Lookups: map[string]cfg.JoinLookup{
				"data": {From: "flaky", On: map[string]string{"id": "id"}},
			}},
		),
	}
	reg, err := Build(map[string]ds.Source{"drv": driver}, mergeEntries(sourceDefs, defs), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Get("j").Fetch(context.Background())
	if err == nil {
		t.Errorf("expected join to fail on lookup error")
	}
	if !strings.Contains(err.Error(), "lookup data") {
		t.Errorf("error should name the failing lookup; got: %v", err)
	}
}

// TestJoinSkipDropsFailedRows — on_error: skip drops rows whose
// lookup failed but lets the rest through.
func TestJoinSkipDropsFailedRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/1") {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer srv.Close()

	driver := &fakeSource{data: []any{
		map[string]any{"id": 1, "name": "fails"},
		map[string]any{"id": 2, "name": "works"},
	}}
	sourceDefs := map[string]*cfg.Source{
		"d": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{"id": {Type: "int", Required: true}}, URL: srv.URL + "/${params.id}"}),
	}
	defs := map[string]*cfg.Source{
		"j": cfg.NewEntry(&cfg.Source{Type: "join", Driver: cfg.JoinDriver{From: "drv"},
			Lookups: map[string]cfg.JoinLookup{"data": {From: "d", On: map[string]string{"id": "id"}}},
			OnError: "skip"},
		),
	}
	reg, err := Build(map[string]ds.Source{"drv": driver}, mergeEntries(sourceDefs, defs), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("j").Fetch(context.Background())
	if err != nil {
		t.Fatalf("skip should not fail when some rows survive: %v", err)
	}
	items := got.([]any)
	if len(items) != 1 {
		t.Fatalf("want 1 surviving row, got %d (%v)", len(items), items)
	}
}

// TestJoinSubscribeReturnsNotStreaming — joins are snapshot-only in v1.
func TestJoinSubscribeReturnsNotStreaming(t *testing.T) {
	driver := &fakeSource{data: []any{}}
	sourceDefs := map[string]*cfg.Source{
		"d": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{"id": {Type: "int", Required: true}}, URL: "http://x/${params.id}"}),
	}
	defs := map[string]*cfg.Source{
		"j": cfg.NewEntry(&cfg.Source{Type: "join", Driver: cfg.JoinDriver{From: "drv"},
			Lookups: map[string]cfg.JoinLookup{"x": {From: "d", On: map[string]string{"id": "id"}}}},
		),
	}
	reg, err := Build(map[string]ds.Source{"drv": driver}, mergeEntries(sourceDefs, defs), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Get("j").(ds.StreamingSource).Subscribe(context.Background())
	if !errors.Is(err, ds.ErrNotStreaming) {
		t.Errorf("join should return ErrNotStreaming; got %v", err)
	}
}

// TestJoinValidatorRejectsPipelineLookup — v1 only accepts SOURCE
// lookups; pipelines as lookups are deferred.
func TestJoinValidatorRejectsPipelineLookup(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"d": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),

		"some_passthrough": cfg.NewEntry(&cfg.Source{Type: "passthrough", From: "d"}),
		"bad_join": cfg.NewEntry(&cfg.Source{Type: "join", Driver: cfg.JoinDriver{From: "d"},
			Lookups: map[string]cfg.JoinLookup{"x": {From: "some_passthrough", On: map[string]string{"k": "id"}}}},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"c": {Type: "list", Items: []string{"x"}},
	},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "c"}}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject pipeline-as-lookup")
	}
}

// TestJoinValidatorRejectsUndeclaredOnParam — every key in on: must
// match a parameter declared on the lookup source.
func TestJoinValidatorRejectsUndeclaredOnParam(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"d": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),
		"l": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{"namespace": {Required: true}}, URL: "x"}),

		"j": cfg.NewEntry(&cfg.Source{Type: "join", Driver: cfg.JoinDriver{From: "d"},
			Lookups: map[string]cfg.JoinLookup{
				// `name` isn't declared on `l` — should be rejected.
				"data": {From: "l", On: map[string]string{"name": "metadata.name"}},
			}},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"c": {Type: "list", Items: []string{"x"}},
	},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "c"}}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject undeclared on: param")
	}
}

// stubPostsServer returns a server that responds to
// /users/{id}/posts with a JSON list of strings.
func stubPostsServer(t *testing.T, posts map[int][]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var id int
		_, _ = fmtSscanf(r.URL.Path, "/users/%d/posts", &id)
		body := `[`
		for i, p := range posts[id] {
			if i > 0 {
				body += ","
			}
			body += `"` + p + `"`
		}
		body += `]`
		_, _ = w.Write([]byte(body))
	}))
}

// TestJoinLookupCacheDedupsDuplicateParams pins feature G's win: when
// two driver rows produce identical lookup params, the upstream only
// fires once. Without the cache the stub server would see two calls;
// with the cache it sees one.
func TestJoinLookupCacheDedupsDuplicateParams(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["hello"]`))
	}))
	defer srv.Close()

	// Two driver rows resolve to the same user_id → same lookup params.
	driver := &fakeSource{data: []any{
		map[string]any{"id": 1, "name": "Ada"},
		map[string]any{"id": 1, "name": "Ada-again"},
	}}
	sourceDefs := map[string]*cfg.Source{
		"posts": cfg.NewEntry(&cfg.Source{
			Type:       "http",
			Parameters: map[string]*cfg.Parameter{"user_id": {Type: "int", Required: true}},
			URL:        srv.URL + "/users/${params.user_id}/posts",
		}),
	}
	defs := map[string]*cfg.Source{
		"users_with_posts": cfg.NewEntry(&cfg.Source{
			Type: "join", Driver: cfg.JoinDriver{From: "drv"},
			Lookups: map[string]cfg.JoinLookup{
				"posts": {From: "posts", On: map[string]string{"user_id": "id"}},
			},
		}),
	}
	reg, err := Build(map[string]ds.Source{"drv": driver}, mergeEntries(sourceDefs, defs), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Get("users_with_posts").Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("want 1 upstream call (cache dedup), got %d", hits)
	}
}

// TestJoinLookupCacheHonorsCacheConfig — the lookup's own `cache:`
// block overrides the automatic defaults. A pathologically short TTL
// forces every visit to miss, so both rows fire distinct requests.
func TestJoinLookupCacheHonorsCacheConfig(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["hello"]`))
	}))
	defer srv.Close()

	driver := &fakeSource{data: []any{
		map[string]any{"id": 1},
		map[string]any{"id": 1},
	}}
	sourceDefs := map[string]*cfg.Source{
		"posts": cfg.NewEntry(&cfg.Source{
			Type:       "http",
			Parameters: map[string]*cfg.Parameter{"user_id": {Type: "int", Required: true}},
			URL:        srv.URL + "/users/${params.user_id}/posts",
			// 1ns TTL means each get() call sees the entry as already
			// expired, forcing a fresh load every time.
			Cache: &cfg.CacheSpec{TTL: "1ns", Size: 10},
		}),
	}
	defs := map[string]*cfg.Source{
		"users_with_posts": cfg.NewEntry(&cfg.Source{
			Type: "join", Driver: cfg.JoinDriver{From: "drv"},
			Lookups: map[string]cfg.JoinLookup{
				"posts": {From: "posts", On: map[string]string{"user_id": "id"}},
			},
		}),
	}
	reg, err := Build(map[string]ds.Source{"drv": driver}, mergeEntries(sourceDefs, defs), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Get("users_with_posts").Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits < 2 {
		t.Errorf("want at least 2 upstream calls (short TTL forces re-fetch), got %d", hits)
	}
}

func fmtSscanf(s, format string, args ...any) (int, error) {
	return fmt.Sscanf(s, format, args...)
}
