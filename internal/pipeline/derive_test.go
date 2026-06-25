package pipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

func TestDeriveSnapshotAddsFields(t *testing.T) {
	up := &fakeSource{data: []any{
		map[string]any{"name": "alpha", "size": 5},
		map[string]any{"name": "beta", "size": 12},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"enriched": {Derive: &cfg.DeriveOp{
				From: "src",
				Compute: map[string]string{
					"name_upper": "upper(name)",
					"is_large":   "size > 10",
				},
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("enriched").Fetch(context.Background())
	items := got.([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	// Original fields must survive — derive is additive.
	first := items[0].(map[string]any)
	if first["name"] != "alpha" || first["size"] != 5 {
		t.Errorf("original fields lost: %v", first)
	}
	if first["name_upper"] != "ALPHA" || first["is_large"] != false {
		t.Errorf("first derived fields wrong: %v", first)
	}
	second := items[1].(map[string]any)
	if second["is_large"] != true {
		t.Errorf("second is_large = %v, want true", second["is_large"])
	}
}

func TestDeriveCopyOnWrite(t *testing.T) {
	// Verify derive doesn't mutate the upstream's items.
	original := map[string]any{"name": "x"}
	up := &fakeSource{data: []any{original}}
	reg, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"e": {Derive: &cfg.DeriveOp{From: "src", Compute: map[string]string{"added": "upper(name)"}}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Get("e").Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := original["added"]; ok {
		t.Errorf("derive mutated the upstream item: %v", original)
	}
}

func TestDeriveOverwritesExistingKey(t *testing.T) {
	// When a compute key collides with an existing field, derive wins.
	// This lets users reshape awkward fields in-place.
	up := &fakeSource{data: []any{map[string]any{"name": "lowercase"}}}
	reg, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"upper_name": {Derive: &cfg.DeriveOp{
				From:    "src",
				Compute: map[string]string{"name": "upper(name)"},
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("upper_name").Fetch(context.Background())
	item := got.([]any)[0].(map[string]any)
	if item["name"] != "LOWERCASE" {
		t.Errorf("derive should override existing key; got %v", item["name"])
	}
}

func TestDeriveStreamingAddsFields(t *testing.T) {
	streamer := &fakeStreamer{
		events: []ds.Event{
			{Line: `{"name":"a"}`},
			{Line: `{"name":"b"}`},
		},
	}
	reg, err := Build(
		map[string]ds.Source{"src": streamer},
		nil,
		map[string]*cfg.Pipeline{
			"e": {Derive: &cfg.DeriveOp{
				From:    "src",
				Compute: map[string]string{"upper": "upper(name)"},
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := reg.Get("e").(ds.StreamingSource).Subscribe(context.Background())
	var seen []map[string]any
	timeout := time.After(time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(ev.Line), &m); err != nil {
				t.Fatalf("event re-encoded as non-JSON: %s", ev.Line)
			}
			seen = append(seen, m)
		case <-timeout:
			t.Fatalf("stream didn't close; got %v", seen)
		}
	}
done:
	if len(seen) != 2 {
		t.Fatalf("want 2 events, got %d", len(seen))
	}
	if seen[0]["name"] != "a" || seen[0]["upper"] != "A" {
		t.Errorf("event 0 wrong: %v", seen[0])
	}
}

func TestDeriveCompileErrorAtBuild(t *testing.T) {
	up := &fakeSource{data: []any{}}
	_, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"bad": {Derive: &cfg.DeriveOp{
				From: "src", Compute: map[string]string{"x": "=== broken"},
			}},
		},
		nil,
	)
	if err == nil {
		t.Errorf("expected compile error on malformed expression")
	}
}
