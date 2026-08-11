package screen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jsdrews/tuilib/pkg/geom"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// These tests cover an action's confirm-modal flow, modeled on the
// kube.yaml pods screen: a table with a delete action on `D`
// (interactive:false + a long confirm message). They pin two things that
// regressed there — the confirm message must render in full (it used to
// clip at the hardcoded 60-col modal edge) and pressing y must actually
// dispatch the command.

// podsWithDeleteConfig builds that screen. marker is touched by the
// delete's Run so a test can prove dispatch fired.
func podsWithDeleteConfig(marker string) *cfg.Config {
	no := false
	return &cfg.Config{
		Data: cfg.DataBlock{
			Sources: map[string]*cfg.Source{
				"pods": {
					Type: "static",
					Data: []any{
						map[string]any{"name": "nginx-56c45fd5ff-75rx7", "ns": "default"},
						map[string]any{"name": "redis-79dc448996-4bvkn", "ns": "default"},
					},
				},
			},
		},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"pods_table": {
					Type:       "table",
					Source:     "pods",
					Filterable: true,
					Columns: []cfg.Column{
						{Title: "Name", Value: cfg.Path{"name"}},
						{Title: "Namespace", Value: cfg.Path{"ns"}},
					},
				},
			},
			Screen: cfg.Screen{
				Layout: cfg.Node{Component: "pods_table"},
				Actions: []cfg.Action{
					{
						Key:         "D",
						Label:       "delete",
						Source:      "pods_table",
						Interactive: &no,
						Confirm:     "Delete pod ${selection.Name} in ${selection.Namespace}? This cannot be undone.",
						Run:         []string{"sh", "-c", "touch " + marker},
					},
				},
			},
		},
	}
}

// deleteScreen builds the model and primes the initial fetch so the table
// has rows (and therefore a real selection to substitute into the confirm
// message / run argv).
func deleteScreen(t *testing.T, c *cfg.Config) *Model {
	t.Helper()
	m := newTestModel(t, c)
	runToQuiescence(t, m, drainCmd(m.OnEnter(nil))...)
	return m
}

// TestActionConfirm_MessageNotTruncated pins the reported bug: the delete
// confirm message ("… This cannot be undone.") is wider than the old
// 60-col modal and clipped its tail. The fitted modal must now show it all.
func TestActionConfirm_MessageNotTruncated(t *testing.T) {
	m := deleteScreen(t, podsWithDeleteConfig(filepath.Join(t.TempDir(), "marker")))

	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if m.confirmModal == nil {
		t.Fatalf("expected confirm modal after pressing D")
	}
	plain := xansi.Strip(m.Layout().Render(geom.New(0, 0, 100, 40)))
	if !strings.Contains(plain, "This cannot be undone.") {
		t.Errorf("confirm message clipped; full text not present in render:\n%s", plain)
	}
}

// TestActionConfirm_WrapsMultiLine covers a confirm message long enough to
// need multiple wrapped lines: the modal must grow vertically and keep the
// message tail and both buttons visible.
func TestActionConfirm_WrapsMultiLine(t *testing.T) {
	c := podsWithDeleteConfig(filepath.Join(t.TempDir(), "marker"))
	c.TUI.Screen.Actions[0].Confirm = "Delete pod ${selection.Name} in namespace " +
		"${selection.Namespace}? This permanently removes the pod and cannot be " +
		"undone; the owning controller may immediately recreate it under a new name."
	m := deleteScreen(t, c)

	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if m.confirmModal == nil {
		t.Fatalf("expected confirm modal after pressing D")
	}
	plain := xansi.Strip(m.Layout().Render(geom.New(0, 0, 100, 40)))
	for _, want := range []string{"new name.", "[ No ]", "[ Yes ]"} {
		if !strings.Contains(plain, want) {
			t.Errorf("multi-line confirm clipped: %q missing in render:\n%s", want, plain)
		}
	}
}

// TestActionConfirm_YesDispatches proves the delete actually runs: D opens
// the confirm, y commits Yes, and the non-interactive command executes
// (touches the marker). Enter alone would commit the default No — the
// confirm is destructive-safe — which is why the demo requires y.
func TestActionConfirm_YesDispatches(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	m := deleteScreen(t, podsWithDeleteConfig(marker))

	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("delete dispatch did not run the command; marker missing: %v", err)
	}
}

// TestActionConfirm_EnterDefaultsToNo documents the safe default: with the
// modal up, Enter commits No, so no dispatch happens and the marker is
// absent. (Pressing y is required to delete — see YesDispatches.)
func TestActionConfirm_EnterDefaultsToNo(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	m := deleteScreen(t, podsWithDeleteConfig(marker))

	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.confirmModal != nil {
		t.Errorf("confirm modal should be dismissed after Enter")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("Enter should default to No and NOT dispatch, but the command ran")
	}
}
