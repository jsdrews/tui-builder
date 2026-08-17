package screen

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// TestClickFocusesClickedPane covers the half of mouse support that isn't
// tuilib's: a click reaches the component it landed on all by itself, but
// this screen keeps its own focus index (it drives on_cursor tagging,
// action dispatch, and help text), so it has to translate the clicked
// component's focus.RequestMsg into a move of that index.
//
// Without the translation the two disagree — the clicked pane scrolls
// under the mouse while the keyboard still drives the pane that had focus
// before, and both render as active.
func TestClickFocusesClickedPane(t *testing.T) {
	c := twoPaneConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
		Mouse:      app.MouseClick,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

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

	// The left list starts focused, so its cursor is the one keys move.
	if got := listCursor(m.View()); got != "alpha" {
		t.Fatalf("expected the list to start on alpha, got %q", got)
	}

	// Click a row in the right-hand table (x=70 is well inside it at
	// width 120 with a 1:2 split).
	var cmd tea.Cmd
	m, cmd = m.Update(tea.MouseMsg{
		X: 70, Y: 8,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	drain(cmd)

	// Now the keyboard must drive the table, not the list.
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	drain(cmd)

	if got := listCursor(m.View()); got != "alpha" {
		t.Errorf("list cursor moved to %q after clicking the table — focus did not follow the click", got)
	}
}

// TestDoubleClickActivatesLikeEnter covers the mouse spelling of enter: a
// double click must fire the same on_key binding the enter key does. The
// component reports the double click as an ActivatedMsg naming itself; the
// screen has to translate that into its enter verb, or the mouse can move
// a cursor but never open anything.
func TestDoubleClickActivatesLikeEnter(t *testing.T) {
	c := pushConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	multi := &Multi{Screens: c.TUI.Screens, Components: c.TUI.Components, Sources: c.Data.Sources}
	root, err := NewMulti(c.TUI.Initial, multi, build.Selection{}, nil, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
		Mouse:      app.MouseClick,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	drain := drainer(&m)
	drain(m.Init())

	// Components stamp their rect during render and reject clicks from a
	// stale frame, so hit-testing needs one frame on the board first.
	view := xansi.Strip(m.View())
	if !strings.Contains(view, "alpha") {
		t.Fatalf("expected the list to render; got:\n%s", view)
	}
	row := rowOf(view, "beta")
	if row < 0 {
		t.Fatalf("could not locate the beta row:\n%s", view)
	}

	press := func() {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.MouseMsg{
			X: 4, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		})
		drain(cmd)
	}
	press()
	press() // second press in the same cell within the interval = double click

	after := xansi.Strip(m.View())
	if !strings.Contains(after, "Detail: beta") {
		t.Errorf("double click did not open the detail screen for the clicked row.\n%s", after)
	}
}

// TestEnterFiresActionBoundToEnter guards the other half of the enter
// verb: `enter` used to consult only on_key pushes, so an action declared
// with `key: enter` (examples/action_prompts.yaml ships one) silently
// never fired.
func TestEnterFiresActionBoundToEnter(t *testing.T) {
	c := twoPaneConfig()
	c.TUI.Screen.Actions = []cfg.ActionBinding{{
		Key:     "enter",
		Action:  "order",
		Label:   "order",
		From:    "names",
		Confirm: "Order ${selection}?",
	}}
	c.Actions = map[string]*cfg.Action{
		"order": {Run: []string{"echo", "hello"}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	drain := drainer(&m)
	drain(m.Init())

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)

	if got := xansi.Strip(m.View()); !strings.Contains(got, "Order alpha?") {
		t.Errorf("enter did not fire the action bound to it; view:\n%s", got)
	}
}

// drainer returns a cmd-queue pump over the model at mp, unpacking
// tea.BatchMsg inline (the pattern the other e2e tests here use).
func drainer(mp *tea.Model) func(tea.Cmd) {
	return func(start tea.Cmd) {
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
			*mp, next = (*mp).Update(msg)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
}

// rowOf returns the screen line holding want, which is the y a click must
// target to land on it.
func rowOf(view, want string) int {
	for i, l := range strings.Split(view, "\n") {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

// pushConfig is a list whose enter binding pushes a detail screen titled
// after the selected item, so a successful activation is visible in the
// rendered output.
func pushConfig() cfg.Config {
	return cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"items": cfg.NewEntry(&cfg.Source{Type: "static", Data: []any{
				map[string]any{"name": "alpha"},
				map[string]any{"name": "beta"},
				map[string]any{"name": "gamma"},
			}}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"names":  {Type: "list", Title: "Names", Source: "items", Item: "name"},
				"detail": {Type: "textview", Title: "Detail: ${selection}", Content: "picked ${selection}"},
			},
			Screens: map[string]*cfg.Screen{
				"list": {
					Title:  "List",
					Layout: cfg.Node{Component: "names"},
					OnKey: []cfg.OnKeyBinding{
						{Source: "names", Push: "detail", Key: "enter"},
					},
				},
				"detail": {Title: "Detail", Layout: cfg.Node{Component: "detail"}},
			},
			Initial: "list",
		},
	}
}

// listCursor returns the item the list's ▸ cursor marks. The cursor row
// spans both panes, so it stops at the left pane's closing border rather
// than running into the table's columns.
func listCursor(view string) string {
	for _, l := range strings.Split(xansi.Strip(view), "\n") {
		i := strings.Index(l, "▸")
		if i < 0 {
			continue
		}
		cell := l[i+len("▸"):]
		if end := strings.Index(cell, "│"); end >= 0 {
			cell = cell[:end]
		}
		return strings.TrimSpace(cell)
	}
	return "(no cursor)"
}

// twoPaneConfig is a list beside a table, both on static data so the test
// needs no network and no fixture file.
func twoPaneConfig() cfg.Config {
	rows := []any{
		map[string]any{"name": "alpha", "region": "north"},
		map[string]any{"name": "beta", "region": "south"},
		map[string]any{"name": "gamma", "region": "east"},
		map[string]any{"name": "delta", "region": "west"},
	}
	return cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"items": cfg.NewEntry(&cfg.Source{Type: "static", Data: rows}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"names": {Type: "list", Title: "Names", Source: "items", Item: "name"},
				"detail": {Type: "table", Title: "Detail", Source: "items", Columns: []cfg.Column{
					{Title: "Name", Width: 20, Value: cfg.Path{"name"}},
					{Title: "Region", Width: 20, Value: cfg.Path{"region"}},
				}},
			},
			Screen: cfg.Screen{
				Title: "Two panes",
				Layout: cfg.Node{HStack: []cfg.Item{
					{Flex: 1, Node: cfg.Node{Component: "names"}},
					{Flex: 2, Node: cfg.Node{Component: "detail"}},
				}},
			},
		},
	}
}
