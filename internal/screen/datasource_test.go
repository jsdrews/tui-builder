package screen

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// TestDataSourceFetchApply spins up a stub HTTP server, builds a
// data-source-bound table screen pointed at it, and drives the fetch +
// apply cycle. Verifies the rendered table contains the stubbed cells.
func TestDataSourceFetchApply(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"id": 1, "name": {"common": "London"},    "region": "Europe", "population": 9000000},
			{"id": 2, "name": {"common": "Tokyo"},     "region": "Asia",   "population": 37000000},
			{"id": 3, "name": {"common": "Reykjavík"}, "region": "Europe", "population": 130000}
		]`)
	}))
	defer ts.Close()

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"places": cfg.NewEntry(&cfg.Source{Type: "http", URL: ts.URL})}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"places_table": {
			Type:   "table",
			Title:  "Places",
			Source: "places",
			Columns: []cfg.Column{
				{Title: "Name", Width: 14, Value: cfg.Path{"name.common"}},
				{Title: "Region", Width: 12, Value: cfg.Path{"region"}},
				{Title: "Population", Width: 12, Value: cfg.Path{"population"}, Sort: "number", Align: "right", Sortable: true},
			},
		},
	},
		Screen: cfg.Screen{
			Title:  "Places",
			Layout: cfg.Node{Component: "places_table"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Drive Init + every follow-up cmd until the queue settles. tea.Batch
	// emits a BatchMsg containing a slice of cmds; we have to expand it
	// inline instead of treating it as a regular update msg.
	queue := []tea.Cmd{m.Init()}
	deadline := 200
	for len(queue) > 0 && deadline > 0 {
		deadline--
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
			for _, sub := range bm {
				queue = append(queue, sub)
			}
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}

	view := m.View()
	for _, want := range []string{"London", "Tokyo", "Reykjavík", "Europe", "Asia"} {
		if !contains(view, want) {
			t.Errorf("rendered view missing %q\n--- view ---\n%s", want, view)
			return
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
