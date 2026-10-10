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
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// newEventlogHarness builds the app shell around one eventlog over the
// windowed stub, whose items are {"title": "Book-0000"} and so on.
func newEventlogHarness(t *testing.T, srv *windowStub, mutate func(*cfg.Config)) *windowHarness {
	t.Helper()
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"events": cfg.NewEntry(&cfg.Source{
				Type: "http", URL: srv.URL + "/search", Root: "docs",
				Window: &cfg.WindowConfig{
					PageSize: 20, OffsetParam: "offset", LimitParam: "limit",
					TotalPath: "numFound", SearchParam: "q",
				},
			}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"events": {Type: "eventlog", Title: "Events", Source: "events",
					Key: cfg.Path{"title"}, Text: cfg.Path{"title"}, Searchable: true},
			},
			Screen: cfg.Screen{Title: "Events", Layout: cfg.Node{Component: "events"}},
		},
	}
	if mutate != nil {
		mutate(&c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Themes: []theme.Theme{theme.Nord(), theme.Dark()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return &windowHarness{t: t, model: m, root: root}
}

func TestEventlogFirstPageLands(t *testing.T) {
	srv := newWindowStub(300)
	defer srv.Close()
	h := newEventlogHarness(t, srv, nil)
	h.drain(h.model.Init())

	if got := h.view(); !strings.Contains(got, "Book-0000") {
		t.Fatalf("first page didn't land in the eventlog; view:\n%s", got)
	}
	if reqs := srv.requests(); len(reqs) == 0 || reqs[0].Get("offset") != "0" {
		t.Errorf("first request = %v, want offset 0", reqs)
	}
}

// With no action bound to enter, an item opens in an inspector: the
// eventlog draws only an item's text, so this is how the rest is reached.
func TestEventlogEnterOpensTheItem(t *testing.T) {
	srv := newWindowStub(300)
	defer srv.Close()
	h := newEventlogHarness(t, srv, nil)
	h.drain(h.model.Init())

	h.send(tea.KeyMsg{Type: tea.KeyEnter})
	if h.root.inspectModal == nil {
		t.Fatal("enter on an item opened nothing")
	}
	if got := h.view(); !strings.Contains(got, "title") {
		t.Errorf("the inspector doesn't show the item's fields; view:\n%s", got)
	}
	h.send(tea.KeyMsg{Type: tea.KeyEsc})
	if h.root.inspectModal != nil {
		t.Error("esc didn't close the inspector")
	}
}

// A `key: enter` action gets the item as ${selection.*}.
func TestEventlogEnterRunsTheBoundAction(t *testing.T) {
	srv := newWindowStub(300)
	defer srv.Close()
	h := newEventlogHarness(t, srv, func(c *cfg.Config) {
		c.Actions = map[string]*cfg.Action{"open": {
			Run: []string{"echo", "${inputs.title}"}, Inputs: map[string]*cfg.Parameter{"title": {Required: true}},
		}}
		c.TUI.Screen.Actions = []cfg.ActionBinding{{
			Key: "enter", Action: "open", From: "events",
			Bind: map[string]string{"title": "${selection.title}"}, Confirm: "Open ${selection.title}?",
		}}
	})
	h.drain(h.model.Init())

	h.send(tea.KeyMsg{Type: tea.KeyEnter})
	if h.root.inspectModal != nil {
		t.Error("a bound action should run instead of opening the inspector")
	}
	if got := h.view(); !strings.Contains(got, "Open Book-0000?") {
		t.Errorf("the bound action didn't get the item as ${selection}; view:\n%s", got)
	}
}

// `growing: {source, while}` follows a job's status: growing while the
// job runs, stopped when it ends. The job source isn't displayed, so the
// screen has to fetch it on its own.
//
// Growth itself is driven by calling updateGrowth with the job's value:
// once an eventlog is growing it polls on a real timer, which a drained
// test would sit through tick by tick.
func TestEventlogFollowsAGrowingJob(t *testing.T) {
	srv := newWindowStub(300)
	defer srv.Close()
	var jobHits int
	var mu sync.Mutex
	job := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		jobHits++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
	}))
	defer job.Close()

	h := newEventlogHarness(t, srv, func(c *cfg.Config) {
		c.Data.Sources["job"] = cfg.NewEntry(&cfg.Source{Type: "http", URL: job.URL})
		c.Data.Sources["events"].Window.Growing = &cfg.Growing{Source: "job", While: "status == 'running'"}
	})
	h.drain(h.model.Init())

	mu.Lock()
	hits := jobHits
	mu.Unlock()
	if hits == 0 {
		t.Fatal("the job source was never fetched — nothing displays it, so the screen has to")
	}
	w := h.root.windows["events"]
	log := h.root.tree.Components["events"].Eventlog
	if w.grows || log.Following() {
		t.Fatal("growing before the job is running")
	}

	h.root.updateGrowth("job", map[string]any{"status": "running"})
	if !w.grows {
		t.Error("not growing once the job runs")
	}
	if !log.Following() {
		t.Error("a growing eventlog should follow the newest item")
	}

	h.root.updateGrowth("job", map[string]any{"status": "successful"})
	if w.grows {
		t.Error("still growing after the job finished")
	}
}

func TestEventlogKeepsItsItemsAcrossAThemeSwap(t *testing.T) {
	srv := newWindowStub(300)
	defer srv.Close()
	h := newEventlogHarness(t, srv, nil)
	h.drain(h.model.Init())

	h.root.SetTheme(theme.Dark())
	if got := h.view(); !strings.Contains(got, "Book-0000") {
		t.Errorf("items lost across a theme swap; view:\n%s", got)
	}
}

// The shipped example pages its synthetic job log through an exec window.
func TestEventlogExampleRenders(t *testing.T) {
	c, err := cfg.Load("../../examples/eventlog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Themes: []theme.Theme{theme.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	h := &windowHarness{t: t, model: m, root: root}
	h.drain(h.model.Init())

	view := h.view()
	for _, want := range []string{"TASK [step 0]", "skipping: [web3]", "no output"} {
		if !strings.Contains(view, want) {
			t.Errorf("example view is missing %q; view:\n%s", want, view)
		}
	}
}
