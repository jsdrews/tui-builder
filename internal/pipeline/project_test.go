package pipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

func TestProjectSnapshotSlimsObjects(t *testing.T) {
	up := &fakeSource{data: []any{
		map[string]any{"metadata": map[string]any{"name": "a", "namespace": "default"}, "status": map[string]any{"phase": "Running"}},
		map[string]any{"metadata": map[string]any{"name": "b", "namespace": "kube-system"}, "status": map[string]any{"phase": "Pending"}},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"slim": {Project: &cfg.ProjectOp{
				From: "src",
				Keep: map[string]string{
					"name":      "metadata.name",
					"namespace": "metadata.namespace",
					"phase":     "status.phase",
				},
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("slim").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items, ok := got.([]any)
	if !ok {
		t.Fatalf("want []any, got %T", got)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	first := items[0].(map[string]any)
	if first["name"] != "a" || first["namespace"] != "default" || first["phase"] != "Running" {
		t.Errorf("first item shape wrong: %v", first)
	}
	// Original keys like `metadata` must NOT survive.
	if _, ok := first["metadata"]; ok {
		t.Errorf("project leaked the metadata key; got %v", first)
	}
}

func TestProjectSnapshotComputedExpressions(t *testing.T) {
	// Keep values are expressions, not just paths.
	up := &fakeSource{data: []any{
		map[string]any{"name": "FooBar", "scores": []any{1, 2, 3}},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"shaped": {Project: &cfg.ProjectOp{
				From: "src",
				Keep: map[string]string{
					"name_lower": "lower(name)",
					"score_count": "len(scores)",
				},
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("shaped").Fetch(context.Background())
	item := got.([]any)[0].(map[string]any)
	if item["name_lower"] != "foobar" {
		t.Errorf("name_lower = %v", item["name_lower"])
	}
	if item["score_count"] != 3 {
		t.Errorf("score_count = %v (%T)", item["score_count"], item["score_count"])
	}
}

func TestProjectStreamingReshapesJSONEvents(t *testing.T) {
	streamer := &fakeStreamer{
		events: []ds.Event{
			{Line: `{"name":"a","extra":"drop me","nested":{"v":1}}`},
			{Line: `{"name":"b","extra":"drop me","nested":{"v":2}}`},
		},
	}
	reg, err := Build(
		map[string]ds.Source{"src": streamer},
		nil,
		map[string]*cfg.Pipeline{
			"slim": {Project: &cfg.ProjectOp{
				From: "src",
				Keep: map[string]string{
					"name": "name",
					"v":    "nested.v",
				},
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := reg.Get("slim").(ds.StreamingSource).Subscribe(context.Background())
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
	if seen[0]["name"] != "a" || seen[0]["v"].(float64) != 1 {
		t.Errorf("event 0 wrong: %v", seen[0])
	}
	if _, ok := seen[0]["extra"]; ok {
		t.Errorf("extra key leaked through project: %v", seen[0])
	}
}

func TestProjectValidationRejectsEmptyKeep(t *testing.T) {
	c := cfg.Config{
		DataSources: map[string]*cfg.DataSource{
			"src": {Type: "exec", Command: []string{"true"}},
		},
		Pipelines: map[string]*cfg.Pipeline{
			"bad": {Project: &cfg.ProjectOp{From: "src"}},
		},
		Components: map[string]*cfg.Component{
			"x": {Type: "list", Items: []string{"x"}},
		},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validation error for empty keep:")
	}
}
