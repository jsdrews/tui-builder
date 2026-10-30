package scaffold

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// shortWindow makes stream sampling fast for a test.
func shortWindow(t *testing.T) {
	t.Helper()
	w, n := StreamWindow, StreamLines
	StreamWindow, StreamLines = 300*time.Millisecond, 5
	t.Cleanup(func() { StreamWindow, StreamLines = w, n })
}

func needSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
}

func TestRunDetectsStream(t *testing.T) {
	needSh(t)
	shortWindow(t)
	ctx := context.Background()
	// Prints, then keeps running: a stream, sampled and stopped.
	start := time.Now()
	out, err := Run(ctx, []string{"sh", "-c", `echo '{"a":1}'; echo '{"a":2}'; sleep 30`}, 10*time.Second, FollowAuto)
	if err != nil || !out.Stream || !out.Detected || strings.Count(string(out.Stdout), "\n") != 2 {
		t.Fatalf("stream → %+v, %v", out, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the stream wasn't stopped after the sample window")
	}
	// The line cap stops it early, and a half-printed line is dropped.
	out, err = Run(ctx, []string{"sh", "-c", `i=0; while true; do echo "line $i"; i=$((i+1)); done`}, 10*time.Second, FollowAuto)
	if err != nil || !out.Stream || strings.Count(string(out.Stdout), "\n") != StreamLines {
		t.Fatalf("a fast stream → %d lines, %v", strings.Count(string(out.Stdout), "\n"), err)
	}
	// Quiet past the window isn't a stream: it may print at the end.
	out, err = Run(ctx, []string{"sh", "-c", `sleep 0.5; echo '[{"a":1}]'`}, 10*time.Second, FollowAuto)
	if err != nil || out.Stream {
		t.Fatalf("a slow command → %+v, %v", out, err)
	}
}

func TestRunFollowFlags(t *testing.T) {
	needSh(t)
	shortWindow(t)
	ctx := context.Background()
	// --follow: sampled without waiting to see it's still running.
	out, err := Run(ctx, []string{"sh", "-c", `echo one; sleep 30`}, 10*time.Second, FollowYes)
	if err != nil || !out.Stream || out.Detected {
		t.Fatalf("--follow → %+v, %v", out, err)
	}
	// --follow on a command that ends early still samples it as a stream.
	out, err = Run(ctx, []string{"sh", "-c", `echo one`}, 10*time.Second, FollowYes)
	if err != nil || !out.Stream {
		t.Fatalf("--follow, ends early → %+v, %v", out, err)
	}
	// --follow with nothing printed: an error pointing at --no-run.
	if _, err := Run(ctx, []string{"sleep", "30"}, 10*time.Second, FollowYes); err == nil || !strings.Contains(err.Error(), "--no-run --follow") {
		t.Errorf("--follow, silent → %v", err)
	}
	// --no-follow: a stream is waited out, and fails at the timeout.
	if _, err := Run(ctx, []string{"sh", "-c", `echo one; sleep 30`}, 700*time.Millisecond, FollowNo); err == nil || !strings.Contains(err.Error(), "didn't finish within 700ms") {
		t.Errorf("--no-follow → %v", err)
	}
}

var streamOpts = ExecOptions{Argv: strings.Fields("kubectl get pods -w -o json")}

func TestJSONStreamTable(t *testing.T) {
	keyed := Output{Stream: true, Detected: true, Stdout: []byte(`{"metadata":{"name":"api","uid":"u1"},"status":{"phase":"Pending"}}
{"metadata":{"name":"api","uid":"u1"},"status":{"phase":"Running"}}
{"metadata":{"name":"web","uid":"u2"},"status":{"phase":"Running"}}
`)}
	res, err := ExecConfig(streamOpts, keyed)
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	src, comp := c.Data.Sources["pods"], c.TUI.Components["pods"]
	if !src.Follow || src.Format == "text" || comp.Type != "table" || len(comp.Columns) == 0 {
		t.Errorf("JSON stream:\n%s", res.Config)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "stream: follow: true (inferred: still running after") {
		t.Errorf("summary %q", res.Summary)
	}
	// The uid repeats across events: that's an update, so it keys the rows.
	if len(comp.RowKey) != 1 || comp.RowKey[0] != "metadata.uid" || comp.MaxRows != 0 {
		t.Errorf("events sharing a uid should be keyed by it:\n%s", res.Config)
	}
	noKey := Output{Stream: true, Stdout: []byte("{\"msg\":\"a\"}\n{\"msg\":\"b\"}\n")}
	res, err = ExecConfig(streamOpts, noKey)
	if err != nil {
		t.Fatal(err)
	}
	if comp := decode(t, res).TUI.Components["pods"]; comp.MaxRows != 100 || len(comp.RowKey) != 0 {
		t.Errorf("events with no id should be a ring buffer:\n%s", res.Config)
	}

	unique := Output{Stream: true, Stdout: []byte(`{"id":"a","msg":"x"}
{"id":"b","msg":"y"}
`)}
	res, err = ExecConfig(streamOpts, unique)
	if err != nil {
		t.Fatal(err)
	}
	if comp := decode(t, res).TUI.Components["pods"]; comp.RowKey[0] != "id" || comp.MaxRows != 0 {
		t.Errorf("a stable id should key the rows:\n%s", res.Config)
	}
}

func TestTextStreamLogview(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: strings.Fields("tail -f app.log"), Timeout: time.Minute}, Output{Stream: true, Stdout: []byte("started\nlistening on :80\n")})
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	src, comp := c.Data.Sources["tail"], c.TUI.Components["tail"]
	if !src.Follow || src.Format != "text" || comp.Type != "logview" || src.Timeout != "" {
		t.Errorf("text stream (no timeout on a stream):\n%s", res.Config)
	}
}

