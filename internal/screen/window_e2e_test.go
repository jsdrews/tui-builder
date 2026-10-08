package screen

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// windowStub is an offset-paged search endpoint that records every
// request. Titles encode their own index so a test can tell which window
// is on screen from the rendered view alone.
type windowStub struct {
	*httptest.Server
	mu    sync.Mutex
	seen  []url.Values
	total int
}

func newWindowStub(total int) *windowStub {
	s := &windowStub{total: total}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s.mu.Lock()
		s.seen = append(s.seen, q)
		s.mu.Unlock()

		// The stub answers the filter the same way the real server
		// would: a scoped author= narrows the set, so the total the
		// table sizes itself against changes with the query.
		total := s.total
		prefix := "Book"
		if q.Get("author") != "" {
			total = 7
			prefix = "By-" + q.Get("author")
		} else if q.Get("q") != "" {
			total = 3
			prefix = "Hit-" + q.Get("q")
		}

		offset := atoiOr(q.Get("offset"), 0)
		limit := atoiOr(q.Get("limit"), 10)
		docs := []any{}
		for i := offset; i < offset+limit && i < total; i++ {
			docs = append(docs, map[string]any{"title": fmt.Sprintf("%s-%04d", prefix, i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"numFound": total, "docs": docs})
	}))
	return s
}

func (s *windowStub) requests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.seen...)
}

func atoiOr(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	return n
}

// windowHarness builds the app shell around one windowed table and
// returns it plus a drain function.
type windowHarness struct {
	t     *testing.T
	model tea.Model
	root  *Model
}

func newWindowHarness(t *testing.T, srv *windowStub, pageSize int) *windowHarness {
	t.Helper()
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"books": cfg.NewEntry(&cfg.Source{
				Type: "http", URL: srv.URL + "/search", Root: "docs",
				Window: &cfg.WindowConfig{
					PageSize:    pageSize,
					OffsetParam: "offset",
					LimitParam:  "limit",
					TotalPath:   "numFound",
					SearchParam: "q",
					Filters:     map[string]string{"Author": "author"},
					SortParam:   "ordering",
					Sorts:       map[string]string{"Title": "title_sort"},
				},
			}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"books": {
					Type: "table", Title: "Books", Source: "books", Filterable: true,
					Columns: []cfg.Column{
						{Title: "Title", Width: 40, Value: cfg.Path{"title"}, Sortable: true},
						{Title: "Author", Width: 20, Value: cfg.Path{"author"}},
					},
				},
			},
			Screen: cfg.Screen{Title: "Books", Layout: cfg.Node{Component: "books"}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root: root, Themes: []theme.Theme{theme.Nord()}, SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return &windowHarness{t: t, model: m, root: root}
}

// drain pumps a cmd and every cmd it cascades into until the queue
// settles. tea.BatchMsg is unpacked inline — the windowed loop batches
// heavily (request + spinner + viewport flush), so without this almost
// nothing would run.
func (h *windowHarness) drain(cmds ...tea.Cmd) {
	h.t.Helper()
	queue := append([]tea.Cmd(nil), cmds...)
	for steps := 0; len(queue) > 0 && steps < 400; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, bm...)
			continue
		}
		// A poll tick would re-arm itself forever; the windowed tests
		// don't set refresh, but guard anyway so a future change can't
		// hang the suite.
		if _, ok := msg.(windowTickMsg); ok {
			continue
		}
		// Spinner ticks re-arm themselves too, and each one really
		// sleeps for its interval — draining them costs whole seconds
		// per keystroke and tells us nothing about the window loop.
		if _, ok := msg.(spinner.TickMsg); ok {
			continue
		}
		var next tea.Cmd
		h.model, next = h.model.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}
	// Render a frame before returning. Pane dimensions are applied
	// during View, and the table reports no viewport until it has them —
	// so without this the next keypress would move the cursor without
	// ever emitting ViewportChangedMsg, and nothing would fetch. A real
	// program renders every frame; the harness has to say so.
	_ = h.model.View()
}

func (h *windowHarness) send(msg tea.Msg) {
	h.t.Helper()
	var cmd tea.Cmd
	h.model, cmd = h.model.Update(msg)
	h.drain(cmd)
}

