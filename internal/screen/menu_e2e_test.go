package screen

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

// A menu on the home screen: enter on an item pushes its screen, and the
// item's bind: fills the destination source's parameter.
func TestMenuOpensItsScreen(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"instance of %s"}]`, strings.TrimPrefix(r.URL.Path, "/engines/"))
	}))
	defer srv.Close()

	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"instance": cfg.NewEntry(&cfg.Source{
				Type: "http", URL: srv.URL + "/engines/${params.engine}",
				Parameters: map[string]*cfg.Parameter{"engine": {Type: "string", Required: true}},
			}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"home": {Type: "menu", Title: "Sections", MenuItems: []cfg.MenuItem{
					{Group: "database", Label: "postgres", Screen: "instance", Bind: map[string]string{"engine": "postgres"}},
					{Group: "database", Label: "mysql", Screen: "instance", Bind: map[string]string{"engine": "mysql"}},
					{Group: "other", Label: "nothing", Screen: "instance", Bind: map[string]string{"engine": "none"}},
				}},
				"detail": {Type: "list", Title: "Instance", Source: "instance", Item: "name"},
			},
			Screens: map[string]*cfg.Screen{
				"home":     {Title: "Home", Layout: cfg.Node{Component: "home"}},
				"instance": {Title: "Instance", Layout: cfg.Node{Component: "detail"}},
			},
			Initial: "home",
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	multi := &Multi{Screens: c.TUI.Screens, Components: c.TUI.Components, Sources: c.Data.Sources}
	root, err := NewMulti(c.TUI.Initial, multi, build.Selection{}, nil, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Palettes: []palette.Palette{palette.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	drain := func(start tea.Cmd) {
		queue := []tea.Cmd{start}
		for steps := 0; len(queue) > 0 && steps < 200; steps++ {
			cmd := queue[0]
			queue = queue[1:]
			if cmd == nil {
				continue
			}
			msg := cmd()
			if msg == nil {
				continue
			}
			if bm, ok := msg.(tea.BatchMsg); ok {
				queue = append(queue, bm...)
				continue
			}
			var next tea.Cmd
			m, next = m.Update(msg)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	drain(m.Init())

	view := m.View()
	for _, want := range []string{"Sections", "database", "postgres", "mysql"} {
		if !strings.Contains(view, want) {
			t.Fatalf("home menu missing %q:\n%s", want, view)
		}
	}

	// Down to mysql, then open it.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	drain(cmd)
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)

	view = m.View()
	if !strings.Contains(view, "instance of mysql") {
		t.Errorf("enter on mysql didn't open its screen with engine=mysql:\n%s", view)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/engines/mysql" {
		t.Errorf("requests = %v, want one to /engines/mysql", paths)
	}
}
