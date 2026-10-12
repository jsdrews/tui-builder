package screen

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// settle is runToQuiescence for a screen with a text input in it: a
// focused input keeps a cursor blinking, one tick per message, so the
// blink messages are dropped rather than pumped forever.
func settle(t *testing.T, m *Model, msgs ...tea.Msg) {
	t.Helper()
	inbox := append([]tea.Msg{tea.WindowSizeMsg{Width: 100, Height: 40}}, msgs...)
	for i := 0; i < 200 && len(inbox) > 0; i++ {
		msg := inbox[0]
		inbox = inbox[1:]
		if strings.HasPrefix(fmt.Sprintf("%T", msg), "cursor.") {
			continue
		}
		_, cmd := m.Update(msg)
		for _, next := range drainCmd(cmd) {
			if !strings.HasPrefix(fmt.Sprintf("%T", next), "cursor.") {
				inbox = append(inbox, next)
			}
		}
	}
}

func inputsExample(t *testing.T) *Model {
	t.Helper()
	c, err := cfg.Load(filepath.Join("..", "..", "examples", "inputs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, c)
	settle(t, m, drainCmd(m.Init())...) // focuses the first component, as the app does
	settle(t, m, drainCmd(m.OnEnter(nil))...)
	return m
}

func podNames(m *Model) []string {
	var out []string
	for _, r := range m.tree.Components["pods_table"].Table.Rows() {
		out = append(out, r[0])
	}
	return out
}

func keys(s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}

var enter = tea.KeyMsg{Type: tea.KeyEnter}

// The table opens with the inputs' initial values, refetches only when
// one commits, and a revert puts back what was committed.
func TestInputsDriveTheirSource(t *testing.T) {
	m := inputsExample(t)
	if got := podNames(m); len(got) != 3 {
		t.Fatalf("initial rows = %v, want web, api, db (deleted hidden, no query)", got)
	}
	// The search field is first in the layout, so it has focus and is
	// editing. Typing alone doesn't refetch.
	settle(t, m, keys("ap")...)
	if got := podNames(m); len(got) != 3 {
		t.Errorf("typing without enter refetched: %v", got)
	}
	settle(t, m, enter)
	if got := podNames(m); len(got) != 1 || got[0] != "api" {
		t.Errorf("after committing \"ap\", rows = %v, want [api]", got)
	}
	if m.inputs["search"] != "ap" {
		t.Errorf("committed search = %q", m.inputs["search"])
	}

	// esc puts the committed text back and leaves the field.
	in := m.tree.Components["search"].Input
	settle(t, m, keys("zz")...)
	settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if in.Value() != "ap" || in.Focused() {
		t.Errorf("after esc: value %q focused %v, want ap and left", in.Value(), in.Focused())
	}

	// Tab to the toggle; a flip commits and refetches.
	settle(t, m, tea.KeyMsg{Type: tea.KeyTab})
	settle(t, m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if m.inputs["show_deleted"] != "true" {
		t.Fatalf("toggle didn't commit: %q", m.inputs["show_deleted"])
	}
	settle(t, m, tea.KeyMsg{Type: tea.KeyTab})
	settle(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := podNames(m); len(got) != 1 || got[0] != "api" {
		t.Errorf("deleted shown, query ap: rows = %v, want [api]", got)
	}
}

// ${input.*} in an action resolves to the committed value when it runs.
func TestInputValuesReachActions(t *testing.T) {
	m := inputsExample(t)
	settle(t, m, keys("db")...)
	settle(t, m, enter)
	def := m.inputDef(m.actionDefs["note"])
	if want := "${inputs.name} (search: db)"; def.Run[1] != want {
		t.Errorf("run = %q, want %q", def.Run[1], want)
	}
	if m.actionDefs["note"].Run[1] == def.Run[1] {
		t.Error("the shared action definition should be left alone")
	}
}