// typeFilter opens the filter, types text, and commits it. Keystrokes go
// in without draining between them — typing isn't a query, so nothing
// worth pumping happens until enter, and draining per rune makes the
// test pay a spinner tick's real sleep fourteen times over.
func (h *windowHarness) typeFilter(text string) {
	h.t.Helper()
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range text {
		h.model, _ = h.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	h.send(tea.KeyMsg{Type: tea.KeyEnter})
}

func (h *windowHarness) table() *table.Model { return h.root.tree.Components["books"].Table }

func (h *windowHarness) view() string { return h.model.View() }

// TestWindowFirstPageLands is the opening of the loop: OnEnter asks for
// page one and the rows appear without anyone scrolling.
func TestWindowFirstPageLands(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	view := h.view()
	if !strings.Contains(view, "Book-0000") {
		t.Fatalf("first window not rendered\n--- view ---\n%s", view)
	}
	off, count, total := h.table().Window()
	if off != 0 || count == 0 {
		t.Errorf("Window() = (%d, %d, %d), want offset 0 with rows resident", off, count, total)
	}
	if total != 500 {
		t.Errorf("total = %d, want 500 — the table sizes itself against the whole set, not the page", total)
	}
	// The table must hold a window, not the whole set.
	if count >= 500 {
		t.Errorf("table holds %d rows, want one page — the point of windowing is not holding all 500", count)
	}
}

// TestWindowScrollFetchesNextWindow: moving the cursor past what's
// loaded is what asks the source for more.
func TestWindowScrollFetchesNextWindow(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	before := len(srv.requests())
	// G jumps the cursor to the last logical row (499), far outside the
	// resident window.
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})

	reqs := srv.requests()
	if len(reqs) <= before {
		t.Fatalf("jumping to the end made no request (%d before, %d after)", before, len(reqs))
	}
	last := reqs[len(reqs)-1]
	if last.Get("offset") == "0" {
		t.Errorf("last request still offset=0 after jumping to row 499: %v", last)
	}
	off, _, _ := h.table().Window()
	if off == 0 {
		t.Errorf("resident window still starts at 0 after jumping to the end")
	}
	if !strings.Contains(h.view(), "Book-0499") {
		t.Errorf("last row not rendered after jump\n--- view ---\n%s", h.view())
	}
}

// TestWindowScopedFilterBecomesQueryParam is the headline: a scoped term
// reaches the server as its mapped parameter, and the answer replaces
// the window rather than filtering the page.
func TestWindowScopedFilterBecomesQueryParam(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	h.typeFilter("author:tolkien") // filters commit on enter, not per keystroke

	reqs := srv.requests()
	var found url.Values
	for _, q := range reqs {
		if q.Get("author") != "" {
			found = q
		}
	}
	if found == nil {
		t.Fatalf("no request carried ?author=; requests: %v", reqs)
	}
	if got := found.Get("author"); got != "tolkien" {
		t.Errorf("author = %q, want %q", got, "tolkien")
	}
	if got := found.Get("q"); got != "" {
		t.Errorf("q = %q, want empty — the term was scoped to a mapped column, so it must not also go to the bare search", got)
	}
	if !strings.Contains(h.view(), "By-tolkien") {
		t.Errorf("filtered rows not rendered\n--- view ---\n%s", h.view())
	}
	// The server said 7 matches; the table must adopt that, not keep
	// sizing itself against the original 500.
	if _, _, total := h.table().Window(); total != 7 {
		t.Errorf("total = %d after filtering, want 7", total)
	}
}

// TestWindowBareFilterUsesSearchParam: an unscoped term goes to the
// source's search parameter.
func TestWindowBareFilterUsesSearchParam(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	h.typeFilter("hobbit") // filters commit on enter, not per keystroke

	var found url.Values
	for _, q := range srv.requests() {
		if q.Get("q") != "" {
			found = q
		}
	}
	if found == nil {
		t.Fatalf("no request carried ?q=; requests: %v", srv.requests())
	}
	if got := found.Get("q"); got != "hobbit" {
		t.Errorf("q = %q, want %q", got, "hobbit")
	}
	if !strings.Contains(h.view(), "Hit-hobbit") {
		t.Errorf("search results not rendered\n--- view ---\n%s", h.view())
	}
}

