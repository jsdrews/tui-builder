// Package screen wraps a built layout tree as a tuilib screen.Screen.
// One component holds keyboard focus at a time: tab / shift+tab cycle
// through the components in walk order. Only the focused component
// receives KeyMsgs; non-key messages fan out so spinner ticks reach every
// pane. IsCapturingKeys reflects only the focused component, so the
// surrounding app shell's q/ctrl+c suppression is scoped to "the thing
// the user is interacting with" rather than "anything in the layout."
package screen

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/alert"
	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/confirm"
	"github.com/jsdrews/tuilib/pkg/layout"
	"github.com/jsdrews/tuilib/pkg/runner"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// Multi is the shared context for a multi-screen app: every named screen,
// the top-level components map, and the top-level data sources map.
// Passed once to every Model so any screen can build + push its on_enter
// targets, and so pushed screens get the same data sources.
type Multi struct {
	Screens     map[string]*cfg.Screen
	Components  map[string]*cfg.Component
	DataSources map[string]*cfg.DataSource
}

// sourceEntry pairs a live data source with its iterable-root path so
// fetch results can be sliced once at apply time. loaded flips true after
// the first successful fetch — used to suppress the loading spinner on
// polling refreshes (the data is already on screen; flashing to a
// spinner and back would just flicker).
type sourceEntry struct {
	src    ds.Source
	root   string
	loaded bool
}

// fetchMsg carries the result of one source fetch back into Update.
type fetchMsg struct {
	source string
	data   any
	err    error
}

// tickMsg signals "time to refetch source X." Emitted by the per-source
// tea.Tick scheduled after each successful fetch when refresh > 0.
type tickMsg struct{ source string }

// nonInteractiveResult is the outcome of a non-interactive action
// dispatch. Used instead of runner.Result for the no-TTY path.
type nonInteractiveResult struct {
	stdout string
	stderr string
	err    error
}

// Model is the config-driven screen. Construct with New (single-screen)
// or NewMulti (multi-screen with on_enter push support) and pass as the
// root to app.New.
type Model struct {
	title    string
	th       theme.Theme
	tree     *build.Tree
	focus    int               // index into tree.All(); -1 means no component focused
	multi    *Multi            // nil for single-screen mode
	bindings map[string]string // component name -> target screen name (this screen's on_enter)
	actions  []cfg.Action      // per-key subprocess bindings for this screen

	// Data-source state. sources is keyed by source name; sourceUsers
	// indexes the bound components per source so a single fetch can fan
	// out to every consumer.
	sources     map[string]*sourceEntry
	sourceUsers map[string][]string // source name -> component names
	started     bool                // OnEnter has fired the initial fetch wave

	// Confirm-modal state. When confirmModal is non-nil it overlays the
	// body via ZStack and captures all keys until ConfirmedMsg /
	// CancelledMsg arrives. pendingArgv holds the fully-substituted argv
	// to dispatch on confirmation.
	confirmModal       *confirm.Model
	pendingArgv        []string
	pendingNotice      string
	pendingInteractive bool

	// Alert-modal state. Shown when an action dispatch returns a non-nil
	// error (subprocess failed to start, exited non-zero, etc.). One OK
	// button; dismissed on enter/space/esc/o.
	alertModal *alert.Model

	// inFlightStderr captures the dispatched interactive subprocess's
	// stderr so the alert can show the actual error text. Set in
	// dispatch() on the interactive path; consumed and cleared on the
	// next runner.Result. Non-interactive dispatches carry their own
	// stderr inside nonInteractiveResult instead.
	inFlightStderr *bytes.Buffer
}

// New builds a single-screen Model. Components are looked up by name in
// the layout tree.
func New(s *cfg.Screen, components map[string]*cfg.Component, dataSources map[string]*cfg.DataSource, th theme.Theme) (*Model, error) {
	return build_(s, components, dataSources, th, nil)
}

