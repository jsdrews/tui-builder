package screen

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tscreen "github.com/jsdrews/tuilib/pkg/screen"
)

// tuilib's screen.Stack hands every non-input message to every screen on
// the stack, not just the top. Two screens built from the same config share
// source names, so a message one of them asked for must not land on the
// other: the covered screen would paint the top screen's rows, or run an
// action against its own selection when the top screen's menu was picked.
func TestStackedScreensIgnoreEachOthersMessages(t *testing.T) {
	parent := menuModel(t, nil, nil)
	child := menuModel(t, nil, nil)

	var s tscreen.Stack
	s = tscreen.NewStack(parent)
	s, _ = s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	s, _ = s.Update(tscreen.PushMsg{Screen: child})

	before := len(parent.tree.Components["pods"].List.Items())
	cmd := child.own(func() tea.Msg {
		return fetchMsg{source: "items", data: []any{
			map[string]any{"name": "a"},
			map[string]any{"name": "b"},
			map[string]any{"name": "c"},
		}}
	})
	s, _ = s.Update(cmd())

	if got := len(child.tree.Components["pods"].List.Items()); got != 3 {
		t.Errorf("child holds %d items, want the 3 it fetched", got)
	}
	if got := len(parent.tree.Components["pods"].List.Items()); got != before {
		t.Errorf("parent holds %d items, want %d — it applied the child's fetch", got, before)
	}
}
