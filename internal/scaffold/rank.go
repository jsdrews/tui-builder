package scaffold

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jsdrews/tui-builder/internal/columns"
)

// identityPaths are where a row's identity is looked for first, before
// the mark key: a readable name beats a uid.
var identityPaths = []string{"name", "metadata.name", "title"}

// identityOf picks the identity column: name, metadata.name or title,
// else the mark key's field, else id. "" when there's none.
func identityOf(fs []*field, key string) string {
	for _, want := range append(slices.Clone(identityPaths), key, "id") {
		if want == "" {
			continue
		}
		if i := slices.IndexFunc(fs, func(f *field) bool { return strings.EqualFold(f.path, want) }); i >= 0 {
			return fs[i].path
		}
	}
	return ""
}

// classify ranks each field from what the sample shows of it.
func classify(fs []*field, key string) []columns.Column {
	id := identityOf(fs, key)
	out := make([]columns.Column, len(fs))
	for i, f := range fs {
		c := columns.Column{Path: f.path, Title: f.title, Sort: f.sort, Values: distinct(f.values)}
		name := lastSegment(f.path)
		switch {
		case f.path == id:
			c.Rank = columns.Identity
		case allBlank(f.values):
			c.Rank = columns.Empty
		case f.bools == f.set:
			c.Rank, c.Bool = columns.Status, true
		case columns.IsStatusName(name):
			c.Rank = columns.Status
		case !f.list && (columns.IsTimeName(name) || allTimestamps(f.values)):
			c.Rank = columns.Time
		case f.sort != "":
			c.Rank = columns.Number
		case averageLength(f.values) > 40:
			c.Rank = columns.Long
		case !f.list && f.strs == f.set && len(c.Values) >= 2 && len(c.Values) <= 8 && len(c.Values) < f.set:
			// A few values, repeated, reads as a state. One value in every
			// row says nothing, so it isn't one.
			c.Rank = columns.Status
		default:
			c.Rank = columns.Short
		}
		out[i] = c
	}
	return out
}

func distinct(values []string) []string {
	var out []string
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func allBlank(values []string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func averageLength(values []string) float64 {
	if len(values) == 0 {
		return 0
	}
	n := 0
	for _, v := range values {
		n += utf8.RuneCountInString(v)
	}
	return float64(n) / float64(len(values))
}

// timeLayouts are the timestamp shapes a value is recognised by.
var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04:05 -0700 MST", "2006-01-02"}

// allTimestamps is true when every value parses as a timestamp.
func allTimestamps(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		ok := false
		for _, l := range timeLayouts {
			if _, err := time.Parse(l, v); err == nil {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
