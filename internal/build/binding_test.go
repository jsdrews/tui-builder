package build

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/theme"

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