func TestPrettyJSONStreamWarns(t *testing.T) {
	pretty := Output{Stream: true, Stdout: []byte("{\n  \"id\": \"a\"\n}\n{\n  \"id\": \"b\"\n}\n")}
	res, err := ExecConfig(streamOpts, pretty)
	if err != nil {
		t.Fatal(err)
	}
	if decode(t, res).TUI.Components["pods"].Type != "table" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "jq -c") {
		t.Errorf("pretty stream: %q\n%s", res.Warnings, res.Config)
	}
}

func TestStreamContradictions(t *testing.T) {
	text := Output{Stream: true, Stdout: []byte("hello\n")}
	for _, tc := range []struct {
		opts ExecOptions
		want string
	}{
		{ExecOptions{Argv: []string{"t"}, Refresh: time.Second}, "drop --refresh"},
		{ExecOptions{Argv: []string{"t"}, Component: "table"}, "prints text"},
		{ExecOptions{Argv: []string{"t"}, Component: "inspector"}, "not a stream"},
		{ExecOptions{Argv: []string{"t"}, Component: "eventlog"}, "windowed"},
		{ExecOptions{Argv: []string{"t"}, Root: "items"}, "no root"},
	} {
		if _, err := ExecConfig(tc.opts, text); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.opts, err, tc.want)
		}
	}
	// A JSON stream can still be shown raw.
	res, err := ExecConfig(ExecOptions{Argv: []string{"t"}, Component: "logview"}, Output{Stream: true, Stdout: []byte("{\"a\":1}\n")})
	if err != nil || decode(t, res).TUI.Components["t"].Type != "logview" {
		t.Errorf("JSON stream as a logview: %v", err)
	}
}

func TestNoRunFollow(t *testing.T) {
	res, err := ExecConfigNoRun(ExecOptions{Argv: strings.Fields("kubectl logs -f api"), Follow: FollowYes})
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	if !c.Data.Sources["logs"].Follow || c.TUI.Components["logs"].Type != "logview" {
		t.Errorf("--no-run --follow:\n%s", res.Config)
	}
	res, err = ExecConfigNoRun(ExecOptions{Argv: strings.Fields("kubectl get pods -w"), Follow: FollowYes, Columns: []ColumnSpec{{"metadata.name", "Pod"}}})
	if err != nil {
		t.Fatal(err)
	}
	if comp := decode(t, res).TUI.Components["pods"]; comp.Type != "table" || comp.MaxRows != 100 {
		t.Errorf("--no-run --follow --columns:\n%s", res.Config)
	}
}
