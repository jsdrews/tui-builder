package screen

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// inputKey handles a key on a focused input or toggle. An input edits
// while it holds focus: enter commits what's typed, esc puts back the
// committed value and leaves the field (so q and esc reach the app
// again), and enter on a field already left starts editing it again. A
// toggle commits on every flip. Tab is left to the screen.
func (m *Model) inputKey(k tea.KeyMsg) (tea.Cmd, bool) {
	c := m.current()
	if c == nil || (c.Kind != build.KInput && c.Kind != build.KToggle) {
		return nil, false
	}
	switch k.String() {
	case "tab", "shift+tab":
		return nil, false
	}
	name := m.tree.Order[m.focus]
	if c.Kind == build.KToggle {
		cmd := updateComponent(c, k)
		return tea.Batch(cmd, m.commitInput(name)), true
	}
	if !c.Input.Focused() {
		if k.String() == "enter" {
			return c.Input.Focus(), true
		}
		return nil, false
	}
	switch k.String() {
	case "enter":
		return m.commitInput(name), true
	case "esc":
		c.Input.SetValue(m.inputs[name])
		c.Input.Blur()
		return nil, true
	}
	return updateComponent(c, k), true
}

// commitInput records name's current value as committed and refetches
// every binding that reads it. Nothing happens when the value didn't
// change.
func (m *Model) commitInput(name string) tea.Cmd {
	v := build.InputValue(m.tree.Components[name])
	if prev, ok := m.inputs[name]; ok && prev == v {
		return nil
	}
	m.inputs[name] = v
	var cmds []tea.Cmd
	for _, b := range m.cursorBindings {
		if readsInput(b.bind, name) {
			cmds = append(cmds, m.startCursorFetch(b))
		}
	}
	return tea.Batch(cmds...)
}

func readsInput(bind map[string]string, name string) bool {
	for _, tmpl := range bind {
		for _, ref := range cfg.InputRefs(tmpl) {
			if ref == name {
				return true
			}
		}
	}
	return false
}

// bindInputs resolves ${input.*} in a binding's templates — bind:,
// confirm and notice — against the committed values, when the action
// starts. Everything downstream (the form, the confirm, a fan-out, a
// push) works from the resolved copy.
func (m *Model) bindInputs(b cfg.ActionBinding) cfg.ActionBinding {
	if len(m.inputs) == 0 {
		return b
	}
	if len(b.Bind) > 0 {
		bind := make(map[string]string, len(b.Bind))
		for k, v := range b.Bind {
			bind[k] = build.SubstituteInputValues(v, m.inputs)
		}
		b.Bind = bind
	}
	b.Confirm.Message = build.SubstituteInputValues(b.Confirm.Message, m.inputs)
	b.Notice = build.SubstituteInputValues(b.Notice, m.inputs)
	return b
}

// inputDef resolves ${input.*} in an action's argv, URL, method, headers
// and body when it runs. The action is shared by every screen that binds
// it, so this works on a copy.
func (m *Model) inputDef(def *cfg.Action) *cfg.Action {
	if def == nil || len(m.inputs) == 0 {
		return def
	}
	reads := false
	for _, t := range def.Templates() {
		if len(cfg.InputRefs(t)) > 0 {
			reads = true
			break
		}
	}
	if !reads {
		return def
	}
	sub := func(s string) string { return build.SubstituteInputValues(s, m.inputs) }
	out := *def
	out.Run = make([]string, len(def.Run))
	for i, a := range def.Run {
		out.Run[i] = sub(a)
	}
	out.Method, out.URL, out.Body = sub(def.Method), sub(def.URL), sub(def.Body)
	if def.Headers != nil {
		out.Headers = make(map[string]string, len(def.Headers))
		for k, v := range def.Headers {
			out.Headers[k] = sub(v)
		}
	}
	return &out
}