// NewMulti builds a Model from one screen in a multi-screen Config. The
// Multi context lets enter on a bound list/table push another screen via
// the screen.Stack. The selection captured at push time substitutes
// ${selection} tokens in the pushed screen's config — and in any
// data-source URL/headers/body referenced from that screen.
func NewMulti(screenName string, multi *Multi, sel build.Selection, th theme.Theme) (*Model, error) {
	src, ok := multi.Screens[screenName]
	if !ok {
		return nil, fmt.Errorf("screen %q not defined", screenName)
	}
	subScreen, subComponents, subSources := build.SubstituteScreen(src, multi.Components, multi.DataSources, sel)
	return build_(subScreen, subComponents, subSources, th, multi)
}

func build_(s *cfg.Screen, components map[string]*cfg.Component, dataSources map[string]*cfg.DataSource, th theme.Theme, multi *Multi) (*Model, error) {
	tree, err := build.Build(&s.Layout, components, th)
	if err != nil {
		return nil, err
	}
	m := &Model{title: s.Title, th: th, tree: tree, focus: -1, multi: multi}
	if len(tree.All()) > 0 {
		m.focus = 0
	}
	if multi != nil {
		m.bindings = map[string]string{}
		for _, b := range s.OnEnter {
			m.bindings[b.Source] = b.Push
		}
	}
	m.actions = append([]cfg.Action(nil), s.Actions...)

	// Wire data sources for any bound components in this screen.
	m.sources = map[string]*sourceEntry{}
	m.sourceUsers = map[string][]string{}
	for _, name := range tree.Order {
		c := tree.Components[name]
		srcName := c.Cfg.Source
		if srcName == "" {
			continue
		}
		def, ok := dataSources[srcName]
		if !ok {
			return nil, fmt.Errorf("component %q: source %q not defined", name, srcName)
		}
		if _, ok := m.sources[srcName]; !ok {
			live, err := ds.New(def)
			if err != nil {
				return nil, fmt.Errorf("data_sources.%s: %w", srcName, err)
			}
			m.sources[srcName] = &sourceEntry{src: live, root: def.Root}
		}
		m.sourceUsers[srcName] = append(m.sourceUsers[srcName], name)
	}
	return m, nil
}

// Title satisfies screen.Screen.
func (m *Model) Title() string { return m.title }

// Init focuses the initial component so the first frame already shows the
// highlighted border on the focused pane.
func (m *Model) Init() tea.Cmd {
	m.applyFocus()
	return nil
}

