package pipeline

import (
	"context"
	"errors"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

func TestSortAscending(t *testing.T) {
	up := &fakeSource{data: []any{
		map[string]any{"name": "c"},
		map[string]any{"name": "a"},
		map[string]any{"name": "b"},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"sorted": cfg.NewEntry(&cfg.Source{Type: "sort", From: "src", By: "name"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("sorted").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := got.([]any)
	wantNames := []string{"a", "b", "c"}
	for i, it := range items {
		if got := it.(map[string]any)["name"]; got != wantNames[i] {
			t.Errorf("items[%d].name = %v, want %v", i, got, wantNames[i])
		}
	}
}

func TestSortDescending(t *testing.T) {
	up := &fakeSource{data: []any{
		map[string]any{"score": 3},
		map[string]any{"score": 1},
		map[string]any{"score": 5},
		map[string]any{"score": 2},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"top_first": cfg.NewEntry(&cfg.Source{Type: "sort", From: "src", By: "score", Order: "desc"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("top_first").Fetch(context.Background())
	want := []float64{5, 3, 2, 1}
	items := got.([]any)
	for i, it := range items {
		s := it.(map[string]any)["score"]
		// expr returns ints as Go int (since data came from a Go literal),
		// but the key compare path converts via toFloat — same result.
		var got float64
		switch v := s.(type) {
		case int:
			got = float64(v)
		case float64:
			got = v
		}
		if got != want[i] {
			t.Errorf("items[%d].score = %v, want %v", i, got, want[i])
		}
	}
}

func TestSortStableOnTies(t *testing.T) {
	// Ties on `group` must preserve original relative order of `tag`.
	up := &fakeSource{data: []any{
		map[string]any{"group": 1, "tag": "a"},
		map[string]any{"group": 2, "tag": "b"},
		map[string]any{"group": 1, "tag": "c"},
		map[string]any{"group": 2, "tag": "d"},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"by_group": cfg.NewEntry(&cfg.Source{Type: "sort", From: "src", By: "group"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("by_group").Fetch(context.Background())
	items := got.([]any)
	wantTags := []string{"a", "c", "b", "d"}
	for i, it := range items {
		if tag := it.(map[string]any)["tag"]; tag != wantTags[i] {
			t.Errorf("stable sort lost tie-order at %d: got %v, want %v", i, tag, wantTags[i])
		}
	}
}

func TestSortNilKeyClustersFirst(t *testing.T) {
	// Items whose sort key resolves to nil (missing field) go to the
	// top in ascending and the bottom in descending order.
	up := &fakeSource{data: []any{
		map[string]any{"name": "b"},
		map[string]any{}, // no name
		map[string]any{"name": "a"},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"sorted": cfg.NewEntry(&cfg.Source{Type: "sort", From: "src", By: "name"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("sorted").Fetch(context.Background())
	items := got.([]any)
	// nil first, then "a", then "b"
	if _, ok := items[0].(map[string]any)["name"]; ok {
		t.Errorf("expected nil-key item first; got %v", items[0])
	}
	if items[1].(map[string]any)["name"] != "a" {
		t.Errorf("items[1] = %v, want name=a", items[1])
	}
	if items[2].(map[string]any)["name"] != "b" {
		t.Errorf("items[2] = %v, want name=b", items[2])
	}
}

func TestSortNonIterablePassesThrough(t *testing.T) {
	// Single object upstream — sort has nothing to sort, returns
	// as-is rather than failing.
	up := &fakeSource{data: map[string]any{"name": "x"}}
	reg, err := Build(
		map[string]ds.Source{"src": up},

		map[string]*cfg.Source{
			"sorted": cfg.NewEntry(&cfg.Source{Type: "sort", From: "src", By: "name"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("sorted").Fetch(context.Background())
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("non-iterable upstream should pass through; got %T", got)
	}
	if m["name"] != "x" {
		t.Errorf("pass-through value lost: %v", m)
	}
}

func TestSortSubscribeReturnsNotStreaming(t *testing.T) {
	streamer := &fakeStreamer{}
	reg, err := Build(
		map[string]ds.Source{"src": streamer},

		map[string]*cfg.Source{
			"sorted": cfg.NewEntry(&cfg.Source{Type: "sort", From: "src", By: "x"}),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Get("sorted").(ds.StreamingSource).Subscribe(context.Background())
	if !errors.Is(err, ds.ErrNotStreaming) {
		t.Errorf("sort should return ErrNotStreaming from Subscribe; got %v", err)
	}
}

func TestSortInvalidOrderRejected(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"src": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),

		"bad": cfg.NewEntry(&cfg.Source{Type: "sort", From: "src", By: "x", Order: "random"})}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"c": {Type: "list", Items: []string{"x"}},
	},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "c"}}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject order=random")
	}
}
