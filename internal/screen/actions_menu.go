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
	xansi "github.com/charmbracelet/x/ansi"

	taction "github.com/jsdrews/tuilib/pkg/action"
	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/runner"

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
		Label: menuLabel(b, def),
		Desc:  def.Description,
	}

	// A binding scoped to a pane is only meaningful while that pane has
	// focus — the selection its bind: templates read comes from there.
	if b.From != "" && b.From != focused {
		a.Disabled = "focus " + b.From
		return a
	}

	sels := m.selectionsFor(b, cur)
	sel := build.Selection{}
	if len(sels) > 0 {
		sel = sels[0]
	}
	a.Multi = def.Multi
	// A push is navigation: nothing to stream, nothing to cancel, and
	// the destination's parameters come from bind: rather than from the
	// action's inputs. Do is the right shape, and tuilib says so —
	// navigational Do actions are one-at-a-time by construction, since
	// pushing a screen replaces what is on top.
	if def.Kind() == "push" {
		a.Do = func() tea.Cmd {
			cmd, _ := m.pushAction(b, def, sel)
			return cmd
		}
		return a
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
		msg := build.SubstituteAll([]string{b.Confirm}, sel, vals)[0]
		// Under a fan-out the author's template resolved against ONE
		// row, so on its own it would say "Delete web?" while deleting
		// three. Say the arity rather than rewriting their sentence:
		// the count is the part they can't have written, since they
		// didn't know it. Inline and short, because of the budget below.
		if def.Multi && len(sels) > 1 {
			msg = fmt.Sprintf("%s (%d rows)", msg, len(sels))
		}
		a.Confirm = fitShellConfirm(msg)
	}

	// Fan-out. One run per marked row rather than one run over a joined
	// argv: N runs are individually tracked, individually cancellable
	// and individually logged, and RunKey pairs exclusivity with the
	// target so restarting `web` while `api` restarts is fine.
	//
	// Do rather than Run because the shell's Run path starts exactly one
	// run for the whole Set. tuilib still sees every one of them: they
	// go out through the same runner.GoWith it would have used.
	if def.Multi && len(sels) > 1 {
		a.Do = func() tea.Cmd { return m.fanOut(b, def, a, sels) }
		return a
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

// menuLabel names the verb in the menu. An explicit `label:` wins; a
// push falls back to "open <screen>", which reads as a verb where the
// bare action name ("open_cities") reads as an identifier; anything else
// falls back to the action's name, which is all there is.
func menuLabel(b cfg.ActionBinding, def *cfg.Action) string {
	if b.Label != "" {
		return b.Label
	}
	if def.Kind() == "push" && def.Push != "" {
		return "open " + def.Push
	}
	return b.Action
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

// selectionsFor lists the rows a binding will act on: every marked row
// when the binding is scoped to a pane, otherwise nothing.
//
// It goes through MarkedSelections, which already follows tuilib's
// Selection contract — marked rows if any, else the cursor row. A
// hand-written version of that branch is how a verb quietly acts on one
// row when the user marked six.
func (m *Model) selectionsFor(b cfg.ActionBinding, cur *build.Component) []build.Selection {
	if b.From == "" || cur == nil {
		return nil
	}
	if sels := build.MarkedSelections(cur); len(sels) > 0 {
		return sels
	}
	if s := selectionFrom(cur); s.String != "" {
		return []build.Selection{s}
	}
	return nil
}

// fanOut turns one pick into N tracked runs, one per marked row.
//
// Each row resolves independently — bind: templates are evaluated
// against that row — so the argv, the success verdict and the summary
// are that row's own. Inputs the binding didn't fill were collected once
// before this and apply to every row, which is the right split: bind:
// is per-row by construction, a form answer is not.
func (m *Model) fanOut(b cfg.ActionBinding, def *cfg.Action, a taction.Action, sels []build.Selection, extra ...action.Inputs) tea.Cmd {
	var cmds []tea.Cmd
	for _, sel := range sels {
		inputs := resolveBinds(b, def, sel)
		for _, e := range extra {
			for k, v := range e {
				if _, bound := inputs[k]; !bound {
					inputs[k] = v
				}
			}
		}
		resolved, err := action.Resolve(def, inputs)
		if err != nil {
			cmds = append(cmds, app.Error(fmt.Sprintf("%s: %v", b.Action, err)))
			continue
		}
		target := sel.String
		cmds = append(cmds, runner.GoWith(runner.GoOptions{
			Label:  a.Label,
			Detail: a.Label + " · " + target,
			Tag:    taction.RunKey(a, target),
			Run:    runFunc(resolved),
		}))
	}
	return tea.Batch(cmds...)
}

// The shell centres its confirm modal at a fixed 52x7 (pkg/app), which
// leaves 48 columns and three lines of message once the border, the
// spacer and the button row are taken out. It does not wrap, so a long
// message loses its tail — and the tail of a confirm is where "cannot be
// undone" lives.
const (
	shellConfirmWidth = 48
	shellConfirmLines = 3
)

// fitShellConfirm wraps a confirm message to the shell modal's budget,
// and folds any overflow into a final line rather than letting it fall
// off the bottom.
//
// Our own modal (newConfirmModal) sizes itself to the text and needs
// none of this; it is only the shell's fixed one that has to be fitted.
func fitShellConfirm(msg string) string {
	wrapped := xansi.Wrap(msg, shellConfirmWidth, " -")
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= shellConfirmLines {
		return wrapped
	}
	// Keep the opening, which carries the verb and the target, and say
	// plainly that there is more rather than truncating mid-sentence.
	kept := lines[:shellConfirmLines-1]
	kept = append(kept, "… (message truncated to fit)")
	return strings.Join(kept, "\n")
}
