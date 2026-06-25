package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

func TestComposeBundlesHeterogeneousChildren(t *testing.T) {
	// Two upstreams with completely different shapes; compose preserves
	// each shape under its caller-chosen key.
	pods := &fakeSource{data: []any{
		map[string]any{"name": "pod-a", "phase": "Running"},
	}}
	deployments := &fakeSource{data: []any{
		map[string]any{"name": "dep-a", "replicas": 3},
	}}
	reg, err := Build(
		map[string]ds.Source{"pods": pods, "deployments": deployments},
		nil,
		map[string]*cfg.Pipeline{
			"fleet": {Compose: &cfg.ComposeOp{
				Parts: map[string]string{
					"p": "pods",
					"d": "deployments",
				},
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("fleet").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("want map[string]any, got %T", got)
	}
	if _, ok := out["p"]; !ok {
		t.Errorf("missing key p")
	}
	if _, ok := out["d"]; !ok {
		t.Errorf("missing key d")
	}
	// Verify each bucket retains its original shape.
	podsOut := out["p"].([]any)
	if podsOut[0].(map[string]any)["phase"] != "Running" {
		t.Errorf("pods shape lost: %v", podsOut)
	}
	depsOut := out["d"].([]any)
	if depsOut[0].(map[string]any)["replicas"] != 3 {
		t.Errorf("deployments shape lost: %v", depsOut)
	}
}

// TestComposeAcceptsPipelineAsChild parallels the union case: compose
// children can be other pipelines (filters, projections, derives,
// even other composes), not just leaf sources.
func TestComposeAcceptsPipelineAsChild(t *testing.T) {
	pods := &fakeSource{data: []any{
		map[string]any{"name": "a", "phase": "Running"},
		map[string]any{"name": "b", "phase": "Pending"},
	}}
	reg, err := Build(
		map[string]ds.Source{"pods": pods},
		nil,
		map[string]*cfg.Pipeline{
			"running_pods": {Filter: &cfg.FilterOp{From: "pods", Where: "phase == 'Running'"}},
			"fleet": {Compose: &cfg.ComposeOp{
				Parts: map[string]string{
					"all":     "pods",
					"running": "running_pods",
				},
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("fleet").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := got.(map[string]any)
	if len(out["all"].([]any)) != 2 {
		t.Errorf("all bucket = %v, want 2 items", out["all"])
	}
	if len(out["running"].([]any)) != 1 {
		t.Errorf("running bucket = %v, want 1 item", out["running"])
	}
}

func TestComposeFailOnChildError(t *testing.T) {
	good := &fakeSource{data: []any{1}}
	bad := &fakeSource{err: errors.New("boom")}
	reg, err := Build(
		map[string]ds.Source{"good": good, "bad": bad},
		nil,
		map[string]*cfg.Pipeline{
			"both": {Compose: &cfg.ComposeOp{
				Parts: map[string]string{"g": "good", "b": "bad"},
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Get("both").Fetch(context.Background())
	if err == nil {
		t.Errorf("expected compose to fail on child error (default behavior)")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should mention the underlying boom; got: %v", err)
	}
}

func TestComposeSkipDropsFailedChild(t *testing.T) {
	good := &fakeSource{data: []any{"good_data"}}
	bad := &fakeSource{err: errors.New("boom")}
	reg, err := Build(
		map[string]ds.Source{"good": good, "bad": bad},
		nil,
		map[string]*cfg.Pipeline{
			"both": {Compose: &cfg.ComposeOp{
				Parts:   map[string]string{"g": "good", "b": "bad"},
				OnError: "skip",
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("both").Fetch(context.Background())
	if err != nil {
		t.Fatalf("skip mode shouldn't error when one child survives: %v", err)
	}
	out := got.(map[string]any)
	if _, ok := out["g"]; !ok {
		t.Errorf("good child should be in output, got %v", out)
	}
	if _, ok := out["b"]; ok {
		t.Errorf("failed child should be absent, got %v", out)
	}
}

func TestComposeSkipAllFailedSurfaces(t *testing.T) {
	// When EVERY child fails under skip, surface a combined error.
	a := &fakeSource{err: errors.New("a-boom")}
	b := &fakeSource{err: errors.New("b-boom")}
	reg, err := Build(
		map[string]ds.Source{"a": a, "b": b},
		nil,
		map[string]*cfg.Pipeline{
			"both": {Compose: &cfg.ComposeOp{
				Parts:   map[string]string{"x": "a", "y": "b"},
				OnError: "skip",
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Get("both").Fetch(context.Background())
	if err == nil {
		t.Errorf("expected combined error when every child fails")
	}
	if !strings.Contains(err.Error(), "a-boom") || !strings.Contains(err.Error(), "b-boom") {
		t.Errorf("combined error should name both children; got: %v", err)
	}
}

func TestComposeSubscribeReturnsNotStreaming(t *testing.T) {
	a := &fakeSource{data: []any{1}}
	reg, err := Build(
		map[string]ds.Source{"a": a},
		nil,
		map[string]*cfg.Pipeline{
			"comp": {Compose: &cfg.ComposeOp{
				Parts: map[string]string{"x": "a"},
			}},
		},
	nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Get("comp").(ds.StreamingSource).Subscribe(context.Background())
	if !errors.Is(err, ds.ErrNotStreaming) {
		t.Errorf("compose should return ErrNotStreaming; got %v", err)
	}
}

func TestComposeValidatorRejectsEmptyParts(t *testing.T) {
	c := cfg.Config{
		DataSources: map[string]*cfg.DataSource{
			"a": {Type: "exec", Command: []string{"true"}},
		},
		Pipelines: map[string]*cfg.Pipeline{
			"bad": {Compose: &cfg.ComposeOp{}},
		},
		Components: map[string]*cfg.Component{
			"x": {Type: "list", Items: []string{"x"}},
		},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "x"}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject empty parts")
	}
}

func TestComposeValidatorRejectsBadOnError(t *testing.T) {
	c := cfg.Config{
		DataSources: map[string]*cfg.DataSource{
			"a": {Type: "exec", Command: []string{"true"}},
		},
		Pipelines: map[string]*cfg.Pipeline{
			"bad": {Compose: &cfg.ComposeOp{
				Parts:   map[string]string{"x": "a"},
				OnError: "random",
			}},
		},
		Components: map[string]*cfg.Component{
			"y": {Type: "list", Items: []string{"y"}},
		},
		Screen: cfg.Screen{Layout: cfg.Node{Component: "y"}},
	}
	if err := c.Validate(); err == nil {
		t.Errorf("expected validator to reject on_error=random")
	}
}
