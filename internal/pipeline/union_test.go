package pipeline

import (
	"context"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

func TestUnionShorthandFlattensChildren(t *testing.T) {
	a := &fakeSource{data: []any{map[string]any{"n": "a1"}, map[string]any{"n": "a2"}}}
	b := &fakeSource{data: []any{map[string]any{"n": "b1"}}}
	reg, err := Build(
		map[string]ds.Source{"a": a, "b": b},

		map[string]*cfg.Source{
			"all": cfg.NewEntry(&cfg.Source{Type: "union", Sources: []string{"a", "b"},
				TagField: "src"},
			),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("all").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items, ok := got.([]any)
	if !ok {
		t.Fatalf("want []any, got %T", got)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 unioned items, got %d", len(items))
	}
	// Tags land under default _meta key, matching the merge source's
	// behavior — segregated from upstream child data.
	wantSrc := []string{"a", "a", "b"}
	wantName := []string{"a1", "a2", "b1"}
	for i, it := range items {
		m := it.(map[string]any)
		if m["n"] != wantName[i] {
			t.Errorf("items[%d].n = %v, want %v", i, m["n"], wantName[i])
		}
		meta, _ := m["_meta"].(map[string]any)
		if meta["src"] != wantSrc[i] {
			t.Errorf("items[%d]._meta.src = %v, want %v", i, meta["src"], wantSrc[i])
		}
	}
}

func TestUnionChildrenPerChildTags(t *testing.T) {
	a := &fakeSource{data: []any{map[string]any{"n": "a1"}}}
	b := &fakeSource{data: []any{map[string]any{"n": "b1"}}}
	reg, err := Build(
		map[string]ds.Source{"a": a, "b": b},

		map[string]*cfg.Source{
			"all": cfg.NewEntry(&cfg.Source{Type: "union", Children: []cfg.MergeChild{
				{Source: "a", Tags: map[string]string{"cluster": "prod", "cluster_url": "http://prod"}},
				{Source: "b", Tags: map[string]string{"cluster": "dev", "cluster_url": "http://dev"}},
			}},
			),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("all").Fetch(context.Background())
	items := got.([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	want := []struct{ name, cluster, url string }{
		{"a1", "prod", "http://prod"},
		{"b1", "dev", "http://dev"},
	}
	for i, w := range want {
		m := items[i].(map[string]any)
		if m["n"] != w.name {
			t.Errorf("items[%d].n = %v, want %v", i, m["n"], w.name)
		}
		meta := m["_meta"].(map[string]any)
		if meta["cluster"] != w.cluster {
			t.Errorf("items[%d]._meta.cluster = %v, want %v", i, meta["cluster"], w.cluster)
		}
		if meta["cluster_url"] != w.url {
			t.Errorf("items[%d]._meta.cluster_url = %v, want %v", i, meta["cluster_url"], w.url)
		}
	}
}

// TestUnionAcceptsPipelineAsChild is the key reason union exists as a
// pipeline operator: children can be OTHER pipelines (filter/project/etc.),
// not just leaf sources. The merge SOURCE can't do this — its children
// must be defined under data_sources:. Union accepts anything that
// satisfies ds.Source, including pipelines.
func TestUnionAcceptsPipelineAsChild(t *testing.T) {
	a := &fakeSource{data: []any{
		map[string]any{"n": "a1", "phase": "Running"},
		map[string]any{"n": "a2", "phase": "Pending"},
	}}
	b := &fakeSource{data: []any{
		map[string]any{"n": "b1", "phase": "Running"},
	}}
	reg, err := Build(
		map[string]ds.Source{"a": a, "b": b},

		map[string]*cfg.Source{
			// First filter source A — children can be any operator.
			"a_running": cfg.NewEntry(&cfg.Source{Type: "filter", From: "a", Where: "phase == 'Running'"}),
			// Then union the filtered pipeline with the raw source.
			"all": cfg.NewEntry(&cfg.Source{Type: "union", Sources: []string{"a_running", "b"},
				TagField: "src"},
			),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("all").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := got.([]any)
	// a_running gives us a1 only; b gives us b1. Two total.
	if len(items) != 2 {
		t.Fatalf("want 2 items (a1 + b1), got %d (%v)", len(items), items)
	}
	wantName := []string{"a1", "b1"}
	wantSrc := []string{"a_running", "b"}
	for i, it := range items {
		m := it.(map[string]any)
		if m["n"] != wantName[i] {
			t.Errorf("items[%d].n = %v, want %v", i, m["n"], wantName[i])
		}
		meta := m["_meta"].(map[string]any)
		if meta["src"] != wantSrc[i] {
			t.Errorf("items[%d]._meta.src = %v, want %v", i, meta["src"], wantSrc[i])
		}
	}
}

func TestUnionMetaKeyFlat(t *testing.T) {
	// Explicit meta_key: "" disables nesting — tags land at the top
	// level. Opt-out for configs that need the legacy flat shape.
	a := &fakeSource{data: []any{map[string]any{"n": "a1"}}}
	flat := ""
	reg, err := Build(
		map[string]ds.Source{"a": a},

		map[string]*cfg.Source{
			"all": cfg.NewEntry(&cfg.Source{Type: "union", Sources: []string{"a"},
				TagField: "src",
				MetaKey:  &flat},
			),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("all").Fetch(context.Background())
	items := got.([]any)
	m := items[0].(map[string]any)
	if m["src"] != "a" {
		t.Errorf("flat mode: src = %v, want %v", m["src"], "a")
	}
	if _, ok := m["_meta"]; ok {
		t.Errorf("flat mode shouldn't write under _meta; got %v", m["_meta"])
	}
}

func TestUnionValidatorRejectsBothShapes(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"a": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),
		"b": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),

		"bad": cfg.NewEntry(&cfg.Source{Type: "union", Sources: []string{"a"},
			Children: []cfg.MergeChild{{Source: "b"}}},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"x": {Type: "list", Items: []string{"x"}},
	},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject both sources: and children: on union")
	}
}

func TestUnionValidatorRejectsTagFieldWithChildren(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"a": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"true"}}),

		"bad": cfg.NewEntry(&cfg.Source{Type: "union", Children: []cfg.MergeChild{{Source: "a"}},
			TagField: "cluster"},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"x": {Type: "list", Items: []string{"x"}},
	},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject tag_field with children on union")
	}
}
