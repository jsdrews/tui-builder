package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

func TestFilterSnapshotKeepsMatchingItems(t *testing.T) {
	// Three pods, two Running and one Pending. Filter keeps Running.
	up := &fakeSource{
		data: []any{
			map[string]any{"name": "a", "status": map[string]any{"phase": "Running"}},
			map[string]any{"name": "b", "status": map[string]any{"phase": "Pending"}},
			map[string]any{"name": "c", "status": map[string]any{"phase": "Running"}},
		},
	}
	reg, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"running": {Filter: &cfg.FilterOp{From: "src", Where: "status.phase == 'Running'"}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("running").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items, ok := got.([]any)
	if !ok {
		t.Fatalf("filter returned non-slice: %T", got)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 survivors, got %d (%v)", len(items), items)
	}
	for _, it := range items {
		if name := it.(map[string]any)["name"]; name != "a" && name != "c" {
			t.Errorf("unexpected survivor: %v", name)
		}
	}
}

func TestFilterSnapshotEmptyResult(t *testing.T) {
	// All items rejected — survivors is an empty slice, not nil.
	up := &fakeSource{data: []any{
		map[string]any{"phase": "Pending"},
		map[string]any{"phase": "Failed"},
	}}
	reg, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"running": {Filter: &cfg.FilterOp{From: "src", Where: "phase == 'Running'"}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("running").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items, ok := got.([]any)
	if !ok {
		t.Fatalf("filter returned non-slice: %T", got)
	}
	if len(items) != 0 {
		t.Errorf("expected empty survivors, got %v", items)
	}
}

func TestFilterStreamingDropsFailing(t *testing.T) {
	// Streaming source: filter drops events whose payload doesn't
	// match. Driven by a fakeStreamer that emits JSON-shaped events.
	streamer := &fakeStreamer{
		events: []ds.Event{
			{Line: `{"phase":"Running","name":"a"}`},
			{Line: `{"phase":"Pending","name":"b"}`},
			{Line: `{"phase":"Running","name":"c"}`},
		},
	}
	reg, err := Build(
		map[string]ds.Source{"src": streamer},
		nil,
		map[string]*cfg.Pipeline{
			"running": {Filter: &cfg.FilterOp{From: "src", Where: "phase == 'Running'"}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := reg.Get("running").(ds.StreamingSource).Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	timeout := time.After(time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			seen = append(seen, ev.Line)
		case <-timeout:
			t.Fatalf("stream didn't close; got %v", seen)
		}
	}
done:
	if len(seen) != 2 {
		t.Fatalf("want 2 events after filter, got %d (%v)", len(seen), seen)
	}
	for _, s := range seen {
		if !strings.Contains(s, "Running") {
			t.Errorf("event leaked through filter: %s", s)
		}
	}
}

func TestFilterStreamingTextPayload(t *testing.T) {
	// Non-JSON streaming text (kube logs, follow-mode exec).
	// Predicate references the line as `item`.
	streamer := &fakeStreamer{
		events: []ds.Event{
			{Line: "INFO server started"},
			{Line: "ERROR something failed"},
			{Line: "WARN slow request"},
			{Line: "ERROR timeout"},
		},
	}
	reg, err := Build(
		map[string]ds.Source{"src": streamer},
		nil,
		map[string]*cfg.Pipeline{
			"errors_only": {Filter: &cfg.FilterOp{From: "src", Where: "item contains 'ERROR'"}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := reg.Get("errors_only").(ds.StreamingSource).Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	timeout := time.After(time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done2
			}
			seen = append(seen, ev.Line)
		case <-timeout:
			t.Fatalf("stream didn't close; got %v", seen)
		}
	}
done2:
	if len(seen) != 2 {
		t.Fatalf("want 2 ERROR lines, got %d (%v)", len(seen), seen)
	}
}

func TestFilterCompileErrorAtBuild(t *testing.T) {
	// Malformed predicate — Build returns a compile error, no source
	// constructed.
	up := &fakeSource{data: []any{}}
	_, err := Build(
		map[string]ds.Source{"src": up},
		nil,
		map[string]*cfg.Pipeline{
			"bad": {Filter: &cfg.FilterOp{From: "src", Where: "=== syntax junk"}},
		},
		nil,
	)
	if err == nil {
		t.Errorf("expected compile error on malformed where:")
	}
}

func TestFilterWithPipelineParams(t *testing.T) {
	// Pipeline params surface in the operator's expressions as
	// `params.X`. Default applies when nothing is bound; bound value
	// overrides.
	up := &fakeSource{data: []any{
		map[string]any{"name": "a", "score": 5},
		map[string]any{"name": "b", "score": 12},
		map[string]any{"name": "c", "score": 20},
	}}
	defs := map[string]*cfg.Pipeline{
		"hot": {
			Parameters: map[string]*cfg.Parameter{
				"min": {Type: "int", Default: "10"},
			},
			Filter: &cfg.FilterOp{From: "src", Where: "score >= int(params.min)"},
		},
	}

	// Default (min=10): keeps b (12) and c (20).
	reg, err := Build(map[string]ds.Source{"src": up}, nil, defs, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get("hot").Fetch(context.Background())
	if items := got.([]any); len(items) != 2 {
		t.Errorf("default: want 2, got %d", len(items))
	}

	// Bound (min=15): keeps c (20) only.
	reg, err = Build(map[string]ds.Source{"src": up}, nil, defs, map[string]map[string]string{
		"hot": {"min": "15"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = reg.Get("hot").Fetch(context.Background())
	if items := got.([]any); len(items) != 1 {
		t.Errorf("min=15: want 1, got %d", len(items))
	}
}

func TestPipelineParamsRequiredEnforced(t *testing.T) {
	up := &fakeSource{data: []any{}}
	defs := map[string]*cfg.Pipeline{
		"needs": {
			Parameters: map[string]*cfg.Parameter{
				"threshold": {Type: "int", Required: true},
			},
			Filter: &cfg.FilterOp{From: "src", Where: "x > int(params.threshold)"},
		},
	}
	// No params provided — Build should error because `threshold` is
	// required and has no default.
	if _, err := Build(map[string]ds.Source{"src": up}, nil, defs, nil); err == nil {
		t.Errorf("expected Build to fail when required pipeline param is unbound")
	}
}
