package screen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	taction "github.com/jsdrews/tuilib/pkg/action"

	"github.com/jsdrews/tui-builder/internal/action"
	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Actions implements tuilib's action.Provider: the screen names its
// verbs and what they will act on, and the shell owns everything after
// the pick — the menu overlay, the confirm modal, the goroutine,
// cancellation, the console entry, the statusbar badge, the kill picker
// and the per-target exclusivity check.
//
// Every binding appears, including ones that can't fire right now.
// Hiding an unavailable verb teaches the user it doesn't exist; showing
// it with a reason teaches them it doesn't apply yet, which is the true
// statement.
func (m *Model) Actions() taction.Set {
	if len(m.actions) == 0 {
		return taction.Set{}
	}
	focused := ""
	if m.focus >= 0 && m.focus < len(m.tree.Order) {
		focused = m.tree.Order[m.focus]
	}
	cur := m.current()
	target, count := m.selectionTarget(cur)

	out := make([]taction.Action, 0, len(m.actions))
	for _, b := range m.actions {
		def := m.actionDefs[b.Action]
		if def == nil {
			continue
		}
		out = append(out, m.menuAction(b, def, cur, focused))
	}
	return taction.Set{Target: target, Count: count, Actions: out}
}

// selectionTarget names what the verbs will act on, and how many.
//
// Marked rows first (MarkedSelections already resolves marks-or-cursor
// the way tuilib's Selection does), falling back to the focused row for
// components that carry no keys — an unmarkable list still has a
// selection, it just isn't a set.
func (m *Model) selectionTarget(c *build.Component) (string, int) {
	if c == nil {
		return "", 0
	}
	if sels := build.MarkedSelections(c); len(sels) > 0 {
		if len(sels) == 1 {
			return sels[0].String, 1
		}
		return fmt.Sprintf("%d items", len(sels)), len(sels)
	}
	if s := selectionFrom(c); s.String != "" {
		return s.String, 1
	}
	return "", 0
}

// menuAction maps one binding onto a menu entry.
//
// The split between Run and Do is the whole design. Run is background
// work the shell can attribute, group, cancel and report on, so
// everything that can be Run is. Do is the escape hatch, and there are
// exactly two things that need it: an interactive command, which wants
// the terminal rather than a writer, and an action with inputs nobody
// bound, which has to ask before it can run at all.
func (m *Model) menuAction(b cfg.ActionBinding, def *cfg.Action, cur *build.Component, focused string) taction.Action {
	a := taction.Action{
		Label: labelOr(b.Label, b.Action),
		Desc:  def.Description,
	}

	// A binding scoped to a pane is only meaningful while that pane has
	// focus — the selection its bind: templates read comes from there.
	if b.From != "" && b.From != focused {
		a.Disabled = "focus " + b.From
		return a
	}

	sel := build.Selection{}
	if b.From != "" && cur != nil {
		sel = selectionFrom(cur)
	}
	inputs := resolveBinds(b, def, sel)

	// Inputs the call site didn't fill have to be asked for, and the
	// shell's ChosenMsg handler runs the action without ever reaching
	// us. Do hands control back instead: the cmd emits one of our
	// messages, which does get forwarded, and the screen opens its form.
	if len(unboundInputs(def, inputs)) > 0 {
		a.Do = func() tea.Cmd {
			return func() tea.Msg { return actionPickedMsg{binding: b} }
		}
		return a
	}

	resolved, err := action.Resolve(def, inputs)
	if err != nil {
		a.Disabled = err.Error()
		return a
	}
	vals := resolved.Values()
	if b.Confirm != "" {
		// Substituted against the RESOLVED values, so an input carrying
		// a default previews what will actually run rather than an
		// empty string.
		a.Confirm = build.SubstituteAll([]string{b.Confirm}, sel, vals)[0]
	}

	// Interactive means "hand over the terminal", which a writer can't
	// do. Everything else streams into the console.
	if resolved.Kind != "http" && b.IsInteractive() {
		notice := build.SubstituteAll([]string{b.Notice}, sel, vals)[0]
		argv := append([]string(nil), resolved.Argv...)
		a.Do = func() tea.Cmd {
			m.trackRun(resolved)
			return m.dispatch(argv, notice, true)
		}
		return a
	}
	a.Run = runFunc(resolved)
	return a
}

// actionPickedMsg carries a menu pick that needs input before it can
// run. It exists because ChosenMsg never reaches a screen: the shell
// matches it, acts, and returns.
type actionPickedMsg struct{ binding cfg.ActionBinding }

// runFunc turns a resolved action into background work.
//
// Output goes two places at once: to the writer, so the console shows it
// arriving, and to a buffer, because `success:` expressions and
// ${body.*} in message: read the whole thing after the fact. Streaming
// without capturing would cost the verdict; capturing without streaming
// would cost the live view.
func runFunc(r *action.Resolved) taction.Func {
	return func(ctx context.Context, out io.Writer) error {
		if r.Kind == "http" {
			res := r.Do(ctx)
			if body := strings.TrimSpace(res.Output); body != "" {
				fmt.Fprintln(out, body)
			}
			fmt.Fprintln(out, res.Summary)
			if !res.OK {
				return errors.New(res.Summary)
			}
			return nil
		}
		if len(r.Argv) == 0 {
			return errors.New("action has no command to run")
		}
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, r.Argv[0], r.Argv[1:]...)
		cmd.Stdout = io.MultiWriter(out, &buf)
		cmd.Stderr = cmd.Stdout
		err := cmd.Run()
		res := r.Finish(exitCode(err), buf.String())
		if !res.OK {
			fmt.Fprintln(out, res.Summary)
			return errors.New(res.Summary)
		}
		if res.Summary != "" {
			fmt.Fprintln(out, res.Summary)
		}
		return nil
	}
}
