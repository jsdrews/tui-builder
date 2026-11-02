package scaffold

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// stream plans a source for a command that keeps running: follow: true,
// shown by a streaming table when it prints a JSON object per line, or a
// logview when it prints text.
func (p *plan) stream(out Output) error {
	how := "--follow"
	if out.Detected {
		how = fmt.Sprintf("inferred: still running after %s", StreamWindow)
	}
	p.follow = true
	p.note("stream", "follow: true", how)
	if p.opts.Refresh > 0 {
		return fmt.Errorf("--refresh polls a command that finishes, and this one streams; drop --refresh, or pass --no-follow if it does finish")
	}
	if p.opts.Root != "" {
		return fmt.Errorf("--root %s: a stream's events are read one per line, whole, so there's no root to pick", p.opts.Root)
	}
	rows, pretty := jsonLines(out.Stdout)
	kind := p.opts.Component
	if kind == "" {
		kind = "logview"
		if rows != nil {
			kind = "table"
		}
	}
	if pretty {
		p.res.Warnings = append(p.res.Warnings, "the command prints each JSON value over several lines, and a stream is read one line per event, "+
			"so the table will stay empty: ask for compact output (jq -c, or the tool's own flag)")
	}
	switch kind {
	case "logview":
		p.text = rows == nil
		p.kind = kind
		p.comp = mapping("type", scalar("logview"), "title", scalar(p.title()), "source", scalar(p.name), "searchable", yes())
	case "table":
		if rows == nil {
			return fmt.Errorf("--component table: the stream prints text, not a JSON object per line")
		}
		if err := p.streamTable(rows); err != nil {
			return err
		}
	case "inspector", "list":
		return fmt.Errorf("--component %s: a %s shows a value that's fetched, not a stream; use a table or logview, or --no-follow if the command finishes", kind, kind)
	default:
		return p.component(kind, nil)
	}
	p.note("component", kind, flagIf(p.opts.Component != "", "--component"))
	return nil
}

// streamTable is a table filled by events: keyed by an id-like field
// every event carries, so each event updates its row, else a ring
// buffer of the newest 100. Unlike a mark key, the field may repeat
// across the sample: a repeat is an update.
func (p *plan) streamTable(rows []*object) error {
	cols, _, err := p.columns(rows)
	if err != nil {
		return err
	}
	key := ""
	if len(rows) > 0 {
		key = everyRowKey(fields(rows), len(rows))
	}
	p.kind = "table"
	p.comp, p.more = p.table(p.title(), cols, key)
	if key != "" {
		add(p.comp, "row_key", scalar(key))
		p.note("row_key", key+", each event updates its row", "")
	} else {
		add(p.comp, "max_rows", intNode(100))
		p.note("rows", "the newest 100, newest first", "")
	}
	return nil
}

// unsampledStream is --no-run --follow: a stream source shown by a
// logview, the one component that needs no shape, or a table of the
// --columns given.
func (p *plan) unsampledStream() (Result, error) {
	p.follow = true
	p.note("stream", "follow: true", "--follow")
	if p.opts.Refresh > 0 {
		return Result{}, fmt.Errorf("--refresh polls a command that finishes, and --follow says this one streams")
	}
	kind := p.opts.Component
	if kind == "" {
		kind = "logview"
		if len(p.opts.Columns) > 0 {
			kind = "table"
		}
	}
	switch kind {
	case "logview":
		p.kind = kind
		p.comp = mapping("type", scalar("logview"), "title", scalar(p.title()), "source", scalar(p.name), "searchable", yes())
	case "table":
		if len(p.opts.Columns) == 0 {
			return Result{}, fmt.Errorf("--component table with --no-run needs --columns: there's no sample to take them from")
		}
		if err := p.streamTable(nil); err != nil {
			return Result{}, err
		}
	default:
		return Result{}, fmt.Errorf("--component %s: a stream is shown by a table or a logview", kind)
	}
	p.note("component", kind, flagIf(p.opts.Component != "", "--component"))
	return p.finish()
}

// jsonLines reads a stream's sample as events. rows is set when every
// non-blank line is one JSON object; pretty when the lines aren't, but
// together they're JSON objects printed over several lines each.
func jsonLines(b []byte) (rows []*object, pretty bool) {
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		v, err := decodeValue(dec)
		o, isObj := v.(*object)
		if err != nil || !isObj || dec.More() {
			rows = nil
			break
		}
		rows = append(rows, o)
	}
	if rows != nil {
		return rows, false
	}
	values, err := decodeSample(Output{Stdout: bytes.TrimSpace(b), Truncated: true})
	if err != nil || len(values) == 0 {
		return nil, false
	}
	for _, v := range values {
		o, ok := v.(*object)
		if !ok {
			return nil, false
		}
		rows = append(rows, o)
	}
	return rows, true
}

// title is the component's title: --title, else the name title-cased.
func (p *plan) title() string {
	if p.opts.Title != "" {
		return p.opts.Title
	}
	return titleCase(p.name)
}
