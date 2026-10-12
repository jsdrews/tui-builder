package datasource

import (
	"context"
	"strings"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// ResumableSource is a stream that can be asked for what it sent since a
// time: the ${resume.since} token in its request. The screen tracks each
// line's timestamp and reconnects from the last one it showed, skipping
// what it already has (see `resume:`). Subscribe is SubscribeSince with
// since empty — the first connect, from the live tail.
type ResumableSource interface {
	StreamingSource
	SubscribeSince(ctx context.Context, since string) (<-chan Event, error)
}

func resumeOne(s, since string) string { return strings.ReplaceAll(s, cfg.ResumeSince, since) }

func resumeAll(in []string, since string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = resumeOne(s, since)
	}
	return out
}

func resumeMap(in map[string]string, since string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = resumeOne(v, since)
	}
	return out
}