// TestWindowFilterDoesNotFilterLocally guards the whole premise. Under a
// window the table must display exactly what came back — if it also
// applied the filter to those rows, a server-side match whose visible
// cells don't contain the search text would vanish.
func TestWindowFilterDoesNotFilterLocally(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "author:tolkien" {
		h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	h.send(tea.KeyMsg{Type: tea.KeyEnter})

	// Rows come back titled "By-tolkien-000N" and carry no Author cell
	// at all. A locally-filtered table would match "author:tolkien"
	// against its empty Author column and show nothing.
	view := h.view()
	if !strings.Contains(view, "By-tolkien-0000") {
		t.Errorf("server-matched rows were filtered away locally\n--- view ---\n%s", view)
	}
}

// TestWindowSortGoesToTheServer: requesting a sort sends the mapped
// field rather than reordering the page in place.
func TestWindowSortGoesToTheServer(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	before := len(srv.requests())
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	reqs := srv.requests()
	if len(reqs) <= before {
		t.Fatalf("sorting made no request")
	}
	var found url.Values
	for _, q := range reqs {
		if q.Get("ordering") != "" {
			found = q
		}
	}
	if found == nil {
		t.Fatalf("no request carried ?ordering=; requests: %v", reqs)
	}
	if got := found.Get("ordering"); got != "title_sort" {
		t.Errorf("ordering = %q, want %q (mapped through window.sorts)", got, "title_sort")
	}
}

// TestWindowSourceSkipsWholeSetFetch: the ordinary fetch path must leave
// a windowed source alone. Routing it through SetRows would collapse the
// window and make the table claim page one is the whole set.
func TestWindowSourceSkipsWholeSetFetch(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	// Every request so far must carry the window's parameters. A
	// whole-set Fetch would have gone out with no offset/limit.
	for i, q := range srv.requests() {
		if q.Get("limit") == "" {
			t.Errorf("request %d has no limit — something fetched the source outside the window path: %v", i, q)
		}
	}
	if _, _, total := h.table().Window(); total != 500 {
		t.Errorf("total = %d, want 500 — the table should still be windowed, not holding a flat page", total)
	}
}

// TestWindowManualRefreshKeepsPlace: 'r' refetches the window on screen
// rather than walking back to page one.
func TestWindowManualRefreshKeepsPlace(t *testing.T) {
	srv := newWindowStub(500)
	defer srv.Close()
	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})

	offBefore, _, _ := h.table().Window()
	if offBefore == 0 {
		t.Fatal("setup: expected to be scrolled away from the first page")
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	offAfter, _, _ := h.table().Window()
	if offAfter != offBefore {
		t.Errorf("refresh moved the window from offset %d to %d — it should refetch in place", offBefore, offAfter)
	}
	last := srv.requests()[len(srv.requests())-1]
	if last.Get("offset") == "0" {
		t.Errorf("refresh requested offset=0 instead of the window on screen: %v", last)
	}
}

// TestWindowFetchErrorSurfaces: a failing endpoint must say so rather
// than leaving an empty table with no explanation.
func TestWindowFetchErrorSurfaces(t *testing.T) {
	srv := &windowStub{total: 0}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := newWindowHarness(t, srv, 20)
	h.drain(h.model.Init())

	// This used to assert an alert modal. The action work replaced that
	// path with the output console, so the first failure now goes out as
	// app.ErrorDetail: the summary lands in the statusbar and the body
	// stays readable behind the console's unread badge. Assert on what
	// the user sees rather than on which modal field is non-nil.
	// Match a substring that survives the statusbar's truncation: at
	// this width the slot renders "books: window fetch fail".
	if got := h.view(); !strings.Contains(got, "window fetch") {
		t.Error("nothing surfaced after the first window fetch failed — an empty table gives the user no clue why")
	}
}

