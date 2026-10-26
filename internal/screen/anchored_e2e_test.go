package screen

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// logStub is a log API walked by timestamp, Prefect-style: POST a body
// with an order and an optional `after` / `before` timestamp. 200 entries
// with id log-000..log-199, two per timestamp, so a timestamp alone can't
// tell neighbours apart.
type logStub struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

func newLogStub() *logStub {
	s := &logStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()
		q, _ := body["q"].(string)
		desc := body["order"] == "desc"
		limit := int(body["limit"].(float64))
		before, hasBefore := body["before"].(float64)
		after, hasAfter := body["after"].(float64)
		var out []map[string]any
		for i := 0; i < 200; i++ {
			ts := float64(i / 2)
			if (hasBefore && ts >= before) || (hasAfter && ts <= after) {
				continue
			}
			text := fmt.Sprintf("entry %03d", i)
			if q != "" && !strings.Contains(text, q) {
				continue
			}
			out = append(out, map[string]any{"id": fmt.Sprintf("log-%03d", i), "ts": ts, "text": text})
		}
		sort.SliceStable(out, func(a, b int) bool {
			if desc {
				return out[a]["id"].(string) > out[b]["id"].(string)
			}
			return out[a]["id"].(string) < out[b]["id"].(string)
		})
		if len(out) > limit {
			out = out[:limit]
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	return s
}

func (s *logStub) requests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func newAnchoredHarness(t *testing.T, srv *logStub, anchor ...string) *windowHarness {
	t.Helper()
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"logs": cfg.NewEntry(&cfg.Source{
				Type: "http", Method: "POST", URL: srv.URL + "/logs",
				Body: `{"limit": ${window.limit}, "q": "${window.search}"}`,
				Window: &cfg.WindowConfig{
					PageSize: 20, Cursor: "ts",
					Older:   map[string]any{"order": "desc", "before": "${window.cursor}"},
					Newer:   map[string]any{"order": "asc", "after": "${window.cursor}"},
					Filters: map[string]string{"Text": "text"},
					Anchor:  strings.Join(anchor, ""),
				},
			}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"logs": {Type: "eventlog", Title: "Logs", Source: "logs", Key: cfg.Path{"id"}, Text: cfg.Path{"text"}},
			},
			Screen: cfg.Screen{Title: "Logs", Layout: cfg.Node{Component: "logs"}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Palettes: []palette.Palette{palette.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return &windowHarness{t: t, model: m, root: root}
}

func TestAnchoredEventlogOpensAtTheNewest(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newAnchoredHarness(t, srv)
	h.drain(h.model.Init())

	if got := h.view(); !strings.Contains(got, "entry 199") {
		t.Fatalf("the newest entry isn't on screen; view:\n%s", got)
	}
	first := srv.requests()[0]
	if _, ok := first["before"]; ok {
		t.Error("the first request walks from the newest item, so it has no cursor to send")
	}
	if first["order"] != "desc" || first["limit"] != float64(21) {
		t.Errorf("first request = %v, want order desc and limit 21", first)
	}
	if !h.root.tree.Components["logs"].Eventlog.Following() {
		t.Error("an Anchored eventlog should follow its newest item")
	}
}

func TestAnchoredEventlogWalksOlderFromItsEdge(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newAnchoredHarness(t, srv)
	h.drain(h.model.Init())

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	var walked bool
	for _, b := range srv.requests()[1:] {
		if b["order"] == "desc" && b["before"] != nil {
			walked = true
		}
	}
	if !walked {
		t.Fatalf("going to the top never asked for older entries; requests: %v", srv.requests())
	}
	_, items := h.root.tree.Components["logs"].Eventlog.Items()
	if len(items) <= 20 {
		t.Errorf("holds %d items after walking older, want more than the first page", len(items))
	}
}

// Two entries share every timestamp. tuilib keys items by cursor, so the
// screen keys them by cursor and id together; both of each pair survive.
func TestAnchoredEventlogKeepsItemsThatShareACursor(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newAnchoredHarness(t, srv)
	h.drain(h.model.Init())

	view := h.view()
	for _, want := range []string{"entry 198", "entry 199"} {
		if !strings.Contains(view, want) {
			t.Errorf("%s is missing — two entries with the same timestamp collapsed into one; view:\n%s", want, view)
		}
	}
}

// What enter hands on is the item's own key, not the cursor-and-key the
// eventlog holds it by.
func TestAnchoredEventlogSelectionIsTheItemKey(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newAnchoredHarness(t, srv)
	h.drain(h.model.Init())

	sel := selectionFrom(h.root.tree.Components["logs"])
	if sel.String != "log-199" {
		t.Errorf("${selection} = %q, want log-199", sel.String)
	}
}

// The shipped example walks its synthetic log through an exec command.
func TestAnchoredExampleWalksOlder(t *testing.T) {
	c, err := cfg.Load("../../examples/anchored.yaml")
	if err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Palettes: []palette.Palette{palette.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	h := &windowHarness{t: t, model: m, root: root}
	h.drain(h.model.Init())

	if got := h.view(); !strings.Contains(got, "request 999") {
		t.Fatalf("the newest line isn't on screen; view:\n%s", got)
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	_, items := root.tree.Components["log"].Eventlog.Items()
	if len(items) <= 50 {
		t.Errorf("holds %d lines after going to the top, want older pages walked in", len(items))
	}
}

// `window.anchor: oldest` starts at the other end: the first request
// walks newer with no cursor, and the view sits at the top.
func TestAnchoredEventlogCanStartAtTheOldest(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newAnchoredHarness(t, srv, "oldest")
	h.drain(h.model.Init())

	first := srv.requests()[0]
	if first["order"] != "asc" {
		t.Errorf("first request = %v, want an ascending walk from the oldest entry", first)
	}
	if _, ok := first["after"]; ok {
		t.Error("the first request has no cursor to send")
	}
	if got := h.view(); !strings.Contains(got, "entry 000") {
		t.Errorf("the oldest entry isn't on screen; view:\n%s", got)
	}
	if h.root.tree.Components["logs"].Eventlog.Following() {
		t.Error("anchored at the oldest, the view shouldn't follow the newest")
	}
}

func newSpanHarness(t *testing.T, srv *logStub) *windowHarness {
	t.Helper()
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"logs": cfg.NewEntry(&cfg.Source{
				Type: "http", Method: "POST", URL: srv.URL + "/logs",
				Body: `{"limit": ${window.limit}, "q": "${window.search}"}`,
				Window: &cfg.WindowConfig{
					PageSize: 20, Cursor: "ts",
					Older: map[string]any{"order": "desc", "before": "${window.cursor}"},
					Newer: map[string]any{"order": "asc", "after": "${window.cursor}"},
				},
			}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"logs": {Type: "table", Title: "Logs", Source: "logs", MarkKey: cfg.Path{"id"},
					Markable: true, Filterable: true,
					Columns: []cfg.Column{
						{Title: "Id", Width: 10, Value: cfg.Path{"id"}},
						{Title: "Text", Width: 20, Value: cfg.Path{"text"}},
					}},
			},
			Screen: cfg.Screen{Title: "Logs", Layout: cfg.Node{Component: "logs"}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Palettes: []palette.Palette{palette.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return &windowHarness{t: t, model: m, root: root}
}

func TestSpanTableOpensAtTheNewestAndWalksOlder(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newSpanHarness(t, srv)
	h.drain(h.model.Init())

	if got := h.view(); !strings.Contains(got, "entry 199") {
		t.Fatalf("the newest row isn't on screen; view:\n%s", got)
	}
	if srv.requests()[0]["order"] != "desc" {
		t.Errorf("first request = %v, want a walk older from the newest row", srv.requests()[0])
	}
	tbl := h.root.tree.Components["logs"].Table
	held := len(tbl.Rows())

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if got := len(tbl.Rows()); got <= held {
		t.Errorf("holds %d rows after going to the top, want more than the %d it opened with", got, held)
	}
}

// Rows are keyed by cursor and mark_key, so a mark stays on its row as
// more rows arrive around it, and resolves to that row's cells.
func TestSpanTableMarksHoldAcrossWalks(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newSpanHarness(t, srv)
	h.drain(h.model.Init())

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	before := build.MarkedSelections(h.root.tree.Components["logs"])
	if len(before) != 1 {
		t.Fatalf("marked %d rows, want 1", len(before))
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	after := build.MarkedSelections(h.root.tree.Components["logs"])
	if len(after) != 1 || after[0].String != before[0].String {
		t.Errorf("mark after walking older = %+v, want it still on %q", after, before[0].String)
	}
	if got := build.Substitute("${selection.Text}", after[0]); !strings.HasPrefix(got, "entry ") {
		t.Errorf("${selection.Text} = %q, want the row's text", got)
	}
}

// Committing a filter re-anchors: the source walks again from the newest
// row matching it.
func TestSpanTableFilterReanchors(t *testing.T) {
	srv := newLogStub()
	defer srv.Close()
	h := newSpanHarness(t, srv)
	h.drain(h.model.Init())

	h.typeFilter("entry 01")
	last := srv.requests()[len(srv.requests())-1]
	if last["q"] != "entry 01" {
		t.Fatalf("the filter didn't reach the source; last request %v", last)
	}
	if got := h.view(); !strings.Contains(got, "entry 019") || strings.Contains(got, "entry 199") {
		t.Errorf("the view didn't re-anchor on the filtered rows; view:\n%s", got)
	}
}

// The shipped span-table example opens at its newest row.
func TestSpanTableExampleRenders(t *testing.T) {
	c, err := cfg.Load("../../examples/span_table.yaml")
	if err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Palettes: []palette.Palette{palette.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	h := &windowHarness{t: t, model: m, root: root}
	h.drain(h.model.Init())
	if got := h.view(); !strings.Contains(got, "request 999") {
		t.Errorf("the newest row isn't on screen; view:\n%s", got)
	}
}
