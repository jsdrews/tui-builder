package datasource

import (
	"context"
	"errors"
	"regexp"
	"strconv"
)

// WindowedSource fetches a slice of a larger remote set instead of the
// whole thing, and lets the server answer the filter and the sort.
//
// It is the third optional extension of Source, alongside
// StreamingSource, and it earns that the same way: the lifecycle is
// genuinely different. Source.Fetch says "give me everything"; a windowed
// source is one where everything is more rows than anyone wants to hold,
// so the question becomes "give me rows 400-499 matching author=tolkien,
// newest first" — and the answer changes as the user scrolls and types.
//
// Implementations still satisfy Source: Fetch returns the first page, so
// `wrangl` and any non-table consumer get something sensible rather than
// an error. Only the bound table drives FetchWindow.
//
// The types here deliberately mirror tuilib's pkg/source.Query and
// pkg/table's window setter without importing either — the data layer
// never imports the TUI (see AGENTS.md). internal/screen owns the
// translation, which is where both halves are already in scope.
type WindowedSource interface {
	Source
	FetchWindow(ctx context.Context, q WindowQuery) (WindowPage, error)
}

// WindowQuery is one request's worth of parameters: which rows, matching
// what, in what order. It carries no transport detail — how to reach the
// source is the implementation's business.
type WindowQuery struct {
	// Offset is the index of the first row wanted.
	Offset int
	// Limit is how many rows are wanted.
	Limit int

	// Search is the filter text the user typed with no column scope, all
	// bare terms joined by a space. Empty when the filter is empty or
	// every term was scoped.
	Search string
	// Filters maps a column *title* to the value the user scoped it to,
	// from "title:value" terms. Titles are resolved against the bound
	// table's columns before they get here, so an implementation can
	// look them up directly.
	Filters map[string]string

	// Sort is the title of the column to order by, "" when unsorted.
	Sort string
	// Desc reverses the order.
	Desc bool
}

// WindowPage is what one FetchWindow returned.
type WindowPage struct {
	// Items are the rows in this window, already sliced by the source's
	// `root:` the same way Fetch would.
	Items []any
	// Total is the number of rows matching the query across every page,
	// or -1 when the source can't say. -1 is not an error: the table
	// then treats the end of what has loaded as the end of the set, and
	// grows as more arrives.
	Total int
}

// ErrNotWindowed is the sentinel a WindowedSource returns when it can't
// serve windows under its current configuration — the same role
// ErrNotStreaming plays for Subscribe. The caller falls back to Fetch.
var ErrNotWindowed = errors.New("source does not support windowed fetch")

// windowTokenRe matches one ${window.*} reference. The name half is
// permissive because filter tokens carry a user-chosen suffix
// (${window.filters.author}); unknown names resolve empty, same as an
// undeclared ${params.X}.
var windowTokenRe = regexp.MustCompile(`\$\{window\.([a-zA-Z0-9_.]+)\}`)

// renderWindowArgv substitutes ${window.*} tokens into an argv and drops
// the elements that end up carrying nothing.
//
// The drop rule: an element is omitted when it referenced at least one
// window token and *every* token it referenced resolved empty. That is
// what makes the `--author=${window.filters.author}` idiom work — with
// no author: term typed, the whole flag disappears rather than being
// passed as `--author=`, which most CLIs read as "match the empty
// string" and would return nothing.
//
// Elements with no window tokens are always kept, and an element mixing
// an always-present token with an empty one is kept too: in
//
//	sh -c "… LIMIT ${window.limit} AND name LIKE '%${window.filters.name}%'"
//
// limit is never empty, so the element survives and the empty filter
// degrades to a LIKE '%%' that matches everything — which is the right
// answer for an unset filter.
//
// Substitution is literal, matching how ${params.*} already behaves in
// argv. That means a filter value containing shell metacharacters lands
// verbatim, so a `sh -c` template is only as safe as the quoting around
// it. Prefer direct argv (no shell) when the values are user-typed —
// see the docs.
func renderWindowArgv(argv []string, q WindowQuery, filters map[string]string) []string {
	vals := windowTokens(q, filters)
	out := make([]string, 0, len(argv))
	for _, elem := range argv {
		saw, nonEmpty := false, false
		rendered := windowTokenRe.ReplaceAllStringFunc(elem, func(tok string) string {
			saw = true
			name := windowTokenRe.FindStringSubmatch(tok)[1]
			v := vals[name]
			if v != "" {
				nonEmpty = true
			}
			return v
		})
		if saw && !nonEmpty {
			continue
		}
		out = append(out, rendered)
	}
	return out
}

// windowTokens flattens a query into the ${window.*} namespace.
//
// filters maps a column title to the token suffix it answers to, so a
// term the user typed as "author:tolkien" against the "Author" column
// reaches the command as ${window.filters.author}. The indirection is
// deliberate: column titles are display text ("First published") and
// make poor token names, and the same mapping already exists on the
// http side pointing at query parameters instead.
func windowTokens(q WindowQuery, filters map[string]string) map[string]string {
	vals := map[string]string{
		"offset": strconv.Itoa(q.Offset),
		"limit":  strconv.Itoa(q.Limit),
		"search": q.Search,
		"sort":   q.Sort,
	}
	// sort_dir is separate from sort so a command can spell the
	// direction its own way ("ORDER BY x ${window.sort_dir}") without
	// the source guessing at a prefix convention.
	if q.Sort != "" {
		vals["sort_dir"] = "asc"
		if q.Desc {
			vals["sort_dir"] = "desc"
		}
	}
	for title, token := range filters {
		if v, ok := q.Filters[title]; ok {
			vals["filters."+token] = v
		}
	}
	return vals
}