// newExecWindowHarness builds the same single-table screen over a
// windowed *exec* source. The command is a shell script that pages and
// filters a synthetic set, so the test exercises the argv-rendering path
// end to end without any network.
func newExecWindowHarness(t *testing.T, pageSize int) *windowHarness {
	t.Helper()
	// Prints {"total":N,"rows":[…]} for rows [offset, offset+limit) of a
	// 300-row set, narrowing to 7 rows when an author filter arrives and
	// 3 when a bare search does — same shape as the http stub, so the
	// assertions can mirror it.
	script := `
TOTAL=300; PREFIX=Row
if [ -n "$AUTHOR" ]; then TOTAL=7; PREFIX="By-$AUTHOR"; fi
if [ -z "$AUTHOR" ] && [ -n "$SEARCH" ]; then TOTAL=3; PREFIX="Hit-$SEARCH"; fi
i=$OFFSET; end=$((OFFSET + LIMIT)); sep=""; ROWS=""
while [ $i -lt $end ] && [ $i -lt $TOTAL ]; do
  ROWS="$ROWS$sep{\"title\":\"$PREFIX-$(printf %04d $i)\"}"
  sep=","; i=$((i + 1))
done
printf '{"total":%s,"rows":[%s]}' "$TOTAL" "$ROWS"
`
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"books": cfg.NewEntry(&cfg.Source{
				Type: "exec",
				Command: []string{"sh", "-c",
					`OFFSET=${window.offset}; LIMIT=${window.limit};` +
						` AUTHOR="${window.filters.author}"; SEARCH="${window.search}";` + script},
				Root: "rows",
				Window: &cfg.WindowConfig{
					PageSize:  pageSize,
					TotalPath: "total",
					Filters:   map[string]string{"Author": "author"},
				},
			}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"books": {
					Type: "table", Title: "Books", Source: "books", Filterable: true,
					Columns: []cfg.Column{
						{Title: "Title", Width: 40, Value: cfg.Path{"title"}},
						{Title: "Author", Width: 20, Value: cfg.Path{"author"}},
					},
				},
			},
			Screen: cfg.Screen{Title: "Books", Layout: cfg.Node{Component: "books"}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root: root, Themes: []theme.Theme{theme.Nord()}, SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return &windowHarness{t: t, model: m, root: root}
}

// TestExecWindowDrivesTheSameLoop: the screen only knows
// ds.WindowedSource, so an exec source must walk the identical path —
// first page on enter, a new window on scroll, a fresh query on filter.
func TestExecWindowDrivesTheSameLoop(t *testing.T) {
	h := newExecWindowHarness(t, 20)
	h.drain(h.model.Init())

	if !strings.Contains(h.view(), "Row-0000") {
		t.Fatalf("first window not rendered\n--- view ---\n%s", h.view())
	}
	off, count, total := h.table().Window()
	if off != 0 || count != 20 || total != 300 {
		t.Errorf("Window() = (%d, %d, %d), want (0, 20, 300)", off, count, total)
	}

	// Scroll to the end — a different window has to be requested.
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	off, _, _ = h.table().Window()
	if off == 0 {
		t.Errorf("window still starts at 0 after jumping to the last row")
	}
	if !strings.Contains(h.view(), "Row-0299") {
		t.Errorf("last row not rendered after jump\n--- view ---\n%s", h.view())
	}
}

// TestExecWindowScopedFilterReachesTheCommand: a scoped term arrives as
// its mapped ${window.filters.*} token, and the command's narrowed total
// replaces the table's.
func TestExecWindowScopedFilterReachesTheCommand(t *testing.T) {
	h := newExecWindowHarness(t, 20)
	h.drain(h.model.Init())

	h.typeFilter("author:tolkien")

	if !strings.Contains(h.view(), "By-tolkien") {
		t.Errorf("scoped filter never reached the command\n--- view ---\n%s", h.view())
	}
	if _, _, total := h.table().Window(); total != 7 {
		t.Errorf("total = %d after filtering, want 7 — the command's count must replace the old one", total)
	}
}

// A bare term lands on ${window.search} rather than any filter token.
func TestExecWindowBareFilterReachesSearchToken(t *testing.T) {
	h := newExecWindowHarness(t, 20)
	h.drain(h.model.Init())

	h.typeFilter("hobbit")

	if !strings.Contains(h.view(), "Hit-hobbit") {
		t.Errorf("bare term never reached ${window.search}\n--- view ---\n%s", h.view())
	}
	if _, _, total := h.table().Window(); total != 3 {
		t.Errorf("total = %d after search, want 3", total)
	}
}
