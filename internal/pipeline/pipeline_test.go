package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// fakeSource is a minimal in-memory ds.Source used by these tests.
// We need our own (rather than reusing http/exec/file) so the tests are
// hermetic — no network, no /tmp, no subprocesses.
type fakeSource struct {
	data    any
	err     error
	refresh time.Duration
	calls   int
}

func (f *fakeSource) Fetch(_ context.Context) (any, error) {
	f.calls++
	return f.data, f.err
}
func (f *fakeSource) Refresh() time.Duration { return f.refresh }

// fakeStreamer adds StreamingSource on top of fakeSource. Subscribe
// can be wired to return ErrNotStreaming (the fallback-to-polling
// sentinel) or a live event channel.
type fakeStreamer struct {
	fakeSource
	subscribeErr error
	events       []ds.Event
}

func (f *fakeStreamer) Subscribe(_ context.Context) (<-chan ds.Event, error) {
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	ch := make(chan ds.Event, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func TestPassthroughFetchDelegates(t *testing.T) {
	up := &fakeSource{data: []any{"a", "b"}, refresh: 30 * time.Second}
	p := &Pipeline{name: "p", upstream: up}

	if got := p.Refresh(); got != 30*time.Second {
		t.Errorf("Refresh: want 30s, got %v", got)
	}
	v, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := v.([]any)
	if !ok || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Fetch: want [a b], got %#v", v)
	}
	if up.calls != 1 {
		t.Errorf("upstream Fetch should be called exactly once; got %d", up.calls)
	}
}

func TestPassthroughSubscribeNotStreaming(t *testing.T) {
	// Wrapping a non-streaming source returns ErrNotStreaming so the
	// caller falls back to polling via Fetch. This matches the merge
	// source's convention.
	up := &fakeSource{}
	p := &Pipeline{name: "p", upstream: up}
	_, err := p.Subscribe(context.Background())
	if !errors.Is(err, ds.ErrNotStreaming) {
		t.Errorf("want ErrNotStreaming, got %v", err)
	}
}

func TestPassthroughSubscribeStreaming(t *testing.T) {
	// When the upstream IS a StreamingSource, Subscribe delegates.
	up := &fakeStreamer{
		events: []ds.Event{{Line: "one"}, {Line: "two"}},
	}
	p := &Pipeline{name: "p", upstream: up}
	ch, err := p.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for ev := range ch {
		lines = append(lines, ev.Line)
	}
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Errorf("delegated stream: want [one two], got %v", lines)
	}
}

func TestBuildResolvesPipelineChain(t *testing.T) {
	// a -> b -> source ("src")
	// Build should resolve in any order and the final pipeline a should
	// Fetch through b through src and return "leaf".
	src := &fakeSource{data: "leaf"}
	sources := map[string]ds.Source{"src": src}
	defs := map[string]*cfg.Pipeline{
		"a": {From: "b"},
		"b": {From: "src"},
	}
	reg, err := Build(sources, nil, defs, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get("a").Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "leaf" {
		t.Errorf("a -> b -> src: want \"leaf\", got %v", got)
	}
}

func TestBuildErrorsOnUnknownRef(t *testing.T) {
	// `from: nope` doesn't match any source or pipeline. Cycle detection
	// is enforced earlier by cfg.Config.Validate; Build only sees
	// unknown-name errors at this layer.
	defs := map[string]*cfg.Pipeline{"a": {From: "nope"}}
	if _, err := Build(map[string]ds.Source{}, nil, defs, nil); err == nil {
		t.Errorf("expected error for unknown ref, got nil")
	}
}

func TestRegistryGetAndKind(t *testing.T) {
	src := &fakeSource{}
	sources := map[string]ds.Source{"src": src}
	defs := map[string]*cfg.Pipeline{"p": {From: "src"}}
	reg, err := Build(sources, nil, defs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Get("missing") != nil {
		t.Errorf("Get(missing) should be nil")
	}
	if reg.Kind("missing") != "" {
		t.Errorf("Kind(missing) should be empty")
	}
	if reg.Kind("src") != "source" {
		t.Errorf("Kind(src): want source, got %q", reg.Kind("src"))
	}
	if reg.Kind("p") != "pipeline" {
		t.Errorf("Kind(p): want pipeline, got %q", reg.Kind("p"))
	}
}
