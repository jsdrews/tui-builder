package datasource

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// TestFileSourceJSON reads a fixture JSON file and confirms the parsed
// value matches what we'd expect from a list-shaped root.
func TestFileSourceJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.json")
	if err := os.WriteFile(path, []byte(`{"items":[{"name":"a"},{"name":"b"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	live, err := Build(map[string]*cfg.DataSource{
		"f": {Type: "file", Path: path, Root: "items"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := live["f"].Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Source.Fetch applies root internally now — the returned value is
	// already the items slice.
	if items := Iter(got); len(items) != 2 {
		t.Fatalf("want 2 items, got %d (%v)", len(items), got)
	}
}

// TestExecSourceJSON runs `sh -c` to emit a small JSON document and
// verifies we can parse + index into it. Skipped on Windows where
// /bin/sh isn't available.
func TestExecSourceJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	live, err := Build(map[string]*cfg.DataSource{
		"e": {
			Type:    "exec",
			Command: []string{"sh", "-c", `echo '[{"x":1},{"x":2},{"x":3}]'`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := live["e"].Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := Iter(got)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	if String(items[1], "x") != "2" {
		t.Fatalf("items[1].x = %v, want 2", items[1])
	}
}

// TestMergeSourceFanout exercises the cross-source composer: two
// children, tag injection, union semantics, and stable ordering by
// cfg.Sources position.
func TestMergeSourceFanout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	defs := map[string]*cfg.DataSource{
		"a": {
			Type:    "exec",
			Command: []string{"sh", "-c", `echo '[{"n":"a1"},{"n":"a2"}]'`},
		},
		"b": {
			Type:    "exec",
			Command: []string{"sh", "-c", `echo '[{"n":"b1"}]'`},
		},
		"all": {
			Type:     "merge",
			Sources:  []string{"a", "b"},
			TagField: "src",
		},
	}
	live, err := Build(defs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := live["all"].Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := Iter(got)
	if len(items) != 3 {
		t.Fatalf("want 3 merged items, got %d (%v)", len(items), items)
	}
	// Ordering follows cfg.Sources: a's two items, then b's one. Tags
	// land under the default `_meta` key — the framework metadata is
	// segregated from the upstream child fields.
	wantNames := []string{"a1", "a2", "b1"}
	wantTags := []string{"a", "a", "b"}
	for i, it := range items {
		if got := String(it, "n"); got != wantNames[i] {
			t.Errorf("items[%d].n = %q, want %q", i, got, wantNames[i])
		}
		if got := String(it, "_meta.src"); got != wantTags[i] {
			t.Errorf("items[%d]._meta.src = %q, want %q", i, got, wantTags[i])
		}
	}
}

// TestMergeChildrenPerChildTags covers the long-form merge shape:
// each child declares its own tags map; merge injects every key into
// every row from that child. Verifies tags are distinct per child,
// not collapsed to one global field.
func TestMergeChildrenPerChildTags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	defs := map[string]*cfg.DataSource{
		"a": {Type: "exec", Command: []string{"sh", "-c", `echo '[{"n":"a1"}]'`}},
		"b": {Type: "exec", Command: []string{"sh", "-c", `echo '[{"n":"b1"}]'`}},
		"all": {
			Type: "merge",
			Children: []cfg.MergeChild{
				{Source: "a", Tags: map[string]string{"cluster": "prod", "cluster_url": "http://prod"}},
				{Source: "b", Tags: map[string]string{"cluster": "dev", "cluster_url": "http://dev"}},
			},
		},
	}
	live, err := Build(defs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := live["all"].Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := Iter(got)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d (%v)", len(items), items)
	}
	want := []struct{ name, cluster, url string }{
		{"a1", "prod", "http://prod"},
		{"b1", "dev", "http://dev"},
	}
	for i, w := range want {
		if got := String(items[i], "n"); got != w.name {
			t.Errorf("items[%d].n = %q, want %q", i, got, w.name)
		}
		// Default meta_key is `_meta` — both child-injected tags
		// live under it.
		if got := String(items[i], "_meta.cluster"); got != w.cluster {
			t.Errorf("items[%d]._meta.cluster = %q, want %q", i, got, w.cluster)
		}
		if got := String(items[i], "_meta.cluster_url"); got != w.url {
			t.Errorf("items[%d]._meta.cluster_url = %q, want %q", i, got, w.url)
		}
	}
}

// TestMergeMetaKeyFlat — explicit `meta_key: ""` (empty string)
// disables nesting so existing configs that expect top-level tag
// fields keep working. The empty string opt-out is the migration
// escape hatch.
func TestMergeMetaKeyFlat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	flat := ""
	defs := map[string]*cfg.DataSource{
		"a": {Type: "exec", Command: []string{"sh", "-c", `echo '[{"n":"a1"}]'`}},
		"all": {
			Type:     "merge",
			Sources:  []string{"a"},
			TagField: "src",
			MetaKey:  &flat,
		},
	}
	live, err := Build(defs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := live["all"].Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := Iter(got)
	// With nesting disabled, src lives at the top level.
	if v := String(items[0], "src"); v != "a" {
		t.Errorf("flat mode: items[0].src = %q, want %q", v, "a")
	}
	if v := String(items[0], "_meta.src"); v != "" {
		t.Errorf("flat mode: items[0]._meta.src = %q, want empty", v)
	}
}

// TestMergeValidatorRejectsBothShapes — sources: and children: are
// mutually exclusive. The validator catches misconfigurations at load
// time rather than silently dropping one shape.
func TestMergeValidatorRejectsBothShapes(t *testing.T) {
	c := cfg.Config{
		DataSources: map[string]*cfg.DataSource{
			"a": {Type: "exec", Command: []string{"true"}},
			"b": {Type: "exec", Command: []string{"true"}},
			"all": {
				Type:     "merge",
				Sources:  []string{"a"},
				Children: []cfg.MergeChild{{Source: "b"}},
			},
		},
		Components: map[string]*cfg.Component{
			"x": {Type: "list", Source: "all", Item: "name"},
		},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected validator to reject both sources: and children:; passed")
	}
}

// TestMergeValidatorRejectsTagFieldWithChildren — tag_field belongs to
// the sources/shorthand path; with children: each child carries its
// own tags map, so tag_field is meaningless and likely a config
// mistake. Reject loudly.
func TestMergeValidatorRejectsTagFieldWithChildren(t *testing.T) {
	c := cfg.Config{
		DataSources: map[string]*cfg.DataSource{
			"a": {Type: "exec", Command: []string{"true"}},
			"all": {
				Type:     "merge",
				Children: []cfg.MergeChild{{Source: "a"}},
				TagField: "cluster",
			},
		},
		Components: map[string]*cfg.Component{
			"x": {Type: "list", Source: "all", Item: "name"},
		},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected validator to reject tag_field combined with children:; passed")
	}
}

// TestMergeOnErrorSkip — one child fails, the other succeeds; with
// on_error:skip the merged result contains the surviving child's items
// PLUS a non-nil error describing which children failed. This makes
// partial failures visible (silent skip used to hide unreachable
// clusters; the new contract surfaces them via the statusbar).
func TestMergeOnErrorSkip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	defs := map[string]*cfg.DataSource{
		"good": {Type: "exec", Command: []string{"sh", "-c", `echo '[{"n":"ok"}]'`}},
		"bad":  {Type: "exec", Command: []string{"sh", "-c", "exit 1"}},
		"all":  {Type: "merge", Sources: []string{"good", "bad"}, OnError: "skip"},
	}
	live, err := Build(defs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := live["all"].Fetch(context.Background())
	// Data should be present (the surviving good child's item).
	if len(Iter(got)) != 1 {
		t.Fatalf("want 1 survivor item, got %d (%v)", len(Iter(got)), got)
	}
	// Error should be non-nil and name the failed child so the user
	// sees the partial-failure diagnostic.
	if err == nil {
		t.Fatal("on_error:skip should surface partial failure in the error, got nil")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should name the failed child, got: %v", err)
	}
	if !strings.Contains(err.Error(), "partial") {
		t.Errorf("error should be tagged 'partial', got: %v", err)
	}
}

// TestMergeWithRootedChildren is the regression for the multi-cluster
// kube bug: each child returns a wrapper object whose iterable items
// live at `root: items`. Each source applies its own root in Fetch, so
// merge sees `[]any` from every child and unions them correctly.
// Without the in-source-root fix, this test fails — merge would see
// the wrapper objects and produce a 3-row table of garbage.
func TestMergeWithRootedChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	defs := map[string]*cfg.DataSource{
		"prod": {
			Type:    "exec",
			Command: []string{"sh", "-c", `echo '{"kind":"PodList","items":[{"name":"p1"},{"name":"p2"}]}'`},
			Root:    "items",
		},
		"staging": {
			Type:    "exec",
			Command: []string{"sh", "-c", `echo '{"kind":"PodList","items":[{"name":"s1"}]}'`},
			Root:    "items",
		},
		"all": {
			Type:     "merge",
			Sources:  []string{"prod", "staging"},
			TagField: "cluster",
		},
	}
	live, err := Build(defs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := live["all"].Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := Iter(got)
	if len(items) != 3 {
		t.Fatalf("want 3 unioned pod items, got %d (%v)", len(items), items)
	}
	// Children's roots applied → each item is a pod (has "name"), not
	// a wrapper (which would have "kind" instead).
	if String(items[0], "name") != "p1" {
		t.Errorf("items[0].name = %q, want p1 (got the wrapper instead?)", String(items[0], "name"))
	}
	if String(items[0], "_meta.cluster") != "prod" {
		t.Errorf("items[0]._meta.cluster = %q, want prod", String(items[0], "_meta.cluster"))
	}
}

// TestMergeOnErrorFail — one child fails, the merge errors out. This
// is the default; opt-in to skip via on_error:skip.
func TestMergeOnErrorFail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	defs := map[string]*cfg.DataSource{
		"good": {Type: "exec", Command: []string{"sh", "-c", `echo '[{"n":"ok"}]'`}},
		"bad":  {Type: "exec", Command: []string{"sh", "-c", "exit 1"}},
		"all":  {Type: "merge", Sources: []string{"good", "bad"}}, // on_error defaults to fail
	}
	live, err := Build(defs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := live["all"].Fetch(context.Background()); err == nil {
		t.Fatal("default on_error:fail should propagate child failure")
	}
}
