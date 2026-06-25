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

// Build constructs every defined data source. Leaves (http / exec /
// file) are built first; merge sources are built last with their
// resolved children attached. Cycles among merges must already have
// been rejected by config.Validate.
//
// Returns a map keyed by source name. screen.Model holds onto this and
// looks up sources by name when wiring component bindings.
func Build(defs map[string]*cfg.DataSource) (map[string]Source, error) {
	out := make(map[string]Source, len(defs))

	// Recursive builder. Since config.Validate rejects cycles we don't
	// need a re-entry guard, but the memoized out[name] check also
	// serves as one.
	var build func(name string) (Source, error)
	build = func(name string) (Source, error) {
		if s, ok := out[name]; ok {
			return s, nil
		}
		def, ok := defs[name]
		if !ok {
			return nil, fmt.Errorf("source %q not defined", name)
		}
		var (
			s   Source
			err error
		)
		switch def.Type {
		case "merge":
			// Resolve children from BOTH legal shapes — the shorthand
			// (Sources + TagField) and the explicit per-child form
			// (Children with tags). The validator already ensures
			// exactly one is set.
			childRefs := def.Sources
			for _, ch := range def.Children {
				childRefs = append(childRefs, ch.Source)
			}
			children := make(map[string]Source, len(childRefs))
			for _, child := range childRefs {
				cs, cerr := build(child)
				if cerr != nil {
					return nil, fmt.Errorf("%s: %w", name, cerr)
				}
				children[child] = cs
			}
			s, err = newMerge(def, children)
		default:
			s, err = newLeaf(def)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = s
		return s, nil
	}
	for name := range defs {
		if _, err := build(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// newLeaf dispatches a single non-merge source.
func newLeaf(d *cfg.DataSource) (Source, error) {
	switch d.Type {
	case "http":
		return newHTTP(d)
	case "exec":
		return newExec(d)
	case "file":
		return newFile(d)
	case "websocket":
		return newWebsocket(d)
	}
	return nil, fmt.Errorf("unknown data source type %q", d.Type)
}

// New is the legacy single-source constructor — kept for tests / callers
// that don't have a full Config. Production code should use Build so
// merge sources can resolve their children.
func New(d *cfg.DataSource) (Source, error) {
	if d.Type == "merge" {
		return nil, fmt.Errorf("merge sources require Build (need to resolve children)")
	}
	return newLeaf(d)
}
