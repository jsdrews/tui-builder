package screen

// Windowed sources: tables bound to a source that declares `window:`.
//
// A normal source is fetched once (or on a timer) and the whole result is
// pushed into every bound component. A windowed source can't work that
// way: the set is bigger than anyone wants to hold, so the table asks for
// the rows it is showing and the source answers the filter and the sort.
//
// tuilib's pkg/remote runs that loop. The table is a remote.Table built
// over a Seekable whose Page calls the data layer's FetchWindow; remote
// decides when to fetch, cancels requests a newer one supersedes, keeps
// stale rows dimmed while a new query loads, marks a failed one, and
// reports each query's outcome to the shell's query history under the
// source's name. What stays here is what remote can't know:
//
//   - Addressing. The table's ViewportChangedMsg and QueryChangedMsg
//     don't name their sender, and remote.Table acts on any it's handed.
//     A screen can hold two windowed tables, and a covered screen still
//     receives the top screen's messages, so each one is tagged with the
//     emitting component (translateWindowMsg), scoped to this screen (see
//     scope.go), and handed only to that component's remote table.
//   - Polling. `refresh:` on a windowed source re-requests the window on
//     screen on a timer.
//   - The query mapping. translateWindowQuery turns tuilib's filter terms
//     into the data layer's WindowQuery, which never imports tuilib.

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/query"
	"github.com/jsdrews/tuilib/pkg/remote"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// windowEntry is one windowed table: its component, the source it pulls
// from, and that source's polling interval.
type windowEntry struct {
	component string // component name in tree.Components
	source    string // source registry name
	// refresh is the source's polling interval, 0 for fetch-once. A
	// windowed source polls by re-requesting the window on screen rather
	// than refetching a whole result set.
	refresh time.Duration
}

// windowViewportMsg and windowQueryMsg carry a table's viewport and
// query messages, tagged with the component that emitted them.
type windowViewportMsg struct {
	component string
	msg       table.ViewportChangedMsg
}

type windowQueryMsg struct {
	component string
	msg       table.QueryChangedMsg
}

// windowTickMsg re-requests the window on screen for a polled source.
type windowTickMsg struct{ component string }

// initWindows turns every table bound to a source that can serve windows
// into a remote table. Called from build_ after the source registry is
// populated.
//
// A component marked Windowed in config whose built source doesn't
// actually implement ds.WindowedSource is left alone: it falls back to
// the ordinary Fetch path, which for an http source is its first page.
// Config validation already rejects the combinations that would make
// that misleading (an operator over a windowed source, a non-table
// binding), so this is the belt to that suspenders.
func (m *Model) initWindows() {
	for _, name := range m.tree.Order {
		c := m.tree.Components[name]
		if c.Cfg == nil || !c.Cfg.Windowed || c.Kind != build.KTable {
			continue
		}
		entry, ok := m.sources[c.Cfg.Source]
		if !ok {
			continue
		}
		ws, ok := entry.src.(ds.WindowedSource)
		if !ok {
			continue
		}
		if m.windows == nil {
			m.windows = map[string]*windowEntry{}
		}
		def := m.windowDefs[c.Cfg.Source]
		shape := remote.Seekable[table.KeyedRow]{
			Page: windowPage(ws, c, m.windowFilters(c.Cfg.Source), m.th),
			// Prefixes this source's lines in the shell's query history.
			Name: c.Cfg.Source,
		}
		if def != nil {
			shape.PageSize, shape.Prefetch = def.PageSize, def.Prefetch
		}
		build.NewRemoteTable(c, m.th, shape)
		m.windows[name] = &windowEntry{
			component: name,
			source:    c.Cfg.Source,
			refresh:   entry.src.Refresh(),
		}
	}
}

// windowPage is the remote table's fetch: one FetchWindow, with the rows
// projected through the table's columns. It runs off the UI goroutine,
// and ctx is cancelled when a newer request supersedes it.
//
// Rows are rendered with the theme current when the table was built; a
// theme swap restyles the table but not the cells already fetched, the
// same as for an ordinary table.
func windowPage(src ds.WindowedSource, c *build.Component, mapped map[string]string, th theme.Theme) func(context.Context, remote.Window) ([]table.KeyedRow, int, error) {
	return func(ctx context.Context, w remote.Window) ([]table.KeyedRow, int, error) {
		page, err := src.FetchWindow(ctx, translateWindowQuery(w, mapped))
		if err != nil {
			return nil, 0, err
		}
		rows := build.TableRows(c, page.Items, th)
		out := make([]table.KeyedRow, len(rows))
		for i, r := range rows {
			out[i] = table.KeyedRow{Cells: r}
		}
		return out, page.Total, nil
	}
}

// startWindows returns the opening request for every windowed table, and
// starts the poll for the ones whose source refreshes. Batched into
// OnEnter alongside the ordinary first-fetch wave.
func (m *Model) startWindows() []tea.Cmd {
	var cmds []tea.Cmd
	for name, w := range m.windows {
		if c := m.tree.Components[name]; c != nil && c.Remote != nil {
			cmds = append(cmds, tagCursorFocused(c.Remote.Init(), name))
		}
		if w.refresh > 0 {
			cmds = append(cmds, windowTick(name, w.refresh))
		}
	}
	return cmds
}

