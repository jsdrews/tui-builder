package build

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jsdrews/tuilib/pkg/theme"
	"github.com/jsdrews/tuilib/pkg/tree"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// TestDeriveInspectorFieldsAutoWalksAnyShape covers feature B: with
// `auto: true`, the inspector accepts whatever the source returns and
// derives labeled fields from the map keys. No declared shape needed.
func TestDeriveInspectorFieldsAutoWalksAnyShape(t *testing.T) {
	th := theme.Nord()
	comp := &cfg.Component{Type: "inspector", Auto: true}
	data := map[string]any{
		"name":    "widget",
		"count":   float64(3),
		"enabled": true,
	}
	fields := deriveInspectorFields(comp, data, th)
	if len(fields) != 3 {
		t.Fatalf("want 3 top-level fields, got %d: %+v", len(fields), fields)
	}
	// FromMap sorts keys alphabetically, so order is deterministic.
	want := []struct {
		label, value string
	}{
		{"count", "3"},
		{"enabled", "true"},
		{"name", "widget"},
	}
	for i, w := range want {
		if fields[i].Label != w.label || fields[i].Value != w.value {
			t.Errorf("field[%d]: want (%q, %q), got (%q, %q)",
				i, w.label, w.value, fields[i].Label, fields[i].Value)
		}
	}
}

// TestDeriveInspectorFieldsAutoNestsSubStructures covers feature C: a
// nested map under a key should expand into Children, not stringify as
// "map[...]" under a scalar Value. Same for []any.
func TestDeriveInspectorFieldsAutoNestsSubStructures(t *testing.T) {
	th := theme.Nord()
	comp := &cfg.Component{Type: "inspector", Auto: true}
	data := map[string]any{
		"metadata": map[string]any{
			"name":      "pod-1",
			"namespace": "default",
		},
		"tags": []any{"a", "b"},
	}
	fields := deriveInspectorFields(comp, data, th)
	if len(fields) != 2 {
		t.Fatalf("want 2 top-level fields, got %d", len(fields))
	}
	// Sorted alphabetically: metadata, tags.
	meta := fields[0]
	if meta.Label != "metadata" {
		t.Fatalf("want first field 'metadata', got %q", meta.Label)
	}
	if meta.Value != "" {
		t.Errorf("nested map field should have empty Value (children only), got %q", meta.Value)
	}
	if len(meta.Children) != 2 {
		t.Errorf("metadata should have 2 children (name, namespace), got %d", len(meta.Children))
	}
	tags := fields[1]
	if tags.Label != "tags" {
		t.Fatalf("want second field 'tags', got %q", tags.Label)
	}
	if len(tags.Children) != 2 {
		t.Errorf("tags array should have 2 children ([0], [1]), got %d", len(tags.Children))
	}
	if tags.Children[0].Label != "[0]" || tags.Children[0].Value != "a" {
		t.Errorf("tags[0]: want ([0], a), got (%q, %q)", tags.Children[0].Label, tags.Children[0].Value)
	}
}

// treeComponent builds a KTree Component from a config the way NewComponent
// would — used by the applyTree tests to exercise the real construction
// path (including the seeded placeholder root that makes InitialDepth's
// pre-expansion carry across SetRoot).
func treeComponent(t *testing.T, cfgComp *cfg.Component) *Component {
	t.Helper()
	th := theme.Nord()
	c, err := NewComponent(cfgComp, th)
	if err != nil {
		t.Fatalf("build tree component: %v", err)
	}
	c.Tree.SetDimensions(60, 20)
	return c
}

// collectLabels walks the current tree and returns every node's label
// in DFS order. Uses tree.Selected() by moving the cursor row-by-row —
// tuilib doesn't expose the row set directly, so we scan via View()
// instead which reflects only currently-visible rows.
func visibleLabels(m *tree.Model) []string {
	view := m.View()
	// The view includes the pane frame + tree body. Split lines and keep
	// non-empty ones after trimming whitespace and the expand glyphs.
	lines := strings.Split(view, "\n")
	var labels []string
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		// Strip leading tree glyphs (▸ ▾ · space).
		trimmed = strings.TrimLeft(trimmed, "▸▾· \t")
		labels = append(labels, trimmed)
	}
	return labels
}

