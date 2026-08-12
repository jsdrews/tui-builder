// Package datasource fetches arbitrary data and hands it to bound
// components. Sources are construct-once, fetch-many — the same Source
// is reused across polling ticks.
//
// Source kinds in v1:
//
//	http   GET (or other verb) a URL; parse JSON or keep as text.
//	exec   Run a command; capture stdout; parse it.
//	file   Read a file from disk.
//	merge  Fan out to N children, union their results, optionally tag
//	       each item with which child it came from. Composes any other
//	       source kind — works recursively (merge of merges) as long as
//	       the graph has no cycles (validated upstream).
//
// All four implement the same one-method Source interface, so any
// component-binding code (list / table / inspector / logview) consumes
// them identically.
package datasource

import (
	"context"
	"errors"
	"fmt"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Source fetches data on demand. Implementations are responsible for any
// per-fetch context (HTTP request, subprocess invocation, file read) but
// should be safe to call concurrently — the caller usually serializes
// via tea.Cmd anyway.
type Source interface {
	Fetch(ctx context.Context) (any, error)
	// Refresh is the polling interval, or 0 for fetch-once.
	Refresh() time.Duration
}

// StreamingSource pushes events over time instead of polling for full
// snapshots. v1 binds streams into logview only — each Event carries a
// line that's appended to the buffer as it arrives. (Streaming into
// list/table needs a keyed-update protocol that's deferred until a
// real use-case demands it.)
//
// Lifecycle:
//
//   - Subscribe returns immediately with a channel of events; the
//     source's own goroutine pushes events as data arrives. The given
//     context cancels the stream — when ctx.Done() fires the source
//     stops, closes its goroutine, and closes the events channel.
//   - The screen reads events one at a time via a Bubble Tea Cmd that
//     re-issues itself (the standard "channel-to-tea.Msg" bridge —
//     CLAUDE rule 12 in tuilib).
//   - When the channel is closed (graceful end of stream) or an Event
//     with a non-nil Err is delivered, the screen treats the stream as
//     finished. The user may force a reconnect via the refresh key.
type StreamingSource interface {
	Source
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// Event is a single update from a streaming source. v1 events carry a
// line (any printable text) destined for a logview's buffer. A
// non-empty Err signals a terminal error and ends the stream.
type Event struct {
	Line string
	Err  error
}

// ErrNotStreaming is the sentinel a StreamingSource's Subscribe returns
// to say "I can't stream under this configuration; fall back to my
// Fetch (polling) path." Used by merge when its children don't all
// implement StreamingSource — and by any future source that publishes
// a Subscribe method conditionally on config (e.g. exec without
// `follow: true`).
//
// Real failures (dial errors, write errors, auth rejections) surface
// as ordinary errors; the screen pops an alert for those. ErrNotStreaming
// is special: it tells the screen to ignore the streaming interface
// for this fetch and take the polling path instead.
var ErrNotStreaming = errors.New("source does not support streaming")

// Build constructs every leaf-kind source in the given map,
// resolving merge sources by recursively building children first.
// Used by this package's own tests; production code goes through
// pipeline.Build, which handles operator entries too.
func Build(sources map[string]*cfg.Source) (map[string]Source, error) {
	out := make(map[string]Source, len(sources))
	var build func(name string) (Source, error)
	build = func(name string) (Source, error) {
		if s, ok := out[name]; ok {
			return s, nil
		}
		s, ok := sources[name]
		if !ok {
			return nil, fmt.Errorf("source %q not defined", name)
		}
		var children map[string]Source
		for _, u := range s.Upstreams() {
			cs, err := build(u)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if children == nil {
				children = map[string]Source{}
			}
			children[u] = cs
		}
		built, err := BuildLeaf(s, children)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = built
		return built, nil
	}
	for name := range sources {
		if _, err := build(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// BuildLeaf constructs a single leaf-kind data source from a
// *cfg.Source. For merge sources, the caller pre-resolves children
// (via the unified Build in internal/pipeline that walks the entire
// graph) and passes the resolved map in.
//
// Returns an error for operator-kind sources; the caller dispatches
// those to internal/pipeline constructors instead.
func BuildLeaf(s *cfg.Source, children map[string]Source) (Source, error) {
	if s == nil {
		return nil, fmt.Errorf("BuildLeaf: nil source")
	}
	switch s.Type {
	case "http":
		return newHTTP(s)
	case "exec":
		return newExec(s)
	case "file":
		return newFile(s)
	case "websocket":
		return newWebsocket(s)
	case "static":
		return newStatic(s)
	case "merge":
		return newMerge(s, children)
	}
	return nil, fmt.Errorf("BuildLeaf: %q is not a leaf kind", s.Type)
}
