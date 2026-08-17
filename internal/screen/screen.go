// Package screen wraps a built layout tree as a tuilib screen.Screen.
// One component holds keyboard focus at a time: tab / shift+tab cycle
// through the components in walk order. Only the focused component
// receives KeyMsgs; non-key messages fan out so spinner ticks reach every
// pane. IsCapturingKeys reflects only the focused component, so the
// surrounding app shell's q/ctrl+c suppression is scoped to "the thing
// the user is interacting with" rather than "anything in the layout."
package screen

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/confirm"
	"github.com/jsdrews/tuilib/pkg/focus"
	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/layout"
	"github.com/jsdrews/tuilib/pkg/list"
	"github.com/jsdrews/tuilib/pkg/runner"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"
	"github.com/jsdrews/tuilib/pkg/tree"

	"github.com/jsdrews/tui-builder/internal/action"
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
	// Actions is the config-wide action registry every screen's
	// bindings resolve against. Post-hoist it holds inline declarations
	// too.
	Actions map[string]*cfg.Action
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

// Model is the config-driven screen. Construct with New (single-screen)
// or NewMulti (multi-screen with on_key push support) and pass as the
// root to app.New.
type Model struct {
	title string
	th    theme.Theme
	tree  *build.Tree
	focus int    // index into tree.All(); -1 means no component focused
	multi *Multi // nil for single-screen mode
	// bindings holds every on_key push for this screen. A single source
	// may declare multiple bindings distinguished by Key, so lookups
	// scan linearly per keystroke.
	bindings []cfg.OnKeyBinding
	// actions holds this screen's key bindings; actionDefs is the
	// config-wide registry they reference by name. Post-hoist the
	// registry holds inline declarations too, so there is exactly one
	// place to look an action up.
	actions    []cfg.ActionBinding
	actionDefs map[string]*cfg.Action

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
	// cursorAllSources is the full sources map for pipeline.Build.
	// Cursor-driven fetches route through pipeline.Build (not just
	// BuildLeaf) so both leaf-kind targets AND pipeline-operator
	// targets (filter / project / derive / sort / join / etc.) work.
	// pipeline.Build needs the whole sources map so `from:` upstreams
	// resolve; constructor calls don't touch the network, so
	// rebuilding on every cursor move is safe. The ParamCache in
	// cursorCaches absorbs the redundant construction cost for
	// repeated params.
	cursorAllSources map[string]*cfg.Source

	// Confirm-modal state. When confirmModal is non-nil it overlays the
	// body via ZStack and captures all keys until ConfirmedMsg /
	// CancelledMsg arrives. pendingResolved holds the fully-resolved
	// action to dispatch on confirmation.
	confirmModal       *confirm.Model
	pendingNotice      string
	pendingInteractive bool
	// confirmW / confirmH are the fitted outer dimensions for the confirm
	// overlay, computed in newConfirmModal from the (word-wrapped) message.
	// The confirm component doesn't wrap or self-measure, so Layout() reads
	// these instead of a hardcoded Center size — otherwise a long message
	// (e.g. "Delete pod <long-name> in <ns>? This cannot be undone.") clips.
	confirmW, confirmH int

	// Form-modal state. Shown when a fired action has inputs the call
	// site didn't bind — each becomes a field generated from its own
	// Parameter. pendingBinding / pendingInputs / pendingSel carry the
	// action context across the modal so onSubmit can resume the flow;
	// pendingFields sizes the overlay.
	formModal      *form.Model
	pendingBinding cfg.ActionBinding
	pendingInputs  action.Inputs
	pendingSel     build.Selection
	pendingFields  int

	// pendingResolved is the action awaiting a confirm answer.
	pendingResolved *action.Resolved
	// pendingRun is the resolved action behind the in-flight capture,
	// kept so runner.Captured can be turned back into a Result carrying
	// the action's own message: / error_message:.
	pendingRun *action.Resolved
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
func New(s *cfg.Screen, components map[string]*cfg.Component, entries map[string]*cfg.Source, actions map[string]*cfg.Action, th theme.Theme) (*Model, error) {
	subS, subC, subE, err := build.SubstituteScreen(s, components, entries, build.Selection{}, nil)
	if err != nil {
		return nil, err
	}
	return build_(subS, subC, subE, actions, th, nil)
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
	return build_(subScreen, subComponents, subEntries, multi.Actions, th, multi)
}

func build_(s *cfg.Screen, components map[string]*cfg.Component, entries map[string]*cfg.Source, actions map[string]*cfg.Action, th theme.Theme, multi *Multi) (*Model, error) {
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
	m.actions = append([]cfg.ActionBinding(nil), s.Actions...)
	m.actionDefs = actions

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
// practice): confirm > form.
//
// There is deliberately no error modal. Failures — action dispatch and
// initial fetch alike — go to the app-wide output console instead, which
// keeps them re-readable rather than dismissed-and-gone, and never blocks
// the UI. The statusbar's persistent unread badge is what makes that
// discoverable; see cmd/tui-builder's OutputKey.
func (m *Model) Layout() layout.Node {
	body := m.tree.RenderNode()
	switch {
	case m.confirmModal != nil:
		// Fitted size (see newConfirmModal) so long confirm messages wrap
		// and stay fully visible instead of clipping at the 60-col edge.
		return layout.ZStack(body, layout.Center(m.confirmW, m.confirmH, layout.Sized(m.confirmModal)))
	case m.formModal != nil:
		// Height scales with the field count: 3 rows per field (input
		// is bordered) + 3 for title + submit button + breathing room.
		h := 3 + 3*m.pendingFields
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
	// Form modal — opened when a fired action had inputs nobody bound.
	// On submit, merge the collected values into the inputs already
	// resolved from bind: and resume. On cancel, abort the action.
	if m.formModal != nil {
		switch x := msg.(type) {
		case form.SubmittedMsg:
			b := m.pendingBinding
			sel := m.pendingSel
			inputs := m.pendingInputs
			if inputs == nil {
				inputs = action.Inputs{}
			}
			for k, v := range stringifyFormValues(x.Values) {
				inputs[k] = v
			}
			def := m.actionDefs[b.Action]
			m.clearPendingForm()
			if def == nil {
				return m, app.Error(fmt.Sprintf("action %q is not defined", b.Action))
			}
			return m, m.actionAfterInputs(b, def, sel, inputs)
		case form.CancelledMsg:
			m.clearPendingForm()
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
			resolved := m.pendingResolved
			notice := m.pendingNotice
			interactive := m.pendingInteractive
			m.clearPendingConfirm()
			if resolved == nil {
				return m, nil
			}
			return m, m.dispatchResolved(resolved, notice, interactive)
		case confirm.CancelledMsg:
			m.clearPendingConfirm()
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
			if cmd, handled := m.activate(); handled {
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
		// Interactive handoff finished. There's no captured output —
		// the subprocess owned the terminal and printed straight to it —
		// so the exit status is the whole report.
		return m, m.actionOutcome(x.Err)
	case runner.Captured:
		// A non-interactive run finished. Its stdout/stderr already
		// streamed into the console line by line via the app shell; what
		// we add is the head line, built with the action's own message: /
		// error_message: so the summary says something better than an
		// exit code.
		r := m.pendingRun
		m.pendingRun = nil
		if r == nil {
			return m, m.actionOutcome(x.Err)
		}
		return m, reportResult(r.Finish(exitCode(x.Err), ""))
	case actionResultMsg:
		return m, reportResult(x.res)
	case list.ActivatedMsg, table.ActivatedMsg:
		// A double click is the mouse spelling of enter. The message names
		// its sender by token, so we activate the component that was
		// double-clicked rather than m.focus — the focus request the same
		// click emitted rides in the same batch, and batched cmds have no
		// ordering guarantee.
		for i, c := range m.tree.All() {
			if !componentActivated(c, msg) {
				continue
			}
			if i != m.focus {
				m.focus = i
				m.applyFocus()
			}
			if cmd, handled := m.activate(); handled {
				return m, cmd
			}
			break
		}
		return m, nil
	case focus.RequestMsg:
		// A component emits this when a click lands inside its rect. Without
		// honouring it the clicked pane moves its own cursor while the
		// keyboard keeps driving the previously focused one — two panes look
		// active at once.
		for i, c := range m.tree.All() {
			if focusRequested(c, x) {
				if i != m.focus {
					m.focus = i
					m.applyFocus()
				}
				break
			}
		}
		return m, nil
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
	// "I just opened the screen and nothing works" path — it gets the
	// full error as console detail under an explicit headline, since an
	// empty table gives no visual clue why. Later transient failures
	// during polling are one summary line: the pane still has data on
	// it, and a flapping cluster shouldn't bury the log.
	//
	// Neither blocks. Both land in the output console, where the
	// statusbar's unread badge keeps them findable after the summary
	// line has been wiped by the next keypress.
	firstFetch := !entry.loaded
	if msg.data != nil {
		entry.loaded = true
	}
	if msg.err != nil {
		if firstFetch && msg.data == nil {
			cmds = append(cmds, app.ErrorDetail(
				fmt.Sprintf("%s: initial fetch failed", msg.source),
				msg.err.Error(),
			))
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
	// After ApplyData: fan a wake msg to every component so any
	// pending tuilib focus emit (RowFocusedMsg from SetRows,
	// SelectedChangedMsg from SetItems / SetRoot) actually flushes.
	// Those emits only fire from tuilib Update; the intercepted
	// fetchMsg path never triggers Update on the bound components on
	// its own, which is why cursor-driven detail panes wouldn't
	// populate until the user pressed something. postApplyMsg falls
	// through to the fanout at the bottom of Update, hits every
	// component's Update, and any pending flushMsgs fire.
	if msg.data != nil {
		cmds = append(cmds, func() tea.Msg { return postApplyMsg{} })
	}
	return tea.Batch(cmds...)
}

// postApplyMsg fans out to every component's Update so tuilib focus
// emits (RowFocusedMsg, SelectedChangedMsg) that were queued during
// SetRows / SetRoot / SetItems actually flush. See handleFetch for
// why the intercepted fetchMsg alone can't trigger those flushes.
type postApplyMsg struct{}

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
	if m.confirmModal != nil || m.formModal != nil {
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
	// A binding with no `from:` isn't scoped to a pane, so it belongs in
	// the strip whichever component holds focus.
	for _, b := range m.actions {
		if b.From != "" && b.From != name {
			continue
		}
		out = append(out, key.NewBinding(key.WithKeys(b.Key), key.WithHelp(b.Key, labelOr(b.Label, b.Action))))
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

// focusableOf returns the component's tuilib focus handle, or nil for
// kinds that take no focus.
func focusableOf(c *build.Component) focus.Focusable {
	switch c.Kind {
	case build.KList:
		return c.List
	case build.KTable:
		return c.Table
	case build.KLogview:
		return c.Logview
	case build.KTree:
		return c.Tree
	case build.KInspector:
		return c.Inspector
	case build.KTextview:
		return c.Textview
	}
	return nil
}

func setFocused(c *build.Component, on bool) {
	f := focusableOf(c)
	if f == nil {
		return
	}
	// Focus returns a cursor-blink cmd for components that have one; none
	// of the kinds above do, so there is nothing to propagate.
	if on {
		f.Focus()
		return
	}
	f.Blur()
}

// focusRequested reports whether req names c. A clicked component asks for
// focus by token (it can't name its own address — see focus.Token); a
// caller holding the component names it by address.
//
// This mirrors focus.Group's matching. We can't use a Group directly: this
// screen's focus index also drives on_cursor tagging, action dispatch, and
// help text, so the index stays the source of truth and requests are
// translated into it.
func focusRequested(c *build.Component, req focus.RequestMsg) bool {
	f := focusableOf(c)
	if f == nil {
		return false
	}
	if req.Target != nil && f == req.Target {
		return true
	}
	if req.Token != nil {
		if id, ok := f.(focus.Identified); ok && id.FocusToken() == req.Token {
			return true
		}
	}
	return false
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

// tryAction handles a KeyMsg that may match one of this screen's action
// bindings. Returns (cmd, true) when the action fired so the caller can
// short-circuit; otherwise (nil, false) to fall through to component
// routing. Suppressed while a component is capturing keys so action keys
// don't hijack filter typing.
//
// A binding with `from:` only fires while that pane holds focus — that's
// what lets the same key mean different things in different panes. A
// binding without one needs no selection and fires anywhere on the
// screen, which is how an action that takes no row input works.
func (m *Model) tryAction(k tea.KeyMsg) (tea.Cmd, bool) {
	if len(m.actions) == 0 {
		return nil, false
	}
	cur := m.current()
	if cur != nil && componentCapturing(cur) {
		return nil, false
	}
	focusedName := ""
	if m.focus >= 0 {
		focusedName = m.tree.Order[m.focus]
	}
	keyStr := k.String()
	for _, b := range m.actions {
		if b.Key != keyStr {
			continue
		}
		if b.From != "" && b.From != focusedName {
			continue
		}
		def := m.actionDefs[b.Action]
		if def == nil {
			// Unreachable via Load (the validator resolves every
			// reference), so this only fires for a hand-built Model in a
			// test. Report rather than panic.
			return app.Error(fmt.Sprintf("action %q is not defined", b.Action)), true
		}

		// The selection feeding bind: comes from the pane named by
		// `from:`, which is the focused one — a binding that reads a
		// selection can't fire from anywhere else.
		sel := build.Selection{}
		if b.From != "" && cur != nil {
			sel = selectionFrom(cur)
		}
		inputs := resolveBinds(b, def, sel)

		// Anything the call site didn't bind gets collected from the
		// user. The form is generated from the action's own declared
		// inputs — there is no separate prompts: schema to keep in sync.
		if missing := unboundInputs(def, inputs); len(missing) > 0 {
			f := m.newInputForm(def, missing)
			m.formModal = &f
			m.pendingBinding = b
			m.pendingInputs = inputs
			m.pendingSel = sel
			m.pendingFields = len(missing)
			return f.Init(), true
		}
		return m.actionAfterInputs(b, def, sel, inputs), true
	}
	return nil, false
}

// actionAfterInputs is the second leg of dispatch — runs once every
// input has a value, whether from bind: or from the generated form.
// Resolves the action into something concrete, then either pops the
// confirm modal or dispatches.
func (m *Model) actionAfterInputs(b cfg.ActionBinding, def *cfg.Action, sel build.Selection, inputs action.Inputs) tea.Cmd {
	resolved, err := action.Resolve(def, inputs)
	if err != nil {
		return app.Error(fmt.Sprintf("%s: %v", b.Action, err))
	}
	// Preview text substitutes the RESOLVED values, not the raw bind map:
	// defaults are applied inside Resolve, so an input with a default
	// would otherwise render empty in the confirm while the argv got the
	// real value — a confirm that disagrees with what runs.
	vals := resolved.Values()
	notice := build.SubstituteAll([]string{b.Notice}, sel, vals)[0]

	if b.Confirm != "" {
		msg := build.SubstituteAll([]string{b.Confirm}, sel, vals)[0]
		modal := m.newConfirmModal(labelOr(b.Label, b.Action), msg)
		m.confirmModal = &modal
		m.pendingResolved = resolved
		m.pendingNotice = notice
		m.pendingInteractive = b.IsInteractive()
		return nil
	}
	return m.dispatchResolved(resolved, notice, b.IsInteractive())
}

// dispatchResolved routes a resolved action to the right runner. Exec
// goes through the process paths in dispatch(); http runs here, since
// there is no terminal involved and nothing to stream.
func (m *Model) dispatchResolved(r *action.Resolved, notice string, interactive bool) tea.Cmd {
	if r.Kind == "http" {
		// Tracked so the summary can be built with the action's
		// message: / error_message: when the response lands.
		return func() tea.Msg {
			return actionResultMsg{res: r.Do(context.Background())}
		}
	}
	m.trackRun(r)
	return m.dispatch(r.Argv, notice, interactive)
}

// trackRun remembers the resolved action behind the next capture so
// runner.Captured can be turned back into a Result with the action's own
// message: / error_message: applied.
//
// Keyed by nothing: runner assigns the RunID inside the Cmd, after we
// return. Since a keypress can only start one action and Captured
// arrives before any realistic second keypress completes, the pending
// slot is sufficient and avoids threading an ID we don't have yet.
func (m *Model) trackRun(r *action.Resolved) {
	m.pendingRun = r
}

// actionResultMsg carries a normalised action Result back into Update.
type actionResultMsg struct{ res action.Result }

// reportResult sends a Result to the app-wide console. The summary
// paints the statusbar and heads the console entry; the body carries
// whatever wouldn't fit in a footer.
//
// There is deliberately nothing else here — no modal, no refresh of the
// views the action touched. A view owns its own refresh cycle: one that
// wants to converge declares `refresh:`, and one that doesn't is asking
// to be told to reload. Coupling a mutation to a repaint would make
// every action responsible for knowing which panes it invalidated.
func reportResult(res action.Result) tea.Cmd {
	body := strings.TrimSpace(res.Output)
	if res.OK {
		return app.InfoDetail(res.Summary, body)
	}
	return app.ErrorDetail(res.Summary, body)
}

// exitCode pulls the process exit status out of a run error. A non-
// ExitError (the binary was missing, say) has no status of its own; 1 is
// the conventional stand-in and keeps `success:` expressions comparing
// against a number either way.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

// clearPendingConfirm drops the confirm modal and the action it was
// gating.
func (m *Model) clearPendingConfirm() {
	m.confirmModal = nil
	m.pendingResolved = nil
	m.pendingNotice = ""
	m.pendingInteractive = false
}

// clearPendingForm drops the form modal and every piece of action
// context it was carrying.
func (m *Model) clearPendingForm() {
	m.formModal = nil
	m.pendingBinding = cfg.ActionBinding{}
	m.pendingInputs = nil
	m.pendingSel = build.Selection{}
	m.pendingFields = 0
}

// resolveBinds turns a binding's bind: templates into concrete input
// values. Templates see the focused row (${selection.*}) and the
// environment (${env.*}) — the same substitution every other call site
// uses, so there's one set of rules to learn.
func resolveBinds(b cfg.ActionBinding, def *cfg.Action, sel build.Selection) action.Inputs {
	inputs := make(action.Inputs, len(b.Bind))
	for name, tmpl := range b.Bind {
		inputs[name] = build.SubstituteAll([]string{tmpl}, sel, nil)[0]
	}
	return inputs
}

// unboundInputs lists the declared inputs with no value yet, in form
// order: Order first, then alphabetically. Inputs live in a map, so
// without the sort a two-field form would render in a different order
// run to run.
//
// An input with a Default is NOT considered missing — the default is the
// answer, and prompting for something the author already decided is
// friction. Bind explicitly or drop the default if you want to be asked.
func unboundInputs(def *cfg.Action, have action.Inputs) []string {
	var missing []string
	for name, p := range def.Inputs {
		if p == nil {
			continue
		}
		if _, ok := have[name]; ok {
			continue
		}
		if p.Default != "" {
			continue
		}
		missing = append(missing, name)
	}
	sort.Slice(missing, func(i, j int) bool {
		pi, pj := def.Inputs[missing[i]], def.Inputs[missing[j]]
		if pi.Order != pj.Order {
			return pi.Order < pj.Order
		}
		return missing[i] < missing[j]
	})
	return missing
}

// dispatch routes a fully-substituted argv down one of two paths that
// have nothing in common but an *exec.Cmd. nil argv is a no-op.
//
// Interactive (pkg/runner) is a whole-program state transition, not a
// goroutine: tea.Exec releases the terminal, the subprocess owns the
// TTY outright, and the alt-screen resumes on exit. Exactly one process
// can own a TTY, so vim / kubectl exec / ssh cannot be run concurrently
// with the UI at any price. Output goes to the real terminal, so there
// is nothing for us to capture — the console gets the exit status via
// runner.Result and that is the whole story.
//
// Non-interactive is runner.CaptureWith, which streams the subprocess
// line-by-line into the app-wide output console (CaptureStarted →
// CapturedLine* → Captured). We deliberately do NOT hand-roll this with
// cmd.Run() into a bytes.Buffer: Capture uses real os.Pipes so the child
// gets an *os.File and os/exec spawns no copy goroutine for Wait to
// block on. With a buffer, any descendant outliving the process keeps
// the write end open, the copy never sees EOF, and Wait never returns —
// the action hangs forever with no way out. Capture also caps memory
// (ring buffer), preserves stdout/stderr interleaving per line, sets a
// process group, and gives the console's kill picker a handle.
func (m *Model) dispatch(argv []string, notice string, interactive bool) tea.Cmd {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if interactive {
		if notice != "" {
			return runner.RunWithNotice(cmd, notice)
		}
		return runner.Run(cmd)
	}
	return runner.CaptureWith(runner.CaptureOptions{Cmd: cmd})
}

// actionOutcome reports an interactive action's exit status. The captured
// output of non-interactive actions doesn't come through here at all —
// runner.Capture streams it straight into the console — so this only has
// an error to report, never a body.
func (m *Model) actionOutcome(err error) tea.Cmd {
	if err != nil {
		return app.Error(fmt.Sprintf("action failed: %v", err))
	}
	return app.Info("action complete")
}

// newInputForm builds the modal that collects an action's unbound
// inputs. Fields are generated from each input's own Parameter — the
// data type picks the widget (bool → toggle, anything with Options →
// select, otherwise a text input), so there is no second widget schema
// to keep in step with the type system.
func (m *Model) newInputForm(def *cfg.Action, names []string) form.Model {
	fields := make([]form.Field, len(names))
	for i, name := range names {
		p := def.Inputs[name]
		switch {
		case len(p.Options) > 0:
			fields[i] = form.Select(form.SelectOptions{
				Key:     name,
				Label:   labelOr(p.Label, name),
				Options: append([]string(nil), p.Options...),
				Initial: indexOf(p.Options, p.Default),
			})
		case p.Type == "bool":
			fields[i] = form.Confirm(form.ConfirmOptions{
				Key:     name,
				Label:   labelOr(p.Label, name),
				Initial: p.Default == "true",
			})
		default:
			// Required and Validate are the form's own enforcement:
			// submit refuses, the label gains a "*", and the offending
			// field's border tints with the reason written on it. Without
			// wiring these, `required: true` would be checked at load
			// (the binding must bind it or make it promptable) and then
			// silently pass an empty string into the argv at runtime.
			fields[i] = form.Text(form.TextOptions{
				Key:         name,
				Label:       labelOr(p.Label, name),
				Placeholder: p.Placeholder,
				Initial:     p.Default,
				Required:    p.Required,
				Validate:    validatorFor(p.Type),
			})
		}
	}
	opts := m.th.Form().With(fields)
	opts.SubmitText = "Run"
	return form.New(opts)
}

// validatorFor turns an input's declared data type into the form's
// per-field validator. Returns nil for types with nothing to check —
// the form treats a nil Validate as "always acceptable".
//
// An empty value is left to Required: this runs after it, so a blank
// optional field stays blank rather than failing a format check it was
// never obliged to satisfy.
func validatorFor(typ string) func(any) error {
	switch typ {
	case "int":
		return func(v any) error {
			s, _ := v.(string)
			if s == "" {
				return nil
			}
			if _, err := strconv.Atoi(s); err != nil {
				return fmt.Errorf("must be a whole number")
			}
			return nil
		}
	case "duration":
		return func(v any) error {
			s, _ := v.(string)
			if s == "" {
				return nil
			}
			if _, err := time.ParseDuration(s); err != nil {
				return fmt.Errorf("must be a duration, e.g. 30s or 5m")
			}
			return nil
		}
	}
	return nil
}

// indexOf finds want in opts, or 0. Used to turn a select input's
// Default (an option string) into the starting index the widget wants,
// so authors express the default the same way for every input type.
func indexOf(opts []string, want string) int {
	for i, o := range opts {
		if o == want {
			return i
		}
	}
	return 0
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

// confirmWrapWidth is the inner text width the confirm message is wrapped
// to. Paired with confirmChrome it yields a modal that fits within an
// 80-column terminal (72 + 4 border/padding cols) while word-wrapping
// anything longer instead of clipping it. Kept below the alert's 80% cap
// so the two modals look consistent on a standard terminal.
const (
	confirmWrapWidth = 72
	confirmChrome    = 4 // border (2) + one padding col each side
)

// newConfirmModal builds a yes/no confirm dialog using the active theme.
// The title is the action's label (or "Confirm" when blank). The message
// is word-wrapped to confirmWrapWidth and the overlay's fitted dimensions
// (m.confirmW / m.confirmH) are recorded for Layout() — the confirm
// component neither wraps nor self-measures, so without this a long
// message clips to a single hardcoded-width line.
func (m *Model) newConfirmModal(label, message string) confirm.Model {
	opts := m.th.Confirm()
	if label == "" {
		label = "Confirm"
	}
	wrapped := xansi.Wrap(message, confirmWrapWidth, " -")
	lines := strings.Split(wrapped, "\n")
	longest := 0
	for _, ln := range lines {
		if w := xansi.StringWidth(ln); w > longest {
			longest = w
		}
	}
	// Outer width fits the longest wrapped line; height fits the message
	// lines + blank spacer + button row, all inside the pane border. Floor
	// at the previous 60×7 so short prompts keep their familiar shape.
	m.confirmW = longest + confirmChrome
	if m.confirmW < 60 {
		m.confirmW = 60
	}
	m.confirmH = len(lines) + 4 // borders (2) + spacer (1) + buttons (1)
	if m.confirmH < 7 {
		m.confirmH = 7
	}
	opts.Title = label
	opts.Message = wrapped
	opts.Confirm = "Yes"
	opts.Cancel = "No"
	return confirm.New(opts)
}

// activate runs the "open the selection" verb against the focused
// component: an on_key binding for enter first, then an action bound to
// enter. Keyboard enter and a double click both route through here so the
// two spellings of the same verb can't drift apart.
//
// Push-before-action matches the ordering the other keys use in Update.
func (m *Model) activate() (tea.Cmd, bool) {
	if cmd, handled := m.tryPush("enter"); handled {
		return cmd, true
	}
	return m.tryAction(tea.KeyMsg{Type: tea.KeyEnter})
}

// componentActivated reports whether msg is c's own activation. Only lists
// and tables emit one, which is also all the validator allows as an on_key
// or action source.
//
// Call this only for ActivatedMsg: tuilib's IsActivate also answers true for
// a plain enter KeyMsg regardless of which component it belongs to, and the
// keyboard path already resolves that through m.focus.
func componentActivated(c *build.Component, msg tea.Msg) bool {
	switch c.Kind {
	case build.KList:
		return c.List.IsActivate(msg)
	case build.KTable:
		return c.Table.IsActivate(msg)
	}
	return false
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
//     Columns = column titles
//   - list.SelectedChangedMsg → String = item, Cells = [item],
//     Columns = ["item"] (so ${cursor.item}
//     reads naturally alongside bare ${cursor})
//   - tree.SelectedChangedMsg → String = label, Cells = path,
//     Columns = nil (numeric ${cursor.N} indexes
//     into path; ${cursor.depth} is special-cased
//     in the resolver; bare ${cursor} = label)
func tagCursorFocused(cmd tea.Cmd, name string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		return translateFocusMsg(cmd(), name)
	}
}

// translateFocusMsg maps tuilib focus emits into taggedCursorMsg and
// leaves other messages untouched. tea.BatchMsg — the shape tuilib
// components use to combine their own Cmd with flushMsgs() — is
// unpacked recursively so a focus emit buried inside a batch still
// gets tagged. Without the batch unwrap our type switch would miss
// every emit that co-flushes with a viewport tick or spinner cmd
// (which is virtually all of them).
func translateFocusMsg(msg tea.Msg, name string) tea.Msg {
	switch x := msg.(type) {
	case tea.BatchMsg:
		wrapped := make([]tea.Cmd, 0, len(x))
		for _, sub := range x {
			wrapped = append(wrapped, tagCursorFocused(sub, name))
		}
		return tea.BatchMsg(wrapped)
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

// initCursor collects every OnCursor binding on this screen and
// prepares its per-source ParamCache. Called from New / NewMulti
// after the tree + source registry are set up. Uses the same
// caching primitive built for join lookups (feature G) so cursor
// sweeps don't hammer parameterized sources.
func (m *Model) initCursor(components map[string]*cfg.Component, sources map[string]*cfg.Source) error {
	m.cursorState = map[string]build.Selection{}
	m.cursorCaches = map[string]*ds.ParamCache{}
	m.cursorSourceDefs = map[string]*cfg.Source{}
	m.cursorAllSources = sources
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
// then consults the shared ParamCache. Miss → returns a Cmd that
// rebuilds the target source via pipeline.Build with the fresh param
// tuple bound at build time, fetches, stores. Cache single-flight
// collapses rapid re-triggers.
//
// pipeline.Build is used instead of ds.BuildLeaf so pipeline-operator
// targets (filter / project / derive / sort / join / etc.) work as
// on_cursor targets, not just leaf sources. Constructors don't touch
// the network, so rebuilding the whole registry per miss is safe;
// the cost is redundant construction, which the cache absorbs for
// repeats.
func (m *Model) startCursorFetch(b cursorBinding) tea.Cmd {
	sel := m.cursorState[b.driver]
	params := make(map[string]string, len(b.bind))
	for k, tmpl := range b.bind {
		params[k] = build.SubstituteCursor(tmpl, sel)
	}
	cache := m.cursorCaches[b.source]
	sources := m.cursorAllSources
	sourceName := b.source
	target := b.target
	return func() tea.Msg {
		data, err := cache.FetchOrLoad(params, func() (any, error) {
			reg, err := pipeline.Build(nil, sources, map[string]map[string]string{
				sourceName: params,
			})
			if err != nil {
				return nil, fmt.Errorf("build: %w", err)
			}
			src := reg.Get(sourceName)
			if src == nil {
				return nil, fmt.Errorf("source %q not in registry", sourceName)
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
		// Same wake fan-out as handleFetch — see postApplyMsg's doc.
		return func() tea.Msg { return postApplyMsg{} }
	}
	return nil
}