// TestApplyTreeFlatBindsItemsAsChildren covers the ungrouped path:
// items become direct children of the root, labeled via Cfg.Label.
func TestApplyTreeFlatBindsItemsAsChildren(t *testing.T) {
	th := theme.Nord()
	c := treeComponent(t, &cfg.Component{
		Type:         "tree",
		Title:        "Resources",
		Source:       "src",
		Label:        cfg.Path{"name"},
		InitialDepth: 2,
	})
	data := []any{
		map[string]any{"name": "alpha"},
		map[string]any{"name": "beta"},
		map[string]any{"name": "gamma"},
	}
	applyTree(c, data, th)
	labels := visibleLabels(c.Tree)
	// Expect "Resources" pane title, "Resources" root row, then 3 leaves.
	// Pane title AND root label are both "Resources" per the fallback.
	joined := strings.Join(labels, "|")
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in visible labels, got: %s", want, joined)
		}
	}
}

// TestApplyTreeGroupByBucketsItems covers the kubectl-shape use case:
// a flat list gets bucketed under parent nodes named for the GroupBy
// value; leaves land under their bucket.
func TestApplyTreeGroupByBucketsItems(t *testing.T) {
	th := theme.Nord()
	c := treeComponent(t, &cfg.Component{
		Type:         "tree",
		Title:        "Namespace",
		Source:       "src",
		Label:        cfg.Path{"name"},
		GroupBy:      cfg.Path{"kind"},
		InitialDepth: 3,
	})
	data := []any{
		map[string]any{"kind": "Pod", "name": "nginx-a"},
		map[string]any{"kind": "Service", "name": "nginx"},
		map[string]any{"kind": "Pod", "name": "nginx-b"},
	}
	applyTree(c, data, th)
	labels := visibleLabels(c.Tree)
	joined := strings.Join(labels, "|")
	// Buckets in first-appearance order: Pod, then Service.
	podIdx := strings.Index(joined, "Pod")
	svcIdx := strings.Index(joined, "Service")
	if podIdx < 0 || svcIdx < 0 {
		t.Fatalf("expected both bucket labels in view; got: %s", joined)
	}
	if podIdx > svcIdx {
		t.Errorf("Pod should appear before Service (first-appearance order); got: %s", joined)
	}
}