// OnEnter fires each time the screen becomes the active top of the stack.
// On the first activation it kicks off one fetch per bound data source;
// subsequent activations (pop-back) reuse cached data and let polling
// handle refreshes.
func (m *Model) OnEnter(any) tea.Cmd {
	if m.started || len(m.sources) == 0 {
		return nil
	}
	m.started = true
	var cmds []tea.Cmd
	for name := range m.sources {
		if cmd := m.startFetch(name); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// Layout returns the live layout.Node tree built from the YAML config.
// When a modal (alert or confirm) is active it overlays a centered
// dialog on top of the body via ZStack — base still renders behind so
// the user sees what they're acting on. Alerts take precedence (they
// only arise after a dispatch attempt completed; at most one modal is
// up at a time in practice).
func (m *Model) Layout() layout.Node {
	body := m.tree.RenderNode()
	switch {
	case m.alertModal != nil:
		return layout.ZStack(body, layout.Center(70, 9, layout.Sized(m.alertModal)))
	case m.confirmModal != nil:
		return layout.ZStack(body, layout.Center(60, 7, layout.Sized(m.confirmModal)))
	}
	return body
}

// Update routes KeyMsgs to the focused component (with tab/shift+tab
// intercepted for focus cycling and enter intercepted when the focused
// component has an on_enter binding). Non-key messages fan out so
// spinner ticks reach every component.
func (m *Model) Update(msg tea.Msg) (tscreen.Screen, tea.Cmd) {
	// Alert modal takes precedence — single OK button, dismiss is the
	// only meaningful outcome. Other messages pass through to it so its
	// cursor blink etc. still ticks.
	if m.alertModal != nil {
		if _, ok := msg.(alert.DismissedMsg); ok {
			m.alertModal = nil
			return m, nil
		}
		next, cmd := m.alertModal.Update(msg)
		m.alertModal = &next
		return m, cmd
	}
	// Confirm modal takes precedence — it's a real modal so every key
	// goes to it until it resolves. We also watch for its Confirmed /
	// Cancelled messages here.
	if m.confirmModal != nil {
		switch msg.(type) {
		case confirm.ConfirmedMsg:
			argv := m.pendingArgv
			notice := m.pendingNotice
			interactive := m.pendingInteractive
			m.confirmModal = nil
			m.pendingArgv = nil
			m.pendingNotice = ""
			m.pendingInteractive = false
			return m, m.dispatch(argv, notice, interactive)
		case confirm.CancelledMsg:
			m.confirmModal = nil
			m.pendingArgv = nil
			m.pendingNotice = ""
			return m, nil
		}
		next, cmd := m.confirmModal.Update(msg)
		m.confirmModal = &next
		return m, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "tab":
			m.cycleFocus(+1)
			return m, nil
		case "shift+tab":
			m.cycleFocus(-1)
			return m, nil
		case "enter":
			if cmd, handled := m.tryPush(); handled {
				return m, cmd
			}
		default:
			if cmd, handled := m.tryAction(k); handled {
				return m, cmd
			}
		}
		switch k.String() {
		case "r":
			// Manual refresh — refetch every bound source. Suppressed
			// while a component is capturing keys (so 'r' typed into a
			// filter doesn't trigger fetches).
			if cur := m.current(); cur == nil || !componentCapturing(cur) {
				if len(m.sources) > 0 {
					var cmds []tea.Cmd
					for name := range m.sources {
						if cmd := m.startFetch(name); cmd != nil {
							cmds = append(cmds, cmd)
						}
					}
					return m, tea.Batch(cmds...)
				}
			}
		}
		if cur := m.current(); cur != nil {
			return m, updateComponent(cur, msg)
		}
		return m, nil
	}

	switch x := msg.(type) {
	case fetchMsg:
		return m, m.handleFetch(x)
	case tickMsg:
		return m, m.startFetch(x.source)
	case runner.Result:
		captured := ""
		if m.inFlightStderr != nil {
			captured = strings.TrimSpace(m.inFlightStderr.String())
			m.inFlightStderr = nil
		}
		return m, m.actionOutcome(captured, x.Err)
	case nonInteractiveResult:
		return m, m.actionOutcome(strings.TrimSpace(x.stderr), x.err)
	}

	var cmds []tea.Cmd
	for _, c := range m.tree.All() {
		if cmd := updateComponent(c, msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// startFetch returns a Cmd that runs the fetch in a goroutine. The
// loading spinner is shown only on the first fetch for a source — once
// data is on screen, subsequent polls swap in place to avoid flicker.
func (m *Model) startFetch(name string) tea.Cmd {
	entry, ok := m.sources[name]
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	if !entry.loaded {
		for _, compName := range m.sourceUsers[name] {
			c := m.tree.Components[compName]
			if cmd := setLoading(c, true); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	cmds = append(cmds, func() tea.Msg {
		data, err := entry.src.Fetch(context.Background())
		return fetchMsg{source: name, data: data, err: err}
	})
	return tea.Batch(cmds...)
}

// handleFetch applies the fetch result to every component bound to the
// source, surfaces errors via app.Error, and schedules the next tick when
// the source has a polling interval. The first successful fetch flips
// entry.loaded so subsequent refreshes skip the spinner.
func (m *Model) handleFetch(msg fetchMsg) tea.Cmd {
	entry, ok := m.sources[msg.source]
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	for _, compName := range m.sourceUsers[msg.source] {
		c := m.tree.Components[compName]
		if !entry.loaded {
			setLoading(c, false)
		}
		if msg.err == nil {
			root := ds.Get(msg.data, entry.root)
			build.ApplyData(c, root)
		}
	}
	if msg.err == nil {
		entry.loaded = true
	} else {
		cmds = append(cmds, app.Error(fmt.Sprintf("%s: %v", msg.source, msg.err)))
	}
	if d := entry.src.Refresh(); d > 0 {
		name := msg.source
		cmds = append(cmds, tea.Tick(d, func(time.Time) tea.Msg {
			return tickMsg{source: name}
		}))
	}
	return tea.Batch(cmds...)
}

// setLoading toggles the pane's loading state on a component and returns
// the spinner's kick-off command (CLAUDE rule 15).
func setLoading(c *build.Component, on bool) tea.Cmd {
	switch c.Kind {
	case build.KList:
		return c.List.SetLoading(on)
	case build.KTable:
		return c.Table.SetLoading(on)
	case build.KLogview:
		return c.Logview.SetLoading(on)
	case build.KTree:
		return c.Tree.SetLoading(on)
	case build.KInspector:
		return c.Inspector.SetLoading(on)
	}
	return nil
}

// IsCapturingKeys reflects whether the *focused* component is engaging
// keystrokes (filter typing). Also true while any modal is showing so
// q/t/esc-pop are routed to the modal, not the app shell.
func (m *Model) IsCapturingKeys() bool {
	if m.alertModal != nil || m.confirmModal != nil {
		return true
	}
	c := m.current()
	if c == nil {
		return false
	}
	return componentCapturing(c)
}

// Help returns the bindings the focused component currently exposes,
// plus "enter → open" when this screen has an on_enter binding for the
// focused component, plus any action keys bound to the focused source.
func (m *Model) Help() []key.Binding {
	c := m.current()
	if c == nil {
		return nil
	}
	out := componentHelp(c)
	if m.focus < 0 || componentCapturing(c) {
		return out
	}
	name := m.tree.Order[m.focus]
	if m.multi != nil {
		if _, ok := m.bindings[name]; ok {
			out = append(out,
				key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "open")),
			)
		}
	}
	for _, a := range m.actions {
		if a.Source != name {
			continue
		}
		label := a.Label
		if label == "" {
			label = "action"
		}
		out = append(out, key.NewBinding(key.WithKeys(a.Key), key.WithHelp(a.Key, label)))
	}
	return out
}

// SetTheme rebuilds each component against the new palette, preserving
// cursor / value / sort state via component accessors.
func (m *Model) SetTheme(t theme.Theme) {
	m.th = t
	for _, c := range m.tree.All() {
		c.Rebuild(t)
	}
	// Rebuild() may have reset pane focus state — re-apply the screen's
	// focus highlight on top.
	m.applyFocus()
}

func (m *Model) current() *build.Component {
	all := m.tree.All()
	if m.focus < 0 || m.focus >= len(all) {
		return nil
	}
	return all[m.focus]
}

func (m *Model) cycleFocus(delta int) {
	all := m.tree.All()
	if len(all) == 0 {
		return
	}
	setFocused(all[m.focus], false)
	m.focus = (m.focus + delta + len(all)) % len(all)
	setFocused(all[m.focus], true)
}

// applyFocus pushes the screen's focus index into pane-level focus on
// every component (true for the focused one, false for the rest).
func (m *Model) applyFocus() {
	for i, c := range m.tree.All() {
		setFocused(c, i == m.focus)
	}
}

func setFocused(c *build.Component, on bool) {
	switch c.Kind {
	case build.KList:
		c.List.SetFocused(on)
	case build.KTable:
		c.Table.SetFocused(on)
	case build.KLogview:
		c.Logview.SetFocused(on)
	case build.KTree:
		c.Tree.SetFocused(on)
	case build.KInspector:
		c.Inspector.SetFocused(on)
	}
}

func componentCapturing(c *build.Component) bool {
	switch c.Kind {
	case build.KList:
		return c.List.Filtering()
	case build.KTable:
		return c.Table.Filtering()
	case build.KLogview:
		return c.Logview.Searching()
	case build.KTree:
		return c.Tree.Searching()
	case build.KInspector:
		return c.Inspector.Searching()
	}
	return false
}

func componentHelp(c *build.Component) []key.Binding {
	switch c.Kind {
	case build.KList:
		return c.List.Help()
	case build.KTable:
		return c.Table.Help()
	case build.KLogview:
		return c.Logview.Help()
	case build.KTree:
		return c.Tree.Help()
	case build.KInspector:
		return c.Inspector.Help()
	}
	return nil
}

// tryAction handles a KeyMsg that may match one of this screen's actions.
// Returns (cmd, true) when the action fired so the caller can short-
// circuit; otherwise (nil, false) to fall through to component routing.
// Suppressed while a component is capturing keys so action keys don't
// hijack filter typing.
func (m *Model) tryAction(k tea.KeyMsg) (tea.Cmd, bool) {
	if len(m.actions) == 0 || m.focus < 0 {
		return nil, false
	}
	cur := m.current()
	if cur == nil || componentCapturing(cur) {
		return nil, false
	}
	focusedName := m.tree.Order[m.focus]
	keyStr := k.String()
	for _, a := range m.actions {
		if a.Key != keyStr || a.Source != focusedName {
			continue
		}
		sel := selectionFrom(cur)
		argv := build.SubstituteAll(a.Run, sel)
		if len(argv) == 0 {
			return nil, true
		}
		// With `confirm:` set, show the modal first; otherwise dispatch
		// immediately.
		if a.Confirm != "" {
			msg := build.SubstituteAll([]string{a.Confirm}, sel)[0]
			modal := m.newConfirmModal(a.Label, msg)
			m.confirmModal = &modal
			m.pendingArgv = argv
			m.pendingNotice = a.Notice
			m.pendingInteractive = a.InteractiveDefault()
			return nil, true
		}
		return m.dispatch(argv, a.Notice, a.InteractiveDefault()), true
	}
	return nil, false
}

// dispatch routes a fully-substituted argv to either pkg/runner
// (interactive — suspends the alt-screen, hands the TTY to the
// subprocess) or to a goroutine-based exec (non-interactive — captures
// stdout/stderr, never suspends, no flicker). nil argv is a no-op.
//
// Stderr is always captured for the alert; on the interactive path it
// stays out of the terminal too (the live shell renders via stdout/pty,
// not stderr, so hiding stderr doesn't hurt any interactive flow we've
// encountered while keeping kubectl-level errors clean).
func (m *Model) dispatch(argv []string, notice string, interactive bool) tea.Cmd {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if interactive {
		var buf bytes.Buffer
		cmd.Stderr = &buf
		m.inFlightStderr = &buf
		if notice != "" {
			return runner.RunWithNotice(cmd, notice)
		}
		return runner.Run(cmd)
	}
	return func() tea.Msg {
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		err := cmd.Run()
		return nonInteractiveResult{
			stdout: stdoutBuf.String(),
			stderr: stderrBuf.String(),
			err:    err,
		}
	}
}

// actionOutcome turns a captured-stderr + error pair into the right
// follow-up command: alert on error, statusbar info on success.
func (m *Model) actionOutcome(captured string, err error) tea.Cmd {
	if err != nil {
		msg := captured
		if msg == "" {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("%s\n\n(%v)", captured, err)
		}
		a := m.newAlertModal("Action failed", msg)
		m.alertModal = &a
		return nil
	}
	return app.Info("action complete")
}

// newConfirmModal builds a yes/no confirm dialog using the active theme.
// The title is the action's label (or "Confirm" when blank); message is
// already substituted.
func (m *Model) newConfirmModal(label, message string) confirm.Model {
	opts := m.th.Confirm()
	if label == "" {
		label = "Confirm"
	}
	opts.Title = label
	opts.Message = message
	opts.Confirm = "Yes"
	opts.Cancel = "No"
	return confirm.New(opts)
}

// newAlertModal builds an error-tinted alert dialog. The message is
// wrapped to fit the modal width and capped at ~5 lines so very long
// subprocess errors don't blow up the screen — alert itself does not
// wrap text. tuilib's convention is to override ActiveColor with the
// theme's ErrorBG to get the red-edge "something went wrong" look.
func (m *Model) newAlertModal(title, message string) alert.Model {
	opts := m.th.Alert()
	opts.Title = title
	opts.Message = wrapMessage(message, 64, 5)
	opts.OK = "OK"
	opts.ActiveColor = m.th.ErrorBG
	return alert.New(opts)
}

// wrapMessage hard-wraps s into at most maxLines lines of maxWidth cells.
// Overflow lines are truncated with an ellipsis on the last visible line.
// Doesn't try to be word-boundary smart — error text isn't prose.
func wrapMessage(s string, maxWidth, maxLines int) string {
	var lines []string
	for _, raw := range strings.Split(s, "\n") {
		for len(raw) > maxWidth {
			lines = append(lines, raw[:maxWidth])
			raw = raw[maxWidth:]
		}
		lines = append(lines, raw)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		last := lines[maxLines-1]
		if len(last) > maxWidth-1 {
			last = last[:maxWidth-1]
		}
		lines[maxLines-1] = last + "…"
	}
	return strings.Join(lines, "\n")
}

// tryPush handles enter when the focused component has an on_enter binding.
// Returns (cmd, true) when handled; (nil, false) to fall through to the
// normal forward-to-component path.
func (m *Model) tryPush() (tea.Cmd, bool) {
	if m.multi == nil || m.focus < 0 {
		return nil, false
	}
	cur := m.current()
	if cur == nil || componentCapturing(cur) {
		return nil, false
	}
	name := m.tree.Order[m.focus]
	target, ok := m.bindings[name]
	if !ok {
		return nil, false
	}
	sel := selectionFrom(cur)
	child, err := NewMulti(target, m.multi, sel, m.th)
	if err != nil {
		return app.Error(fmt.Sprintf("%s: %v", target, err)), true
	}
	return tscreen.Push(child), true
}

// selectionFrom extracts a Selection from a list or table component. The
// validator restricts on_enter sources to those two kinds, so other
// kinds return a zero Selection.
func selectionFrom(c *build.Component) build.Selection {
	switch c.Kind {
	case build.KList:
		s, _ := c.List.Selected()
		return build.Selection{String: s}
	case build.KTable:
		row, _ := c.Table.Selected()
		cells := []string(row)
		titles := make([]string, 0, len(cells))
		for _, col := range c.Table.Columns() {
			titles = append(titles, col.Title)
		}
		first := ""
		if len(cells) > 0 {
			first = cells[0]
		}
		return build.Selection{String: first, Cells: cells, Columns: titles}
	}
	return build.Selection{}
}

func updateComponent(c *build.Component, msg tea.Msg) tea.Cmd {
	switch c.Kind {
	case build.KList:
		m, cmd := c.List.Update(msg)
		*c.List = m
		return cmd
	case build.KTable:
		m, cmd := c.Table.Update(msg)
		*c.Table = m
		return cmd
	case build.KLogview:
		m, cmd := c.Logview.Update(msg)
		*c.Logview = m
		return cmd
	case build.KTree:
		m, cmd := c.Tree.Update(msg)
		*c.Tree = m
		return cmd
	case build.KInspector:
		m, cmd := c.Inspector.Update(msg)
		*c.Inspector = m
		return cmd
	}
	return nil
}
