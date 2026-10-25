package screen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// podsStub serves a pods list whose phases a test can change, or fail.
type podsStub struct {
	*httptest.Server
	mu     sync.Mutex
	phases map[string]string
	fail   bool
}

func newPodsStub() *podsStub {
	s := &podsStub{phases: map[string]string{"u1": "Running", "u2": "Pending"}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{
			map[string]any{"uid": "u1", "name": "web", "status": map[string]any{"phase": s.phases["u1"]}},
			map[string]any{"uid": "u2", "name": "api", "status": map[string]any{"phase": s.phases["u2"]}},
		})
	}))
	return s
}

func (s *podsStub) set(f func(*podsStub)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func newActivityHarness(t *testing.T, srv *podsStub) *windowHarness {
	t.Helper()
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			// A long interval: the test drives polls with r.
			"pods": cfg.NewEntry(&cfg.Source{Type: "http", URL: srv.URL, Refresh: "1h"}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{"pods": {
				Type: "table", Title: "Pods", Source: "pods", MarkKey: cfg.Path{"uid"},
				Columns: []cfg.Column{
					{Title: "Name", Width: 10, Value: cfg.Path{"name"}},
					{Title: "Phase", Width: 14, Value: cfg.Path{"status.phase"}},
				},
				Activity: &cfg.Activity{Status: cfg.Path{"status.phase"}, Busy: []string{"Pending"}},
			}},
			Screen: cfg.Screen{Layout: cfg.Node{Component: "pods"}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Palettes: []palette.Palette{palette.Nord(), palette.Dark()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	return &windowHarness{t: t, model: m, root: root}
}

func TestActivityFollowsTheServer(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	h := newActivityHarness(t, srv)
	h.drain(h.model.Init())

	tbl := h.root.tree.Components["pods"].Table
	if got := tbl.ActivityCount(); got != 1 {
		t.Fatalf("%d rows busy, want 1 (api is Pending)", got)
	}
	if got := h.view(); !strings.Contains(got, "Pending") {
		t.Errorf("the busy row should show its status beside the spinner; view:\n%s", got)
	}

	srv.set(func(s *podsStub) { s.phases["u2"] = "Running" })
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := tbl.ActivityCount(); got != 0 {
		t.Errorf("%d rows busy after api settled, want 0", got)
	}

	srv.set(func(s *podsStub) { s.phases["u1"] = "Pending" })
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := tbl.ActivityCount(); got != 1 {
		t.Errorf("%d rows busy after web went Pending, want 1", got)
	}
}

// A polled pane says "reads failing" on its title through an outage, and
// drops it when reads recover.
func TestReadsFailingMarksThePolledPane(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	h := newActivityHarness(t, srv)
	h.drain(h.model.Init())

	srv.set(func(s *podsStub) { s.fail = true })
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := h.view(); !strings.Contains(got, "reads failing") {
		t.Fatalf("no outage marker on the title; view:\n%s", got)
	}
	if got := h.view(); !strings.Contains(got, "api") {
		t.Errorf("an outage blanked the rows; view:\n%s", got)
	}

	h.root.SetTheme(theme.New(palette.Dark()))
	if got := h.view(); !strings.Contains(got, "reads failing") {
		t.Errorf("a theme swap dropped the outage marker; view:\n%s", got)
	}

	srv.set(func(s *podsStub) { s.fail = false })
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := h.view(); strings.Contains(got, "reads failing") {
		t.Errorf("the marker stayed after reads recovered; view:\n%s", got)
	}
}

// The shipped example always has one pod Pending and one Terminating.
func TestActivityExampleSpinsTheBusyPods(t *testing.T) {
	c, err := cfg.Load("../../examples/activity.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c.SubstituteEnv()
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Palettes: []palette.Palette{palette.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	h := &windowHarness{t: t, model: m, root: root}
	h.drain(h.model.Init())
	if got := root.tree.Components["pods"].Table.ActivityCount(); got != 2 {
		t.Errorf("%d pods busy, want 2 (one Pending, one Terminating)", got)
	}
}