// TestSourceTreeRootLabelFallbacks pins the fallback chain — matters
// because the root label doubles as the identity key tuilib uses to
// carry expand state across SetRoot swaps.
func TestSourceTreeRootLabelFallbacks(t *testing.T) {
	cases := []struct {
		name string
		in   *cfg.Component
		want string
	}{
		{"explicit root_label wins", &cfg.Component{RootLabel: "R", Title: "T", Source: "S"}, "R"},
		{"title used when no root_label", &cfg.Component{Title: "T", Source: "S"}, "T"},
		{"source name used when both empty", &cfg.Component{Source: "S"}, "S"},
	}
	for _, tc := range cases {
		if got := sourceTreeRootLabel(tc.in); got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}

// TestApplyTreePreservesRootExpansionAcrossRefresh pins the live-update
// contract: the root's expand state survives ApplyData because
// buildTree seeds a placeholder root with the same label the apply path
// uses, and tuilib.tree.SetRoot preserves reachable expanded entries.
func TestApplyTreePreservesRootExpansionAcrossRefresh(t *testing.T) {
	th := theme.Nord()
	c := treeComponent(t, &cfg.Component{
		Type:         "tree",
		Title:        "Resources",
		Source:       "src",
		Label:        cfg.Path{"name"},
		InitialDepth: 2,
	})
	// First fill.
	applyTree(c, []any{map[string]any{"name": "alpha"}}, th)
	if !strings.Contains(strings.Join(visibleLabels(c.Tree), "|"), "alpha") {
		t.Fatalf("first fill: alpha should be visible (root expanded)")
	}
	// Second fill with swapped item — root stays expanded, new item visible.
	applyTree(c, []any{map[string]any{"name": "beta"}}, th)
	labels := strings.Join(visibleLabels(c.Tree), "|")
	if strings.Contains(labels, "alpha") {
		t.Errorf("post-refresh: alpha should be gone; got: %s", labels)
	}
	if !strings.Contains(labels, "beta") {
		t.Errorf("post-refresh: beta should be visible (root still expanded); got: %s", labels)
	}
}

// TestApplyTextviewStringSetsContent covers the format:text source
// path — a raw string (kubectl describe, help page, markdown blob)
// lands as the textview's content untouched.
func TestApplyTextviewStringSetsContent(t *testing.T) {
	th := theme.Nord()
	c, err := NewComponent(&cfg.Component{Type: "textview", Source: "src"}, th)
	if err != nil {
		t.Fatalf("build textview: %v", err)
	}
	c.Textview.SetDimensions(60, 20)
	applyTextview(c, "line 1\nline 2\nline 3")
	if got := c.Textview.Content(); got != "line 1\nline 2\nline 3" {
		t.Errorf("content: want raw string, got %q", got)
	}
}

// TestApplyTextviewJoinsStringSlice covers the []any-of-strings shape
// (matches logview's convention so a source can back either component).
func TestApplyTextviewJoinsStringSlice(t *testing.T) {
	th := theme.Nord()
	c, err := NewComponent(&cfg.Component{Type: "textview", Source: "src"}, th)
	if err != nil {
		t.Fatalf("build textview: %v", err)
	}
	c.Textview.SetDimensions(60, 20)
	applyTextview(c, []any{"a", "b", "c"})
	if got := c.Textview.Content(); got != "a\nb\nc" {
		t.Errorf("content: want joined string, got %q", got)
	}
}

// TestBuildTextviewReadsWrapAndContent pins that the config-time Content
// + Wrap fields feed into the constructed model, so static-content mode
// works without a source.
func TestBuildTextviewReadsWrapAndContent(t *testing.T) {
	th := theme.Nord()
	c, err := NewComponent(&cfg.Component{
		Type:    "textview",
		Content: "hello",
		Wrap:    true,
	}, th)
	if err != nil {
		t.Fatalf("build textview: %v", err)
	}
	if !c.Textview.Wrap() {
		t.Errorf("wrap should be true from config")
	}
	if got := c.Textview.Content(); got != "hello" {
		t.Errorf("content: want %q, got %q", "hello", got)
	}
}

// TestApplyTreeChildrenWalksNestedStructure covers the recursive
// walker. Feature: a source-bound tree can consume nested data with
// `children:` pointing at each node's descendant list — filesystem
// trees, k8s owner-reference graphs, org charts, anything where the
// source already carries the hierarchy.
func TestApplyTreeChildrenWalksNestedStructure(t *testing.T) {
	th := theme.Nord()
	c := treeComponent(t, &cfg.Component{
		Type:         "tree",
		Title:        "Files",
		Source:       "src",
		Label:        cfg.Path{"name"},
		Children:     cfg.Path{"contents"},
		InitialDepth: 3,
	})
	data := []any{
		map[string]any{
			"name": "cmd",
			"contents": []any{
				map[string]any{
					"name": "wrangl",
					"contents": []any{
						map[string]any{"name": "main.go"},
					},
				},
				map[string]any{"name": "README.md"},
			},
		},
	}
	applyTree(c, data, th)
	// Expand every node so deeper labels become visible for the
	// assertion. buildTree's placeholder-seed only opens the root;
	// tuilib doesn't (yet) expose ExpandAll as a Go call, so we
	// simulate the 'E' key.
	m, _ := c.Tree.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	*c.Tree = m
	joined := strings.Join(visibleLabels(c.Tree), "|")
	for _, want := range []string{"Files", "cmd", "wrangl", "main.go", "README.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in tree; got: %s", want, joined)
		}
	}
}

// TestDeriveInspectorFieldsDeclaredModeIgnoresAuto covers the fallback:
// with Auto=false, we take the declared Fields path unchanged. This
// pins that adding the Auto branch doesn't disturb the existing wire.
func TestDeriveInspectorFieldsDeclaredModeIgnoresAuto(t *testing.T) {
	th := theme.Nord()
	comp := &cfg.Component{
		Type: "inspector",
		Fields: []cfg.InspectorField{
			{Label: "Name", Path: "name"},
		},
	}
	data := map[string]any{"name": "widget", "extra": "should-not-appear"}
	fields := deriveInspectorFields(comp, data, th)
	if len(fields) != 1 {
		t.Fatalf("declared mode should only surface declared fields; got %d", len(fields))
	}
	if fields[0].Label != "Name" || fields[0].Value != "widget" {
		t.Errorf("want (Name, widget), got (%q, %q)", fields[0].Label, fields[0].Value)
	}
}
