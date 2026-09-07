package screen

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// rightClickApp builds the two-pane fixture with a verb scoped to the
// RIGHT pane, plus the action menu bound, and drains to a steady state.
func rightClickApp(t *testing.T) (tea.Model, func(tea.Cmd)) {
	t.Helper()
	c := twoPaneConfig()
	c.TUI.Screen.Actions = []cfg.ActionBinding{{
		Action: "inspect", Label: "inspect", From: "detail",
		Bind: map[string]string{"name": "${selection.Name}"},
	}}
	c.Actions = map[string]*cfg.Action{
		"inspect": {Run: []string{"echo", "${inputs.name}"},
			Inputs: map[string]*cfg.Parameter{"name": {}}},
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
		Mouse:      app.MouseClick,
		ActionsKey: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions")),
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
	return m, drain
}

// Right-click is "what can I do to THIS?" — one gesture, not
// click-then-open-the-menu. The pane it lands on must be the pane the
// menu describes, even when a different pane had focus.
//
// The hazard is ordering: the shell forwards the press and opens the
// menu in the same Update, while the clicked pane's focus request comes
// back as a command. If the screen only moved focus on that command, the
// menu would describe whatever was selected before the click.
func TestRightClickRetargetsTheMenu(t *testing.T) {
	m, drain := rightClickApp(t)

	// Focus starts on the left list; the verb is scoped to the right
	// table, so it is unavailable.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	drain(cmd)
	view := xansi.Strip(m.View())
	if !strings.Contains(view, "inspect") {
		t.Fatalf("menu should list the verb even when unavailable:\n%s", view)
	}
	if !strings.Contains(view, "focus detail") {
		t.Errorf("it should say why it is unavailable:\n%s", view)
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	drain(cmd)

	// Right-click a row in the right-hand table.
	m, cmd = m.Update(tea.MouseMsg{
		X: 70, Y: 8,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonRight,
	})
	drain(cmd)

	view = xansi.Strip(m.View())
	if !strings.Contains(view, "inspect") {
		t.Fatalf("right-click should open the menu:\n%s", view)
	}
	if strings.Contains(view, "focus detail") {
		t.Errorf("the menu still describes the old pane — retargeting didn't take:\n%s", view)
	}
}
