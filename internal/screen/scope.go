package screen

// Screen-scoped messages.
//
// tuilib's screen.Stack hands input to the top screen only, but every
// other message — timers, fetch results, run outcomes — to every screen
// on the stack, so a covered screen keeps its polls alive. A pushed
// screen is another Model built from the same config, with the same
// source and component names, so the messages this package invents
// can't say whose they are by name alone. Left unscoped, a covered
// screen would paint the top screen's fetch into its own table, and
// would answer the top screen's menu pick by running the action against
// its own selection.
//
// So every Model has an id, and every internal message it hands to the
// runtime travels inside a scopedMsg carrying it. Update unwraps its
// own and drops everyone else's. The runner's messages aren't ours to
// wrap — they are built inside tuilib — so they are matched by the
// *exec.Cmd the screen launched instead (see ownsRun).

import (
	"os/exec"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"

	tscreen "github.com/jsdrews/tuilib/pkg/screen"
)

var nextModelID atomic.Uint64

// scopedMsg is an internal message stamped with the Model that asked
// for it.
type scopedMsg struct {
	owner uint64
	msg   tea.Msg
}

// own stamps the internal messages cmd produces with m's id. Messages
// meant for tuilib (focus requests, app.Info, screen.Push, component
// ticks) pass through untouched; the runtime has to see them as they
// are. Batches are walked, the same way tagWindowRequest walks them.
func (m *Model) own(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	id := m.id
	var wrap func(tea.Cmd) tea.Cmd
	wrap = func(cmd tea.Cmd) tea.Cmd {
		if cmd == nil {
			return nil
		}
		return func() tea.Msg {
			msg := cmd()
			switch x := msg.(type) {
			case tea.BatchMsg:
				out := make(tea.BatchMsg, 0, len(x))
				for _, sub := range x {
					out = append(out, wrap(sub))
				}
				return out
			case fetchMsg, tickMsg, streamMsg, postApplyMsg,
				actionPickedMsg, actionResultMsg,
				taggedCursorMsg, cursorFetchMsg,
				windowViewportMsg, windowQueryMsg, windowTickMsg:
				return scopedMsg{owner: id, msg: msg}
			}
			return msg
		}
	}
	return wrap(cmd)
}

// Update is the stack's entry point: it drops messages another screen
// asked for, unwraps this screen's own, and stamps whatever the screen
// asks for next.
func (m *Model) Update(msg tea.Msg) (tscreen.Screen, tea.Cmd) {
	if s, ok := msg.(scopedMsg); ok {
		if s.owner != m.id {
			return m, nil
		}
		msg = s.msg
	}
	scr, cmd := m.update(msg)
	return scr, m.own(cmd)
}

// launched records a subprocess this screen started, so its outcome is
// claimed by this screen alone.
func (m *Model) launched(cmd *exec.Cmd) {
	if m.runs == nil {
		m.runs = map[*exec.Cmd]bool{}
	}
	m.runs[cmd] = true
}

// ownsRun reports whether a runner outcome is this screen's to report,
// and forgets the run if so. A subprocess is matched by its *exec.Cmd.
// A Go run carries none; its outcome goes to the screen that isn't
// covered by another one of ours, which is the screen the user started
// it from.
func (m *Model) ownsRun(cmd *exec.Cmd) bool {
	if cmd == nil {
		return !m.covered
	}
	if !m.runs[cmd] {
		return false
	}
	delete(m.runs, cmd)
	return true
}
