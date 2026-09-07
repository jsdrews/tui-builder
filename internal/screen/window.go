package screen

// Windowed sources: the screen half of tuilib's pkg/source loop.
//
// A normal source is fetched once (or on a timer) and the whole result is
// pushed into every bound component. A windowed source can't work that
// way — the set is bigger than anyone wants to hold, so the table asks
// for the rows it is showing and the source answers the filter and the
// sort along the way.
//
// The loop, and who owns each step:
//
//	OnEnter          → coord.Init()             → windowRequestMsg
//	windowRequestMsg → FetchWindow (a tea.Cmd)  → windowFetchedMsg
//	windowFetchedMsg → coord.Deliver + ApplyWindow
//	ApplyWindow      → ViewportChangedMsg       → coord.Viewport → request?
//	QueryChangedMsg  → coord.SetQuery           → windowRequestMsg
//
// Installing a window makes the table emit a fresh ViewportChangedMsg,
// which closes the loop: a short page that still doesn't fill the screen
// asks for the rest by itself.
//
// tuilib's coordinator emits an untagged source.RequestMsg, and the
// table's viewport/query messages don't name their sender either. A
// screen can hold more than one windowed table, so every one of those is
// rewritten to carry the emitting component's name before it reaches
// Update — the same trick tagCursorFocused plays for RowFocusedMsg.

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/query"
	tsource "github.com/jsdrews/tuilib/pkg/source"
	"github.com/jsdrews/tuilib/pkg/table"

	"github.com/jsdrews/tui-builder/internal/build"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// windowEntry is one windowed table: the component it draws into, the
// source it pulls from, and tuilib's coordinator tracking which window is
// held and which is in flight.
type windowEntry struct {
	component string // component name in tree.Components
	source    string // source registry name
	src       ds.WindowedSource
	coord     tsource.Model
	// refresh is the source's polling interval, 0 for fetch-once. A
	// windowed source polls by re-requesting the window on screen rather
	// than refetching a whole result set.
	refresh time.Duration
}

// windowRequestMsg is a tagged tuilib source.RequestMsg — "component X's
// coordinator wants this window."
type windowRequestMsg struct {
	component string
	query     tsource.Query
}

// windowFetchedMsg carries one window back into Update. gen echoes the
// requesting query's generation so a reply the user has already scrolled
// past can be dropped rather than painted.
type windowFetchedMsg struct {
	component string
	gen       int
	offset    int
	page      ds.WindowPage
	err       error
}

// windowViewportMsg is a tagged table.ViewportChangedMsg.
type windowViewportMsg struct {
	component   string
	first, last int
}

// windowQueryMsg is a tagged table.QueryChangedMsg — the filter the user
// committed and the sort they asked for.
type windowQueryMsg struct {
	component string
	raw       string
	terms     []query.Term
	sort      string
	desc      bool
}

// windowTickMsg re-requests the window on screen for a polled source.
type windowTickMsg struct{ component string }

// initWindows registers a coordinator for every table bound to a source
// that can serve windows. Called from build_ after the source registry is
// populated.
//
// A component marked Windowed in config whose built source doesn't
// actually implement ds.WindowedSource is left unregistered: it falls
// back to the ordinary Fetch path, which for an http source is its first
// page. Config validation already rejects the combinations that would
// make that misleading (an operator over a windowed source, a non-table
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
		w := m.windowCfg(c.Cfg.Source)
		m.windows[name] = &windowEntry{
			component: name,
			source:    c.Cfg.Source,
			src:       ws,
			refresh:   entry.src.Refresh(),
			coord: tsource.New(tsource.Options{
				PageSize: w.pageSize,
				Prefetch: w.prefetch,
			}),
		}
	}
}

// windowOpts is the subset of cfg.WindowConfig the coordinator needs.
type windowOpts struct{ pageSize, prefetch int }

