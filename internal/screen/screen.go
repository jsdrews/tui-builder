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
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/alert"
	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/confirm"
	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/layout"
	"github.com/jsdrews/tuilib/pkg/runner"
	"github.com/jsdrews/tuilib/pkg/list"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"
	"github.com/jsdrews/tuilib/pkg/tree"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/pipeline"
)

// Multi is the shared context for a multi-screen app: every named
// screen, the top-level components map, and the unified data
// sources map. Passed once to every Model so any screen can build +
// push its on_key targets, and so pushed screens get the same
// data layer.
type Multi struct {
	Screens    map[string]*cfg.Screen
	Components map[string]*cfg.Component
	Sources    map[string]*cfg.Source
}

// sourceEntry tracks a live data source bound to one or more components.
// loaded flips true after the first successful fetch — used to suppress
// the loading spinner on polling refreshes (the data is already on
// screen; flashing to a spinner and back would just flicker).
//
// Streaming sources additionally carry a cancel func that ends the
// subscription when the screen's lifecycle calls for it (deferred — v1
// leaks streams on pop, documented in the package doc).
type sourceEntry struct {
	src    ds.Source
	loaded bool
	stream <-chan ds.Event    // non-nil for streaming sources after Subscribe
	cancel context.CancelFunc // cancels the stream's context
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

// streamMsg delivers one event from a streaming source. Lines append
// to the bound logview; an Err ends the stream and surfaces in the
// statusbar. The "done" path (channel closed) maps to a streamMsg with
// done=true so the consumer Cmd knows to stop re-issuing itself.
type streamMsg struct {
	source string
	line   string
	err    error
	done   bool
}

// nonInteractiveResult is the outcome of a non-interactive action
// dispatch. Used instead of runner.Result for the no-TTY path.
type nonInteractiveResult struct {
	stdout string
	stderr string
	err    error
}

// Model is the config-driven screen. Construct with New (single-screen)
// or NewMulti (multi-screen with on_key push support) and pass as the
// root to app.New.
type Model struct {
	title    string
	th       theme.Theme
	tree     *build.Tree
	focus    int                            // index into tree.All(); -1 means no component focused
	multi    *Multi                         // nil for single-screen mode
	// bindings holds every on_key push for this screen. A single source
	// may declare multiple bindings distinguished by Key, so lookups
	// scan linearly per keystroke.
	bindings []cfg.OnKeyBinding
	actions  []cfg.Action                   // per-key subprocess bindings for this screen

	// Data-source state. sources is keyed by source name; sourceUsers
	// indexes the bound components per source so a single fetch can fan
	// out to every consumer.
	sources     map[string]*sourceEntry
	sourceUsers map[string][]string // source name -> component names
	started     bool                // the initial fetch wave has fired

	// cursorBindings lists every component whose `on_cursor:` block ties
	// its source refetch to another table's focused row. Scanned when a
	// RowFocusedMsg arrives from a driver.
	cursorBindings []cursorBinding
	// cursorState tracks the latest Selection emitted by each driver
	// table — indexed by driver component name. Populated by tagged
	// RowFocusedMsgs. Used by lifecycle events (screen re-enter) to
	// re-drive dependent fetches without needing another cursor
	// movement to prime the pump.
	cursorState map[string]build.Selection
	// cursorCaches wraps each cursor-driven source's per-params LRU.
	// Indexed by target source name (which is unique per binding since
	// the same source shouldn't be cursor-bound twice). Lookups here
	// dedup rapid cursor sweeps that would otherwise hammer the
	// upstream — the caching layer built in feature G, reused.
	cursorCaches map[string]*ds.ParamCache
	// cursorSourceDefs holds the *cfg.Source template for each cursor-
	// bound target. Runtime BindParams clones this per fetch so we can
	// keep swapping in different param tuples without disturbing the
	// canonical config.
	cursorSourceDefs map[string]*cfg.Source

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

	// Form-modal state. Shown when an action declares `prompts:` —
	// each prompt becomes a field, on submit the values feed into
	// ${prompt.*} substitution for the rest of the action's flow
	// (confirm message + run argv). pendingAction / pendingSel carry
	// the action context across the modal.
	formModal     *form.Model
	pendingAction cfg.Action
	pendingSel    build.Selection

	// inFlightStderr captures the dispatched interactive subprocess's
	// stderr so the alert can show the actual error text. Set in
	// dispatch() on the interactive path; consumed and cleared on the
	// next runner.Result. Non-interactive dispatches carry their own
	// stderr inside nonInteractiveResult instead.
	inFlightStderr *bytes.Buffer
}

// New builds a single-screen Model. Components are looked up by name in
// the layout tree. SubstituteScreen with an empty selection applies
// ${env.*} substitution to URLs / headers / argv / etc. — without this
// step, env tokens in single-screen configs would be passed through
// literally (multi-screen already substitutes via NewMulti).
//
// params is nil here — there's no push site context in single-screen
// mode. Parameterized sources will be constructed with unresolved
// ${params.*} templates and will surface errors at fetch time. Use
// wrangl --param for now, or have your source declare defaults.
func New(s *cfg.Screen, components map[string]*cfg.Component, entries map[string]*cfg.Source, th theme.Theme) (*Model, error) {
	subS, subC, subE, err := build.SubstituteScreen(s, components, entries, build.Selection{}, nil)
	if err != nil {
		return nil, err
	}
	return build_(subS, subC, subE, th, nil)
}

// NewMulti builds a Model from one screen in a multi-screen Config. The
// Multi context lets enter on a bound list/table push another screen via
// the screen.Stack. The selection captured at push time substitutes
// ${selection} tokens in the pushed screen's config — and in any
// data-source URL/headers/body referenced from that screen.
//
// params carries the push-site `bind:` block's resolved values for the
// destination screen's parameterized sources. Pass nil on the initial
// multi-screen construction (no push has fired yet) and on screens
// whose on_key has no Bind: map. Missing required params surface as
// a build error so tryPush can pop an alert instead of constructing
// a half-broken screen.
func NewMulti(screenName string, multi *Multi, sel build.Selection, params map[string]string, th theme.Theme) (*Model, error) {
	src, ok := multi.Screens[screenName]
	if !ok {
		return nil, fmt.Errorf("screen %q not defined", screenName)
	}
	subScreen, subComponents, subEntries, err := build.SubstituteScreen(src, multi.Components, multi.Sources, sel, params)
	if err != nil {
		return nil, err
	}
	return build_(subScreen, subComponents, subEntries, th, multi)
}

func build_(s *cfg.Screen, components map[string]*cfg.Component, entries map[string]*cfg.Source, th theme.Theme, multi *Multi) (*Model, error) {
	tree, err := build.Build(&s.Layout, components, th)
	if err != nil {
		return nil, err
	}
	m := &Model{title: s.Title, th: th, tree: tree, focus: -1, multi: multi}
	if len(tree.All()) > 0 {
		m.focus = 0
	}
	if multi != nil {
		m.bindings = append([]cfg.OnKeyBinding(nil), s.OnKey...)
	}
	m.actions = append([]cfg.Action(nil), s.Actions...)

	// One unified registry over the entries map. Sources and operator
	// pipelines share the namespace — reg.Get resolves both.
	reg, err := pipeline.Build(nil, entries, nil)
	if err != nil {
		return nil, err
	}
	m.sources = map[string]*sourceEntry{}
	m.sourceUsers = map[string][]string{}
	for _, name := range tree.Order {
		c := tree.Components[name]
		// Source references any entry in the unified registry (leaf
		// source or operator pipeline). reg.Get resolves both kinds.
		boundName := c.Cfg.Source
		if boundName == "" {
			continue
		}
		bound := reg.Get(boundName)
		if bound == nil {
			return nil, fmt.Errorf("component %q: source %q not defined", name, boundName)
		}
		if _, ok := m.sources[boundName]; !ok {
			m.sources[boundName] = &sourceEntry{src: bound}
		}
		m.sourceUsers[boundName] = append(m.sourceUsers[boundName], name)
	}
	if err := m.initCursor(components, entries); err != nil {
		return nil, err
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
// On the first activation it kicks off one fetch per bound data source
// — and for sources that implement ds.StreamingSource, opens the
// subscription and starts pumping events into the bound logview.
// Subsequent activations (pop-back) reuse cached data and let polling
// handle refreshes.
func (m *Model) OnEnter(any) tea.Cmd {
	if m.started || len(m.sources) == 0 {
		return nil
	}
	m.started = true
	var cmds []tea.Cmd
	for name, entry := range m.sources {
		// Cursor-driven sources fetch only when their driver has a
		// focused row — kicking a fetch on OnEnter would try to hit
		// the upstream with empty template substitutions. Skip; the
		// first RowFocusedMsg from the driver (which tuilib fires as
		// part of initial view) primes the pump.
		if _, ok := m.cursorCaches[name]; ok {
			continue
		}
		// Streaming sources take a different path: subscribe once and
		// pump events into the logview as they arrive. Fetch still
		// fires alongside so the bound logview can render an empty
		// initial state via the standard apply path.
		if streamer, ok := entry.src.(ds.StreamingSource); ok {
			ctx, cancel := context.WithCancel(context.Background())
			ch, err := streamer.Subscribe(ctx)
			if err == nil {
				entry.stream = ch
				entry.cancel = cancel
				cmds = append(cmds, m.startFetch(name), nextStreamMsg(name, ch))
				continue
			}
			cancel()
			// ErrNotStreaming is the source's way of saying "I can't
			// stream under this config — use my Fetch path instead."
			// Fall through to the polling branch below. Real errors
			// (dial failures etc.) get the alert-or-statusbar treatment.
			if !errors.Is(err, ds.ErrNotStreaming) {
				cmds = append(cmds, app.Error(fmt.Sprintf("%s: %v", name, err)))
				continue
			}
		}
		if cmd := m.startFetch(name); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// nextStreamMsg returns the Cmd that reads one event from a streaming
// source's channel and turns it into a streamMsg. The Update handler
// re-issues this Cmd after each non-terminal event so the pump keeps
// draining without blocking the model.
func nextStreamMsg(source string, ch <-chan ds.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamMsg{source: source, done: true}
		}
		return streamMsg{source: source, line: ev.Line, err: ev.Err}
	}
}

// Layout returns the live layout.Node tree built from the YAML config.
// When a modal is active it overlays a centered dialog on top of the
// body via ZStack — base still renders behind so the user sees what
// they're acting on. Precedence (only one can be up at a time in
// practice): alert > confirm > form.
func (m *Model) Layout() layout.Node {
	body := m.tree.RenderNode()
	switch {
	case m.alertModal != nil:
		// Autosize is on (see newAlertModal) so the alert measures its
		// own content and picks a centered rect within the outer bounds
		// — no fixed-size Center wrapper. tuilib caps at 80%×60% and
		// scrolls internally past that.
		return layout.ZStack(body, layout.Sized(m.alertModal))
	case m.confirmModal != nil:
		return layout.ZStack(body, layout.Center(60, 7, layout.Sized(m.confirmModal)))
	case m.formModal != nil:
		// Height scales with the field count: 3 rows per field (input
		// is bordered) + 3 for title + submit button + breathing room.
		h := 3 + 3*len(m.pendingAction.Prompts)
		if h < 9 {
			h = 9
		}
		return layout.ZStack(body, layout.Center(60, h, layout.Sized(m.formModal)))
	}
	return body
}

// Update routes KeyMsgs to the focused component (with tab/shift+tab
// intercepted for focus cycling and enter intercepted when the focused
// component has an on_key binding). Non-key messages fan out so
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
	// Form modal — opened when an action declares prompts. On submit,
	// pull values out and proceed to confirm/dispatch via
	// actionAfterPrompts. On cancel, abort the action entirely.
	if m.formModal != nil {
		switch x := msg.(type) {
		case form.SubmittedMsg:
			prompts := stringifyFormValues(x.Values)
			action := m.pendingAction
			sel := m.pendingSel
			m.formModal = nil
			m.pendingAction = cfg.Action{}
			m.pendingSel = build.Selection{}
			return m, m.actionAfterPrompts(action, sel, prompts)
		case form.CancelledMsg:
			m.formModal = nil
			m.pendingAction = cfg.Action{}
			m.pendingSel = build.Selection{}
			return m, nil
		}
		next, cmd := m.formModal.Update(msg)
		m.formModal = &next
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
			if cmd, handled := m.tryPush("enter"); handled {
				return m, cmd
			}
		default:
			// on_key bindings fire before actions
			// so a screen author can bind `d → describe` (push) without
			// colliding with an action on the same key (which would also
			// have matched). Uniqueness is enforced at validate time on
			// the push side; collision with an action is still possible
			// and picks push-first (deliberate — pushes are lower risk
			// than firing a subprocess).
			if cmd, handled := m.tryPush(k.String()); handled {
				return m, cmd
			}
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
			return m, tagCursorFocused(updateComponent(cur, msg), m.tree.Order[m.focus])
		}
		return m, nil
	}

	switch x := msg.(type) {
	case fetchMsg:
		return m, m.handleFetch(x)
	case tickMsg:
		return m, m.startFetch(x.source)
	case streamMsg:
		return m, m.handleStream(x)
	case taggedCursorMsg:
		return m, m.handleCursorChange(x)
	case cursorFetchMsg:
		return m, m.applyCursorFetch(x)
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
	for i, c := range m.tree.All() {
		if cmd := updateComponent(c, msg); cmd != nil {
			cmds = append(cmds, tagCursorFocused(cmd, m.tree.Order[i]))
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
	// Sources may legitimately return (data, err) at the same time —
	// notably merge with on_error:skip surfaces partial failures via a
	// non-nil err while still handing back the surviving union. Apply
	// whatever data we got, then surface the error (if any) so the
	// user sees both halves of the story.
	for _, compName := range m.sourceUsers[msg.source] {
		c := m.tree.Components[compName]
		if !entry.loaded {
			setLoading(c, false)
		}
		if msg.data != nil {
			build.ApplyData(c, msg.data, m.th)
		}
	}
	// Track whether this fetch is the FIRST attempt (entry.loaded was
	// false going in). The "still false after this fetch" case is the
	// "I just opened the screen and nothing works" path — surface
	// errors in an alert modal there, since the empty table gives no
	// visual clue why. Later transient failures during polling stay in
	// the statusbar (we don't want a modal popping every 5s if a
	// cluster briefly disconnects).
	firstFetch := !entry.loaded
	if msg.data != nil {
		entry.loaded = true
	}
	if msg.err != nil {
		if firstFetch && msg.data == nil && m.alertModal == nil {
			a := m.newAlertModal(
				fmt.Sprintf("%s: initial fetch failed", msg.source),
				msg.err.Error(),
			)
			m.alertModal = &a
		} else {
			cmds = append(cmds, app.Error(fmt.Sprintf("%s: %v", msg.source, msg.err)))
		}
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
	case build.KTextview:
		return c.Textview.SetLoading(on)
	}
	return nil
}

// IsCapturingKeys reflects whether the *focused* component is engaging
// keystrokes (filter typing). Also true while any modal is showing so
// q/t/esc-pop are routed to the modal, not the app shell.
func (m *Model) IsCapturingKeys() bool {
	if m.alertModal != nil || m.confirmModal != nil || m.formModal != nil {
		return true
	}
	c := m.current()
	if c == nil {
		return false
	}
	return componentCapturing(c)
}

// Help returns the bindings the focused component currently exposes,
// plus one entry per on_key push binding for the focused component,
// plus any action keys bound to the focused source.
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
		for _, b := range m.bindings {
			if b.Source != name {
				continue
			}
			glyph := b.Key
			if b.Key == "enter" {
				glyph = "⏎"
			}
			label := b.Label
			if label == "" {
				label = "open"
			}
			out = append(out, key.NewBinding(key.WithKeys(b.Key), key.WithHelp(glyph, label)))
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
	case build.KTextview:
		c.Textview.SetFocused(on)
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
	case build.KTextview:
		return c.Textview.Searching()
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
	case build.KTextview:
		return c.Textview.Help()
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
		// Three possible paths, top-down:
		//   1. prompts: → form modal collects values, then continues
		//   2. confirm: → confirm modal (with substituted message)
		//   3. dispatch
		// We capture the action and selection into pendingAction so
		// the form's onSubmit can resume the flow with the same data.
		if len(a.Prompts) > 0 {
			form := m.newPromptForm(a.Prompts)
			m.formModal = &form
			m.pendingAction = a
			m.pendingSel = sel
			return form.Init(), true
		}
		return m.actionAfterPrompts(a, sel, nil), true
	}
	return nil, false
}

// actionAfterPrompts is the second leg of action dispatch — runs after
// any prompts have been collected (or immediately, if there were no
// prompts). Performs final substitution with the prompt values and
// either pops the confirm modal or dispatches directly.
func (m *Model) actionAfterPrompts(a cfg.Action, sel build.Selection, prompts map[string]string) tea.Cmd {
	argv := build.SubstituteAll(a.Run, sel, prompts)
	if len(argv) == 0 {
		return nil
	}
	if a.Confirm != "" {
		msg := build.SubstituteAll([]string{a.Confirm}, sel, prompts)[0]
		modal := m.newConfirmModal(a.Label, msg)
		m.confirmModal = &modal
		m.pendingArgv = argv
		m.pendingNotice = build.SubstituteAll([]string{a.Notice}, sel, prompts)[0]
		m.pendingInteractive = a.InteractiveDefault()
		return nil
	}
	notice := build.SubstituteAll([]string{a.Notice}, sel, prompts)[0]
	return m.dispatch(argv, notice, a.InteractiveDefault())
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

// newPromptForm builds the form modal for an action's prompts. Maps
// each cfg.Prompt to the matching tuilib form.Field constructor.
func (m *Model) newPromptForm(prompts []cfg.Prompt) form.Model {
	fields := make([]form.Field, len(prompts))
	for i, p := range prompts {
		switch p.Type {
		case "select":
			fields[i] = form.Select(form.SelectOptions{
				Key:     p.Key,
				Label:   labelOr(p.Label, p.Key),
				Options: append([]string(nil), p.Options...),
				Initial: p.InitialIdx,
			})
		case "confirm":
			fields[i] = form.Confirm(form.ConfirmOptions{
				Key:     p.Key,
				Label:   labelOr(p.Label, p.Key),
				Initial: p.InitialBool,
			})
		default: // text
			fields[i] = form.Text(form.TextOptions{
				Key:         p.Key,
				Label:       labelOr(p.Label, p.Key),
				Placeholder: p.Placeholder,
				Initial:     p.Initial,
			})
		}
	}
	opts := m.th.Form().With(fields)
	opts.SubmitText = "Run"
	return form.New(opts)
}

func labelOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// stringifyFormValues coerces form.SubmittedMsg.Values (any-typed) into
// the string map ${prompt.*} substitution wants. Booleans become
// "true"/"false"; everything else uses fmt.Sprint.
func stringifyFormValues(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch x := v.(type) {
		case string:
			out[k] = x
		case bool:
			if x {
				out[k] = "true"
			} else {
				out[k] = "false"
			}
		default:
			out[k] = fmt.Sprint(v)
		}
	}
	return out
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

// newAlertModal builds an error-tinted alert dialog. Autosize is on so
// the modal caps at 80%×60% of the screen (per tuilib) and word-wraps
// the full message with internal scroll — kubectl-describe-shape errors
// no longer clip to five lines. tuilib's convention is to override
// ActiveColor with the theme's ErrorBG to get the red-edge "something
// went wrong" look. Layout() pairs this with layout.Sized(...) instead
// of layout.Center(w, h, ...) since the alert measures itself.
func (m *Model) newAlertModal(title, message string) alert.Model {
	opts := m.th.Alert()
	opts.Title = title
	opts.Message = message
	opts.OK = "OK"
	opts.ActiveColor = m.th.ErrorBG
	opts.Autosize = true
	return alert.New(opts)
}

// tryPush handles a key that the focused component has an on_key
// binding for. `press` is the key string as produced by tea.KeyMsg.String()
// — "enter" for the Enter key, "d" / "l" / "ctrl+r" / etc. for arbitrary
// bindings. Returns (cmd, true) when a matching binding fires; (nil, false)
// to fall through to actions or normal component forwarding.
func (m *Model) tryPush(press string) (tea.Cmd, bool) {
	if m.multi == nil || m.focus < 0 {
		return nil, false
	}
	cur := m.current()
	if cur == nil || componentCapturing(cur) {
		return nil, false
	}
	name := m.tree.Order[m.focus]
	var binding *cfg.OnKeyBinding
	for i := range m.bindings {
		b := &m.bindings[i]
		if b.Source == name && b.Key == press {
			binding = b
			break
		}
	}
	if binding == nil {
		return nil, false
	}
	sel := selectionFrom(cur)
	// Resolve the push-site bind: block against the focused row's
	// selection. We always pass a non-nil map (possibly empty) — the
	// push IS a binding context, even if the user forgot to declare
	// bind:. That lets applyBindParams enforce required params with a
	// clean "missing required" error instead of silently constructing
	// a screen that 404s on first fetch.
	params := make(map[string]string, len(binding.Bind))
	for k, v := range binding.Bind {
		params[k] = build.Substitute(v, sel)
	}
	child, err := NewMulti(binding.Push, m.multi, sel, params, m.th)
	if err != nil {
		return app.Error(fmt.Sprintf("%s: %v", binding.Push, err)), true
	}
	return tscreen.Push(child), true
}

// selectionFrom extracts a Selection from a list or table component. The
// validator restricts on_key sources / action sources to lists and
// tables, so other kinds return a zero Selection.
//
// Items and cells are ANSI-stripped before being captured so that
// color_rules wrapping (which lives in the displayed text) does not
// leak into ${selection.*} substitutions that downstream URLs / headers
// / titles depend on. Selection carries the logical value; the visible
// styling stays in the component's view.
func selectionFrom(c *build.Component) build.Selection {
	switch c.Kind {
	case build.KList:
		s, _ := c.List.Selected()
		return build.Selection{String: xansi.Strip(s)}
	case build.KTable:
		row, _ := c.Table.Selected()
		cells := make([]string, len(row))
		for i, raw := range row {
			cells[i] = xansi.Strip(raw)
		}
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

// handleStream applies one streaming event. Lines are appended to every
// logview bound to the source; errors surface in the statusbar and
// terminate the pump. On normal channel closure (done=true) the pump
// stops without an error message — the screen will resubscribe on a
// manual `r` refresh.
func (m *Model) handleStream(x streamMsg) tea.Cmd {
	entry, ok := m.sources[x.source]
	if !ok {
		return nil
	}
	if x.err != nil {
		return app.Error(fmt.Sprintf("%s: %v", x.source, x.err))
	}
	if x.done {
		// Stream ended cleanly. Drop the channel so a manual refresh
		// can re-subscribe; cancel the context so any lingering
		// goroutines in the source can exit.
		if entry.cancel != nil {
			entry.cancel()
			entry.cancel = nil
		}
		entry.stream = nil
		return nil
	}
	// Apply: dispatch per component kind. Logview = append the raw
	// line. Table = JSON-parse + project to a row via column paths
	// (handled in build.ApplyStreamLine; non-JSON / all-empty lines
	// are skipped there). Other kinds ignore streaming events.
	for _, compName := range m.sourceUsers[x.source] {
		c := m.tree.Components[compName]
		switch c.Kind {
		case build.KLogview:
			c.Logview.Append(x.line)
		case build.KTable:
			build.ApplyStreamLine(c, x.line, m.th)
		}
	}
	// Re-issue the pump so the next event keeps draining the channel.
	return nextStreamMsg(x.source, entry.stream)
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
	case build.KTextview:
		m, cmd := c.Textview.Update(msg)
		*c.Textview = m
		return cmd
	}
	return nil
}

// cursorBinding is the resolved form of a component's `on_cursor:`
// declaration. Compared against every driver's tagged RowFocusedMsg
// to decide which dependent source should refetch.
type cursorBinding struct {
	driver string            // driver component name (a table)
	target string            // dependent component name
	source string            // dependent source's registry name
	bind   map[string]string // param name -> template ("${cursor.Name}", etc.)
}

// taggedCursorMsg attaches a driver component name to a
// pre-normalized Selection built from whichever tuilib focus-change
// message the driver emitted (table.RowFocusedMsg,
// list.SelectedChangedMsg, or tree.SelectedChangedMsg). The fan-out
// path wraps each component's Cmd, translates the message, and
// re-emits this so screen.Update can route it uniformly.
type taggedCursorMsg struct {
	driver string
	empty  bool
	sel    build.Selection
}

// cursorFetchMsg carries a cursor-driven fetch result back to
// Update. Distinct from the polled fetchMsg because it applies to a
// specific target (one component), not all sourceUsers of the source.
type cursorFetchMsg struct {
	target string
	data   any
	err    error
}

// tagCursorFocused wraps a component's returned Cmd so any tuilib
// focus-change message the cmd produces is normalised into a
// taggedCursorMsg carrying the emitting component's name plus a
// pre-built build.Selection. Non-matching messages pass through
// untouched. Called from the broadcast fan-out — every component's
// Update return value goes through this — so we never lose track of
// which driver's cursor moved. Selection shape per driver kind:
//
//   - table.RowFocusedMsg  → String = first cell, Cells = row cells,
//                            Columns = column titles
//   - list.SelectedChangedMsg → String = item, Cells = [item],
//                            Columns = ["item"] (so ${cursor.item}
//                            reads naturally alongside bare ${cursor})
//   - tree.SelectedChangedMsg → String = label, Cells = path,
//                            Columns = nil (numeric ${cursor.N} indexes
//                            into path; ${cursor.depth} is special-cased
//                            in the resolver; bare ${cursor} = label)
func tagCursorFocused(cmd tea.Cmd, name string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		switch x := msg.(type) {
		case table.RowFocusedMsg:
			if x.Empty {
				return taggedCursorMsg{driver: name, empty: true}
			}
			first := ""
			if len(x.Cells) > 0 {
				first = x.Cells[0]
			}
			return taggedCursorMsg{
				driver: name,
				sel: build.Selection{
					String:  first,
					Cells:   append([]string(nil), x.Cells...),
					Columns: append([]string(nil), x.Columns...),
				},
			}
		case list.SelectedChangedMsg:
			if x.Empty {
				return taggedCursorMsg{driver: name, empty: true}
			}
			return taggedCursorMsg{
				driver: name,
				sel: build.Selection{
					String:  x.Item,
					Cells:   []string{x.Item},
					Columns: []string{"item"},
				},
			}
		case tree.SelectedChangedMsg:
			if x.Empty {
				return taggedCursorMsg{driver: name, empty: true}
			}
			return taggedCursorMsg{
				driver: name,
				sel: build.Selection{
					String: x.Label,
					Cells:  append([]string(nil), x.Path...),
				},
			}
		}
		return msg
	}
}

// initCursor collects every OnCursor binding on this screen and
// prepares its per-source ParamCache. Called from New / NewMulti
// after the tree + source registry are set up. Uses the same
// caching primitive built for join lookups (feature G) so cursor
// sweeps don't hammer parameterized sources.
func (m *Model) initCursor(components map[string]*cfg.Component, sources map[string]*cfg.Source) error {
	m.cursorState = map[string]build.Selection{}
	m.cursorCaches = map[string]*ds.ParamCache{}
	m.cursorSourceDefs = map[string]*cfg.Source{}
	for _, name := range m.tree.Order {
		comp := components[name]
		if comp == nil || comp.OnCursor == nil {
			continue
		}
		srcName := comp.Source
		srcDef := sources[srcName]
		if srcDef == nil {
			return fmt.Errorf("on_cursor target %q references undefined source %q", name, srcName)
		}
		spec := srcDef.Cache
		if spec == nil {
			// Match the join operator's lookupCacheDefaults so
			// cursor-driven and join-lookup call sites converge on the
			// same "reasonable default" story.
			spec = &cfg.CacheSpec{TTL: "60s", Size: ds.ParamCacheDefaultSize}
		}
		cache, err := ds.NewParamCache(spec)
		if err != nil {
			return fmt.Errorf("on_cursor cache for %q: %w", name, err)
		}
		m.cursorCaches[srcName] = cache
		m.cursorSourceDefs[srcName] = srcDef
		m.cursorBindings = append(m.cursorBindings, cursorBinding{
			driver: comp.OnCursor.Source,
			target: name,
			source: srcName,
			bind:   comp.OnCursor.Bind,
		})
	}
	return nil
}

// handleCursorChange updates cursor state for the emitting driver
// and dispatches a cursor-driven fetch on every binding that watches
// it. Empty focus (transition to no visible row / item / node) clears
// the state so downstream templates resolve to "" instead of stale
// values. The Selection is already normalised — tagCursorFocused
// handles the per-driver-kind translation.
func (m *Model) handleCursorChange(t taggedCursorMsg) tea.Cmd {
	if t.empty {
		m.cursorState[t.driver] = build.Selection{}
	} else {
		m.cursorState[t.driver] = t.sel
	}
	var cmds []tea.Cmd
	for _, b := range m.cursorBindings {
		if b.driver != t.driver {
			continue
		}
		if cmd := m.startCursorFetch(b); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// startCursorFetch computes the current param tuple by substituting
// the driver's cursor Selection into the binding's Bind templates,
// then consults the shared ParamCache. Hit → apply cached value to
// the target immediately (no goroutine hop). Miss → return a Cmd
// that clones the source cfg, binds params, builds a fresh leaf,
// fetches, stores. Cache single-flight collapses rapid re-triggers.
func (m *Model) startCursorFetch(b cursorBinding) tea.Cmd {
	sel := m.cursorState[b.driver]
	params := make(map[string]string, len(b.bind))
	for k, tmpl := range b.bind {
		params[k] = build.SubstituteCursor(tmpl, sel)
	}
	cache := m.cursorCaches[b.source]
	srcDef := m.cursorSourceDefs[b.source]
	target := b.target
	return func() tea.Msg {
		data, err := cache.FetchOrLoad(params, func() (any, error) {
			cloned := srcDef.Clone()
			if err := cloned.BindParams(params); err != nil {
				return nil, fmt.Errorf("bind: %w", err)
			}
			src, err := ds.BuildLeaf(cloned, nil)
			if err != nil {
				return nil, fmt.Errorf("build: %w", err)
			}
			return src.Fetch(context.Background())
		})
		return cursorFetchMsg{target: target, data: data, err: err}
	}
}

// applyCursorFetch routes a cursor-driven fetch result to its single
// target component. Errors surface in the statusbar via app.Error;
// we don't pop an alert modal here because cursor moves are
// high-frequency and a modal per failed hover would be miserable.
func (m *Model) applyCursorFetch(x cursorFetchMsg) tea.Cmd {
	c := m.tree.Components[x.target]
	if c == nil {
		return nil
	}
	if x.err != nil {
		return app.Error(fmt.Sprintf("cursor fetch %s: %v", x.target, x.err))
	}
	if x.data != nil {
		build.ApplyData(c, x.data, m.th)
	}
	return nil
}
