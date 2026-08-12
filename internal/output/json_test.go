package output

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// fakeSource: minimal one-shot ds.Source used by these tests.
type fakeSource struct {
	data any
	err  error
}

func (f *fakeSource) Fetch(_ context.Context) (any, error)  { return f.data, f.err }
func (f *fakeSource) Refresh() time.Duration                 { return 0 }

type fakeStreamer struct {
	fakeSource
	subscribeErr error
	events       []ds.Event
	closeAfter   bool // when true, close the channel after draining events
}

func (f *fakeStreamer) Subscribe(_ context.Context) (<-chan ds.Event, error) {
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	ch := make(chan ds.Event, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	if f.closeAfter {
		close(ch)
	}
	return ch, nil
}

func TestRunOneShotCompact(t *testing.T) {
	var buf bytes.Buffer
	src := &fakeSource{data: map[string]any{"k": "v"}}
	if err := Run(context.Background(), src, &buf, Options{}); err != nil {
		t.Fatal(err)
	}
	// Default is compact JSON with a trailing newline (one line per dump
	// keeps the contract uniform with NDJSON streams).
	got := buf.String()
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("one-shot output must end with newline, got %q", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Errorf("one-shot output must be valid JSON: %v (%q)", err, got)
	}
	if decoded["k"] != "v" {
		t.Errorf("round-trip lost data: %v", decoded)
	}
}

func TestRunOneShotPretty(t *testing.T) {
	var buf bytes.Buffer
	src := &fakeSource{data: map[string]any{"k": "v"}}
	if err := Run(context.Background(), src, &buf, Options{Pretty: true}); err != nil {
		t.Fatal(err)
	}
	// Pretty output uses two-space indent — should contain a literal
	// indented line. Compact mode has no internal newlines.
	if !strings.Contains(buf.String(), "  \"k\"") {
		t.Errorf("pretty output should be indented, got %q", buf.String())
	}
}

func TestRunOneShotFetchError(t *testing.T) {
	var buf bytes.Buffer
	src := &fakeSource{err: errors.New("boom")}
	err := Run(context.Background(), src, &buf, Options{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected fetch error to propagate, got %v", err)
	}
}

func TestRunStreamingEmitsOnePerLine(t *testing.T) {
	var buf bytes.Buffer
	src := &fakeStreamer{
		events: []ds.Event{
			{Line: `{"a":1}`},
			{Line: `{"a":2}`},
		},
		closeAfter: true,
	}
	if err := Run(context.Background(), src, &buf, Options{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
	for i, want := range []string{`{"a":1}`, `{"a":2}`} {
		if lines[i] != want {
			t.Errorf("line %d: want %s, got %s", i, want, lines[i])
		}
	}
}

func TestRunStreamingNonJSONLineWrapped(t *testing.T) {
	// Non-JSON lines (e.g. plain text from kube logs) get JSON-string
	// wrapped so the line protocol stays valid.
	var buf bytes.Buffer
	src := &fakeStreamer{
		events:     []ds.Event{{Line: "hello world"}},
		closeAfter: true,
	}
	if err := Run(context.Background(), src, &buf, Options{}); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(buf.String(), "\n")
	if got != `"hello world"` {
		t.Errorf("want JSON-string wrapped, got %q", got)
	}
}

func TestRunStreamingLimit(t *testing.T) {
	// Limit=2 → emit exactly 2 frames and stop, even if more queued.
	var buf bytes.Buffer
	src := &fakeStreamer{
		events: []ds.Event{
			{Line: `"a"`}, {Line: `"b"`}, {Line: `"c"`}, {Line: `"d"`},
		},
		closeAfter: true,
	}
	if err := Run(context.Background(), src, &buf, Options{Limit: 2}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("Limit=2: want 2 lines, got %d (%q)", len(lines), buf.String())
	}
}

func TestRunStreamingErrorEvent(t *testing.T) {
	// Per-event errors emit as `{"error":"..."}` lines and the stream
	// continues — they don't fail Run.
	var buf bytes.Buffer
	src := &fakeStreamer{
		events: []ds.Event{
			{Err: errors.New("transient")},
			{Line: `"ok"`},
		},
		closeAfter: true,
	}
	if err := Run(context.Background(), src, &buf, Options{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"error":"transient"`) {
		t.Errorf("error event should emit as JSON error line, got %q", out)
	}
	if !strings.Contains(out, `"ok"`) {
		t.Errorf("stream should continue after error event, got %q", out)
	}
}

func TestRunStreamingNotStreamingFallsBackToFetch(t *testing.T) {
	// A "streaming" source whose Subscribe returns ErrNotStreaming
	// should be treated as one-shot via Fetch.
	var buf bytes.Buffer
	src := &fakeStreamer{
		fakeSource:   fakeSource{data: "polled"},
		subscribeErr: ds.ErrNotStreaming,
	}
	if err := Run(context.Background(), src, &buf, Options{}); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimRight(buf.String(), "\n")
	if got != `"polled"` {
		t.Errorf("ErrNotStreaming should fall back to Fetch, got %q", got)
	}
}

func TestRunStreamingMaxDuration(t *testing.T) {
	// Slow producer + short MaxDuration: Run returns cleanly without
	// emitting anything. We use a channel that never produces.
	var buf bytes.Buffer
	src := &neverStreamer{}
	start := time.Now()
	err := Run(context.Background(), src, &buf, Options{MaxDuration: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("MaxDuration should cap consumption; elapsed=%v", elapsed)
	}
}

// neverStreamer returns a channel that never delivers anything — used
// to test MaxDuration without flakiness from real time.
type neverStreamer struct{}

func (n *neverStreamer) Fetch(_ context.Context) (any, error) { return nil, nil }
func (n *neverStreamer) Refresh() time.Duration               { return 0 }
func (n *neverStreamer) Subscribe(_ context.Context) (<-chan ds.Event, error) {
	return make(chan ds.Event), nil
}