// windowCfg reads the window block off the source definition. The
// definition is the authority on page size — the coordinator and the
// source must agree, or the source would answer a different slice than
// the one the coordinator recorded.
func (m *Model) windowCfg(source string) windowOpts {
	out := windowOpts{}
	if def, ok := m.windowDefs[source]; ok && def != nil {
		out.pageSize = def.PageSize
		out.prefetch = def.Prefetch
	}
	return out
}

// startWindows returns the opening request for every windowed table.
// Batched into OnEnter alongside the ordinary first-fetch wave.
func (m *Model) startWindows() []tea.Cmd {
	var cmds []tea.Cmd
	for name, w := range m.windows {
		cmds = append(cmds, tagWindowRequest(w.coord.Init(), name))
		if c := m.tree.Components[name]; c != nil {
			if cmd := setLoading(c, true); cmd != nil {
				cmds = append(cmds, cmd)
			}
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

// handleWindowRequest runs one FetchWindow off the main loop. The
// coordinator never does I/O itself — it says what it wants and this is
// where that becomes a request.
func (m *Model) handleWindowRequest(msg windowRequestMsg) tea.Cmd {
	w, ok := m.windows[msg.component]
	if !ok {
		return nil
	}
	q := translateWindowQuery(msg.query, m.windowFilters(w.source))
	src, gen, offset := w.src, msg.query.Gen, msg.query.Offset
	name := msg.component
	return func() tea.Msg {
		page, err := src.FetchWindow(context.Background(), q)
		return windowFetchedMsg{component: name, gen: gen, offset: offset, page: page, err: err}
	}
}

// handleWindowFetched installs a delivered window, or drops it when it
// answers a query the user has already moved past.
func (m *Model) handleWindowFetched(msg windowFetchedMsg) tea.Cmd {
	w, ok := m.windows[msg.component]
	if !ok {
		return nil
	}
	c := m.tree.Components[msg.component]
	if msg.err != nil {
		// Report the failure but still tell the coordinator the request
		// finished, otherwise it stays pending forever and never asks
		// for this window again. A zero-count page is exactly that
		// signal, and it's also what keeps a broken endpoint from being
		// re-requested on every scroll tick.
		w.coord.Deliver(tsource.Page{Gen: msg.gen, Offset: msg.offset, Count: 0, Total: w.coord.Total()})
		return tea.Batch(setLoading(c, false), m.windowError(w, c, msg.err))
	}
	accepted := w.coord.Deliver(tsource.Page{
		Gen:    msg.gen,
		Offset: msg.offset,
		Count:  len(msg.page.Items),
		Total:  msg.page.Total,
	})
	if !accepted {
		return nil
	}
	if c == nil {
		return nil
	}
	build.ApplyWindow(c, msg.page.Items, msg.offset, msg.page.Total, m.th)
	// postApplyMsg flushes the ViewportChangedMsg that SetWindow just
	// queued — that emit only fires from the table's own Update, and the
	// intercepted fetch path never calls it. Without this the loop stalls
	// after the first page: a window that doesn't fill the screen would
	// never ask for the rest. Same reason handleFetch sends it.
	return tea.Batch(
		setLoading(c, false),
		func() tea.Msg { return postApplyMsg{} },
	)
}

// handleWindowViewport asks the coordinator whether the rows now on
// screen need a fetch. Usually they don't — this fires on every scroll.
func (m *Model) handleWindowViewport(msg windowViewportMsg) tea.Cmd {
	w, ok := m.windows[msg.component]
	if !ok {
		return nil
	}
	return tagWindowRequest(w.coord.Viewport(msg.first, msg.last), msg.component)
}

// handleWindowQuery turns a committed filter or a requested sort into a
// fresh first page.
func (m *Model) handleWindowQuery(msg windowQueryMsg) tea.Cmd {
	w, ok := m.windows[msg.component]
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	if c := m.tree.Components[msg.component]; c != nil && c.Table != nil {
		// Row 400 of the previous result set means nothing in the next
		// one. tuilib leaves this to the screen deliberately — doing it
		// inside the table would walk the cursor through stale rows a
		// frame before the new ones land.
		c.Table.SetCursor(0)
		cmds = append(cmds, setLoading(c, true))
	}
	cmds = append(cmds, tagWindowRequest(w.coord.SetQuery(msg.raw, msg.terms, msg.sort, msg.desc), msg.component))
	return tea.Batch(cmds...)
}

// handleWindowTick re-requests the window on screen and re-arms the
// timer. Polling a windowed source means "same rows, fresh data" — it
// does not walk back to page one, which would yank the user's place.
func (m *Model) handleWindowTick(msg windowTickMsg) tea.Cmd {
	w, ok := m.windows[msg.component]
	if !ok {
		return nil
	}
	cmds := []tea.Cmd{tagWindowRequest(w.coord.Refresh(), msg.component)}
	if w.refresh > 0 {
		cmds = append(cmds, windowTick(msg.component, w.refresh))
	}
	return tea.Batch(cmds...)
}

// refreshWindows re-requests every windowed table's current window. The
// manual 'r' key routes here instead of startFetch, whose SetRows would
// collapse the window into the page it happens to hold.
func (m *Model) refreshWindows() []tea.Cmd {
	var cmds []tea.Cmd
	for name, w := range m.windows {
		cmds = append(cmds, tagWindowRequest(w.coord.Refresh(), name))
		if c := m.tree.Components[name]; c != nil {
			if cmd := setLoading(c, true); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// windowError surfaces a failed window fetch. The first one carries the
// full error — an empty table gives the user no clue why — and later
// ones just the summary, so a flaky endpoint doesn't shout on every
// scroll.
//
// This used to raise an alert modal. The action work replaced that path
// with the output console: ErrorDetail puts the whole message somewhere
// it can be read twice, behind an unread badge, without stealing the
// keyboard from a user who is mid-scroll. Same reasoning as the initial
// -fetch error in handleFetch, so both now read the same way.
func (m *Model) windowError(w *windowEntry, c *build.Component, err error) tea.Cmd {
	if _, count, _ := c.Table.Window(); count == 0 {
		return app.ErrorDetail(w.source+": window fetch failed", err.Error())
	}
	return app.Error(w.source + ": " + err.Error())
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
func translateWindowQuery(q tsource.Query, mapped map[string]string) ds.WindowQuery {
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

// windowedSource reports whether any registered coordinator pulls from
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

// tagWindowRequest rewrites the untagged source.RequestMsg a coordinator
// emits into one naming the component whose coordinator emitted it.
// tea.BatchMsg is unpacked recursively for the same reason
// translateFocusMsg does it — a request can co-flush with a spinner tick.
func tagWindowRequest(cmd tea.Cmd, component string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		switch x := cmd().(type) {
		case tea.BatchMsg:
			wrapped := make([]tea.Cmd, 0, len(x))
			for _, sub := range x {
				wrapped = append(wrapped, tagWindowRequest(sub, component))
			}
			return tea.BatchMsg(wrapped)
		case tsource.RequestMsg:
			return windowRequestMsg{component: component, query: x.Query}
		default:
			return x
		}
	}
}

// translateWindowMsg tags the two table messages a windowed source needs.
// Called from translateFocusMsg, which already wraps every component's
// returned Cmd and knows which component produced it. Returns nil when
// the message isn't one of ours.
func translateWindowMsg(msg tea.Msg, name string) tea.Msg {
	switch x := msg.(type) {
	case table.ViewportChangedMsg:
		return windowViewportMsg{component: name, first: x.FirstVisible, last: x.LastVisible}
	case table.QueryChangedMsg:
		return windowQueryMsg{
			component: name,
			raw:       x.Raw,
			terms:     x.Terms,
			sort:      x.Sort,
			desc:      x.Desc,
		}
	}
	return nil
}