// windowTick schedules the next poll for a windowed component.
func windowTick(component string, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return windowTickMsg{component: component}
	})
}

// remoteFor returns the remote table behind a windowed component, or nil.
func (m *Model) remoteFor(component string) *remote.Table {
	if _, ok := m.windows[component]; !ok {
		return nil
	}
	if c := m.tree.Components[component]; c != nil {
		return c.Remote
	}
	return nil
}

// handleWindowViewport hands a table's viewport change to its own remote
// table, which decides whether the rows now on screen need a fetch.
func (m *Model) handleWindowViewport(msg windowViewportMsg) tea.Cmd {
	rt := m.remoteFor(msg.component)
	if rt == nil {
		return nil
	}
	return tagCursorFocused(rt.Update(msg.msg), msg.component)
}

// handleWindowQuery hands a committed filter or sort to its own remote
// table. The old rows stay on screen, dimmed, until the answer lands.
func (m *Model) handleWindowQuery(msg windowQueryMsg) tea.Cmd {
	rt := m.remoteFor(msg.component)
	if rt == nil {
		return nil
	}
	return tagCursorFocused(rt.Update(msg.msg), msg.component)
}

// handleWindowTick re-requests the window on screen and re-arms the
// timer. Polling a windowed source means "same rows, fresh data" — it
// does not walk back to page one, which would yank the user's place.
func (m *Model) handleWindowTick(msg windowTickMsg) tea.Cmd {
	rt := m.remoteFor(msg.component)
	if rt == nil {
		return nil
	}
	cmds := []tea.Cmd{tagCursorFocused(rt.Refresh(), msg.component)}
	if w := m.windows[msg.component]; w.refresh > 0 {
		cmds = append(cmds, windowTick(msg.component, w.refresh))
	}
	return tea.Batch(cmds...)
}

// refreshWindows re-requests every windowed table's current window. The
// manual 'r' key routes here instead of startFetch, whose SetRows would
// collapse the window into the page it happens to hold. It's also the
// retry after a failed query: the table keeps what was typed.
func (m *Model) refreshWindows() []tea.Cmd {
	var cmds []tea.Cmd
	for name := range m.windows {
		if rt := m.remoteFor(name); rt != nil {
			cmds = append(cmds, tagCursorFocused(rt.Refresh(), name))
		}
	}
	return cmds
}

// translateWindowQuery maps tuilib's query onto the data layer's, which
// is where the two halves meet: pkg/source and pkg/query are TUI-side,
// ds.WindowQuery is data-side, and the data layer never imports tuilib.
//
// Term routing:
//   - a scoped term ("author:tolkien") whose column the source maps in
//     `window.filters:` becomes Filters[Title]
//   - everything else — bare terms, and scoped terms on unmapped columns
//     — joins into Search, because a term the source can't scope is
//     still a term the user meant to search for
//
// Regex terms ("~^new") can't cross an HTTP query string, so they travel
// as their literal text minus the tilde. The server answers a substring
// search over it, which is narrower than the user asked for but is a
// strict subset rather than a wrong answer.
func translateWindowQuery(q remote.Window, mapped map[string]string) ds.WindowQuery {
	out := ds.WindowQuery{
		Offset: q.Offset,
		Limit:  q.Limit,
		Sort:   q.Sort,
		Desc:   q.Desc,
	}
	var bare []string
	for _, t := range q.Terms {
		val := termValue(t)
		if t.Title != "" {
			if _, ok := mapped[t.Title]; ok {
				if out.Filters == nil {
					out.Filters = map[string]string{}
				}
				out.Filters[t.Title] = val
				continue
			}
		}
		bare = append(bare, val)
	}
	out.Search = strings.Join(bare, " ")
	return out
}

// termValue is the text a term should send to the source: its literal
// value, or for a regex term the pattern as typed with the "~" removed.
func termValue(t query.Term) string {
	if t.Regex == nil {
		return t.Value
	}
	raw := t.Raw
	if i := strings.Index(raw, ":"); t.Title != "" && i > 0 {
		raw = raw[i+1:]
	}
	return strings.TrimPrefix(raw, "~")
}

// windowedSource reports whether any windowed table pulls from
// this source — the cue for the ordinary fetch paths to leave it alone.
func (m *Model) windowedSource(name string) bool {
	for _, w := range m.windows {
		if w.source == name {
			return true
		}
	}
	return false
}

// windowFilters returns a source's column-title → query-parameter map,
// or nil when it declares none (in which case every term is bare and
// lands in Search).
func (m *Model) windowFilters(source string) map[string]string {
	if def, ok := m.windowDefs[source]; ok && def != nil {
		return def.Filters
	}
	return nil
}

// translateWindowMsg tags the two table messages a windowed source needs.
// Called from translateFocusMsg, which already wraps every component's
// returned Cmd and knows which component produced it.
func translateWindowMsg(msg tea.Msg, name string) tea.Msg {
	switch x := msg.(type) {
	case table.ViewportChangedMsg:
		return windowViewportMsg{component: name, msg: x}
	case table.QueryChangedMsg:
		return windowQueryMsg{component: name, msg: x}
	}
	return nil
}
