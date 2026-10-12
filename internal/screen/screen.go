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
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/jsdrews/tuilib/pkg/activity"
	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/confirm"
	"github.com/jsdrews/tuilib/pkg/eventlog"
	"github.com/jsdrews/tuilib/pkg/focus"
	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/geom"
	"github.com/jsdrews/tuilib/pkg/help"
	"github.com/jsdrews/tuilib/pkg/inspector"
	"github.com/jsdrews/tuilib/pkg/layout"
	"github.com/jsdrews/tuilib/pkg/list"
	"github.com/jsdrews/tuilib/pkg/mouse"
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
// push its push-action targets, and so pushed screens get the same
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
// subscription. Its context derives from the screen's, so popping the
// screen ends it too (see Model.pushed).
type sourceEntry struct {
	src    ds.Source
	loaded bool
	stream <-chan ds.Event    // non-nil while a streaming source is live
	cancel context.CancelFunc // cancels the stream's context
	// streaming is set once a source has subscribed, and stays set after
	// its stream ends, so `r` knows to reconnect it rather than refetch.
	streaming bool
	// failing is whether the newest fetch failed outright, for the
	// "· reads failing" title on a polled source's components.
	failing bool
}

// distinctUse is one windowed-table column whose filter suggestions come
// from a source.
type distinctUse struct {
	component string
	column    int
}

// fetchMsg carries the result of one source fetch back into Update.
type fetchMsg struct {
	source string
	data   any
	err    error
	// reads holds the activity read each activity component stamped when
	// the fetch was issued, by component name.
	reads map[string]activity.Read
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
// or NewMulti (multi-screen, with push actions) and pass as the
// root to app.New.
type Model struct {
	title string
	th    theme.Theme
	tree  *build.Tree
	focus int    // index into tree.All(); -1 means no component focused
	multi *Multi // nil for single-screen mode
	// actions holds this screen's action bindings; actionDefs is the
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
	// distinctUsers maps a source named by a column's `distinct:
	// {source:}` to the columns its answer feeds.
	distinctUsers map[string][]distinctUse
	started       bool // the initial fetch wave has fired

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

	// ctx is the parent of this screen's remote requests and stream
	// subscriptions; cancel ends them all. A screen can't tell it has
	// been popped, but the one under it can (its OnEnter runs again), so
	// that screen cancels the ones it pushed — pushed holds their cancels.
	ctx    context.Context
	cancel context.CancelFunc
	pushed []context.CancelFunc

	// inputs holds each input's and toggle's committed value, by
	// component name: what ${input.NAME} reads. An input commits on
	// enter, a toggle on every flip.
	inputs map[string]string

	// windows lists the windowed tables (each a tuilib remote.Table),
	// keyed by component name (not source name — two tables could window the same
	// source at different scroll positions). See window.go.
	windows map[string]*windowEntry
	// windowDefs is the `window:` block per source name, for the page
	// size / prefetch the remote table needs and the column-title →
	// query-parameter map the query translation needs.
	windowDefs map[string]*cfg.WindowConfig

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
	formModal *form.Model
	// inspectModal shows an eventlog item's raw data when enter has no
	// action bound to it. esc closes it.
	inspectModal *inspector.Model
	// claims maps a run tag to the claim its run opens; claimOps finds an
	// open claim by operation, for activity.UnobservedMsg. See claims.go.
	claims         map[string]*claimRun
	claimOps       map[uint64]*claimRun
	pendingBinding cfg.ActionBinding
	pendingInputs  action.Inputs
	pendingSel     build.Selection
	pendingSels    []build.Selection
	// pendingFan is a fan-out held behind the confirm modal: one
	// dialog gates N runs, not one dialog per row.
	pendingFan    func() tea.Cmd
	pendingFields int

	// pendingResolved is the action awaiting a confirm answer.
	pendingResolved *action.Resolved
	// pendingRun is the resolved action behind the in-flight capture,
	// kept so runner.Captured can be turned back into a Result carrying
	// the action's own message: / error_message:.
	pendingRun *action.Resolved

	// id scopes this screen's internal messages; runs holds the
	// subprocesses it launched; covered is set while a screen it pushed
	// sits on top of it. See scope.go.
	id      uint64
	runs    map[*exec.Cmd]bool
	covered bool
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
// whose binding has no Bind: map. Missing required params surface as
// a build error so pushAction can pop an alert instead of constructing
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
	m := &Model{title: s.Title, th: th, tree: tree, focus: -1, multi: multi, id: nextModelID.Add(1)}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	if len(tree.All()) > 0 {
		m.focus = 0
	}
	if multi != nil {
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
		if def, ok := entries[boundName]; ok && def != nil && def.Window != nil {
			if m.windowDefs == nil {
				m.windowDefs = map[string]*cfg.WindowConfig{}
			}
			m.windowDefs[boundName] = def.Window
		}
	}
	// A growing condition reads another source — a job's status, beside
	// its events. Nothing displays that source, so register it here to be
	// fetched and polled like any bound one; handleFetch evaluates the
	// condition each time it answers.
	for _, def := range m.windowDefs {
		if def.Growing == nil || def.Growing.Always {
			continue
		}
		name := def.Growing.Source
		if _, ok := m.sources[name]; ok {
			continue
		}
		src := reg.Get(name)
		if src == nil {
			return nil, fmt.Errorf("window.growing: source %q not defined", name)
		}
		m.sources[name] = &sourceEntry{src: src}
	}
	// A column's `distinct: {source:}` reads a source nothing displays,
	// like the growing condition above: fetch and poll it the same way,
	// and hand each answer to the column (handleFetch).
	for _, name := range tree.Order {
		for i, col := range tree.Components[name].Cfg.Columns {
			if col.Distinct == nil || col.Distinct.Source == "" {
				continue
			}
			ref := col.Distinct.Source
			if _, ok := m.sources[ref]; !ok {
				src := reg.Get(ref)
				if src == nil {
					return nil, fmt.Errorf("component %q: distinct source %q not defined", name, ref)
				}
				m.sources[ref] = &sourceEntry{src: src}
			}
			if m.distinctUsers == nil {
				m.distinctUsers = map[string][]distinctUse{}
			}
			m.distinctUsers[ref] = append(m.distinctUsers[ref], distinctUse{name, i})
		}
	}
	m.initWindows()
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
	m.covered = false
	// Whatever this screen pushed has been popped: stop its requests and
	// streams rather than let them run on for a screen nobody can see.
	for _, cancel := range m.pushed {
		cancel()
	}
	m.pushed = nil
	return m.own(m.onEnter())
}

func (m *Model) onEnter() tea.Cmd {
	if m.started || len(m.sources) == 0 {
		return nil
	}
	m.started = true
	var cmds []tea.Cmd
	// Windowed sources open with a request for the first page instead of
	// a whole-set Fetch. Their remote tables drive everything after that.
	cmds = append(cmds, m.startWindows()...)
	for name, entry := range m.sources {
		// A windowed source's rows arrive through SetWindow. Fetching it
		// here would take the ordinary path into SetRows, which clears
		// the window and leaves the table claiming page one is the whole
		// set.
		if m.windowedSource(name) {
			continue
		}
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
			pump, err := subscribe(m.ctx, name, entry, streamer)
			if err == nil {
				cmds = append(cmds, m.startFetch(name), pump)
				continue
			}
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
	// A binding with no cursor driver reads only inputs, so nothing will
	// prime it: fetch it now with the inputs' initial values.
	for _, b := range m.cursorBindings {
		if b.driver == "" {
			cmds = append(cmds, m.startCursorFetch(b))
		}
	}
	return tea.Batch(cmds...)
}

// subscribe opens a streaming source's channel and returns the pump that
// drains it.
func subscribe(parent context.Context, name string, entry *sourceEntry, streamer ds.StreamingSource) (tea.Cmd, error) {
	ctx, cancel := context.WithCancel(parent)
	ch, err := streamer.Subscribe(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	entry.stream = ch
	entry.cancel = cancel
	entry.streaming = true
	return nextStreamMsg(name, ch), nil
}

// reconnect re-subscribes every stream that has ended, marking the break
// in each bound logview. A stream that's still live is left alone: its
// Fetch is an empty placeholder, so refetching it would only wipe what
// it has streamed so far.
func (m *Model) reconnect() []tea.Cmd {
	var cmds []tea.Cmd
	for name, entry := range m.sources {
		if !entry.streaming || entry.stream != nil {
			continue
		}
		streamer, ok := entry.src.(ds.StreamingSource)
		if !ok {
			continue
		}
		pump, err := subscribe(m.ctx, name, entry, streamer)
		if err != nil {
			cmds = append(cmds, app.Error(fmt.Sprintf("%s: %v", name, err)))
			continue
		}
		m.markStream(name, "— reconnected —")
		cmds = append(cmds, pump)
	}
	return cmds
}

// markStream adds an annotation line to every logview bound to source.
func (m *Model) markStream(source, text string) {
	for _, compName := range m.sourceUsers[source] {
		if c := m.tree.Components[compName]; c.Kind == build.KLogview {
			c.Logview.AppendMarker(text)
		}
	}
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
	case m.inspectModal != nil:
		modal := m.inspectModal
		return layout.ZStack(body, layout.RenderFunc(func(r geom.Rect) string {
			return layout.Center(r.W*4/5, r.H*4/5, layout.Sized(modal)).Render(r)
		}))
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
// component has an action bound to enter). Non-key messages fan out so
// spinner ticks reach every component.
func (m *Model) update(msg tea.Msg) (tscreen.Screen, tea.Cmd) {
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
			return m, m.actionAfterInputs(b, def, sel, m.pendingSels, inputs)
		case form.CancelledMsg:
			m.clearPendingForm()
			return m, nil
		}
		next, cmd := m.formModal.Update(msg)
		m.formModal = &next
		return m, cmd
	}
	// The item inspector holds the keyboard while it's open; esc closes
	// it. Everything else (fetches, ticks) carries on underneath.
	if m.inspectModal != nil {
		if k, ok := msg.(tea.KeyMsg); ok {
			if k.String() == "esc" {
				m.inspectModal = nil
				return m, nil
			}
			next, cmd := m.inspectModal.Update(k)
			m.inspectModal = &next
			return m, cmd
		}
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
			fan := m.pendingFan
			m.clearPendingConfirm()
			if fan != nil {
				return m, fan()
			}
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
		if cmd, handled := m.inputKey(k); handled {
			return m, cmd
		}
		switch k.String() {
		case "tab":
			m.cycleFocus(+1)
			return m, nil
		case "shift+tab":
			m.cycleFocus(-1)
			return m, nil
		case "enter":
			// The only key an action still fires from. Everything else
			// goes through the menu — `enter` is exempt because it is
			// not a shortcut competing for the letter budget, it is
			// activation, which is why tuilib emits ActivatedMsg for it
			// and for a double click rather than treating it as a
			// binding. See plans/tuilib-0.24.md.
			if cmd, handled := m.activate(); handled {
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
					for name, entry := range m.sources {
						// Windowed sources refresh through their
						// remote table — see refreshWindows. Streams
						// reconnect instead — see reconnect.
						if m.windowedSource(name) || entry.streaming {
							continue
						}
						if cmd := m.startFetch(name); cmd != nil {
							cmds = append(cmds, cmd)
						}
					}
					cmds = append(cmds, m.refreshWindows()...)
					cmds = append(cmds, m.reconnect()...)
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
	case windowViewportMsg:
		return m, m.handleWindowViewport(x)
	case windowQueryMsg:
		return m, m.handleWindowQuery(x)
	case windowTickMsg:
		return m, m.handleWindowTick(x)
	case runner.Result:
		if !m.ownsRun(x.Cmd) {
			return m, nil
		}
		// Interactive handoff finished. There's no captured output —
		// the subprocess owned the terminal and printed straight to it —
		// so the exit status is the whole report.
		return m, m.actionOutcome(x.Err)
	case runner.CaptureStarted:
		if cmd := m.openClaim(x.Tag); cmd != nil {
			return m, cmd
		}
	case activity.UnobservedMsg:
		return m, m.unobserved(x)
	case runner.Captured:
		// A claimed run: the rows' claim learns the outcome. The run's
		// summary already streamed into the console and the shell posts
		// the receipt, so there's nothing more to report.
		if cmd, ok := m.closeClaim(x.Tag, x.Err); ok {
			return m, cmd
		}
		if !m.ownsRun(x.Cmd) {
			return m, nil
		}
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
	case actionPickedMsg:
		// A menu pick that needs input first. The shell ran the action's
		// Do, which handed control back here rather than running
		// anything — see menuAction. From this point it is the same path
		// a key press takes.
		if cmd, ok := m.startAction(x.binding); ok {
			return m, cmd
		}
		return m, nil
	case eventlog.ActivatedMsg:
		// enter (or a double click) on an item. A bound `key: enter`
		// action gets the item as ${selection}; with none, the item
		// opens in an inspector, since the eventlog draws only its text.
		for i, c := range m.tree.All() {
			if c.Kind != build.KEventlog || c.Eventlog.FocusToken() != x.Token {
				continue
			}
			if i != m.focus {
				m.focus = i
				m.applyFocus()
			}
			if cmd, handled := m.activate(); handled {
				return m, cmd
			}
			_, key := build.SplitAnchoredKey(x.Key)
			ins := build.ItemInspector(key, x.Data, m.th)
			ins.Focus()
			m.inspectModal = &ins
			return m, nil
		}
		return m, nil
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
	cmds = m.syncFocusAfterPress(msg, cmds)
	// A click can flip a toggle too; it commits the same as a key.
	if _, ok := msg.(mouse.Msg); ok {
		for i, c := range m.tree.All() {
			if c.Kind == build.KToggle {
				cmds = append(cmds, m.commitInput(m.tree.Order[i]))
			}
		}
	}
	return m, tea.Batch(cmds...)
}

// syncFocusAfterPress applies a mouse press's focus change immediately,
// instead of waiting for the component's focus.RequestMsg to arrive as a
// command on the next Update.
//
// Right-click is why this has to be synchronous. The shell forwards the
// press and opens the action menu in the SAME Update, so by the time it
// calls Actions() the request has not been delivered — and the menu
// would describe whichever pane was focused before the click, which is
// exactly the question the gesture asked. A left click has a frame to
// spare, but running one rule for both beats two rules.
//
// It cannot read the flag off the component: a list sets its own focus
// on press, a table only emits the request, so "who thinks it is
// focused" is not a reliable answer. What is reliable is the request
// itself, so the fan-out's commands are run here and their messages
// re-wrapped. Each command still runs exactly once and every message is
// still delivered — only the timing moves, by one Update, within the
// press that caused it.
func (m *Model) syncFocusAfterPress(msg tea.Msg, cmds []tea.Cmd) []tea.Cmd {
	e, ok := msg.(mouse.Msg)
	if !ok || !e.IsPointPress() {
		return cmds
	}
	out := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		out = append(out, m.flushForFocus(cmd)...)
	}
	return out
}

// flushForFocus runs one command, applies any focus request it produced,
// and returns commands that re-deliver whatever it produced.
func (m *Model) flushForFocus(cmd tea.Cmd) []tea.Cmd {
	if cmd == nil {
		return nil
	}
	produced := cmd()
	if produced == nil {
		return nil
	}
	if batch, ok := produced.(tea.BatchMsg); ok {
		var out []tea.Cmd
		for _, c := range batch {
			out = append(out, m.flushForFocus(c)...)
		}
		return out
	}
	if req, ok := produced.(focus.RequestMsg); ok {
		for i, c := range m.tree.All() {
			if !focusRequested(c, req) {
				continue
			}
			if i != m.focus {
				m.focus = i
				m.applyFocus()
			}
			break
		}
	}
	return []tea.Cmd{func() tea.Msg { return produced }}
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
	// Stamp the read for every component tracking activity now, when the
	// fetch is issued: that's what lets the reply be told apart from one
	// a newer fetch overtook, or one that raced a write.
	var reads map[string]activity.Read
	for _, compName := range m.sourceUsers[name] {
		if c := m.tree.Components[compName]; build.HasActivity(c) {
			if reads == nil {
				reads = map[string]activity.Read{}
			}
			reads[compName] = build.BeginRead(c)
		}
	}
	cmds = append(cmds, func() tea.Msg {
		data, err := entry.src.Fetch(context.Background())
		return fetchMsg{source: name, data: data, err: err, reads: reads}
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
	// A failure that still brought data (merge with on_error: skip) is a
	// read that landed; only a fetch with nothing to show failed.
	failed := msg.data == nil && msg.err != nil
	var readErr error
	if failed {
		readErr = msg.err
	}
	for _, compName := range m.sourceUsers[msg.source] {
		c := m.tree.Components[compName]
		if !entry.loaded {
			setLoading(c, false)
		}
		if rd, ok := msg.reads[compName]; ok {
			build.ApplyRead(c, rd, msg.data, readErr, m.th)
		} else if msg.data != nil {
			build.ApplyData(c, msg.data, m.th)
		}
	}
	if msg.data != nil {
		for _, u := range m.distinctUsers[msg.source] {
			build.ApplyDistinct(m.tree.Components[u.component], u.column, msg.data)
		}
	}
	// A polled source's components say "· reads failing" on their title
	// for as long as its reads fail, so a pane that's quietly stale is
	// marked as such. Always on; one-shot sources have no poll to fail.
	polled := entry.src.Refresh() > 0
	startedFailing := failed && !entry.failing
	if polled && failed != entry.failing {
		suffix := ""
		if failed {
			suffix = " · reads failing"
		}
		for _, compName := range m.sourceUsers[msg.source] {
			build.SetTitleSuffix(m.tree.Components[compName], suffix)
		}
	}
	entry.failing = failed
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
		if cmd := m.updateGrowth(msg.source, msg.data); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if msg.err != nil {
		if firstFetch && msg.data == nil {
			cmds = append(cmds, app.ErrorDetail(
				fmt.Sprintf("%s: initial fetch failed", msg.source),
				msg.err.Error(),
			))
		} else if !polled || !failed || startedFailing {
			// A polled source reports an outage once, when its reads
			// start failing; the title carries it after that, rather
			// than a fresh error on the statusbar every poll.
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
	case build.KEventlog:
		// A remote eventlog shows its own loading state.
		return nil
	}
	return nil
}

// IsCapturingKeys reflects whether the *focused* component is engaging
// keystrokes (filter typing). Also true while any modal is showing so
// q/t/esc-pop are routed to the modal, not the app shell.
func (m *Model) IsCapturingKeys() bool {
	if m.confirmModal != nil || m.formModal != nil || m.inspectModal != nil {
		return true
	}
	c := m.current()
	if c == nil {
		return false
	}
	return componentCapturing(c)
}

// helpSectionOpen heads the one action binding that still reaches the
// help overlay.
const helpSectionOpen = "Open"

// Help returns the bindings the focused component currently exposes,
// plus the `enter` verb if this screen has one.
//
// Flat, because that is what the statusbar strip wants. The key overlay
// takes HelpSections instead.
func (m *Model) Help() []key.Binding {
	c := m.current()
	if c == nil {
		return nil
	}
	out := componentHelp(c)
	if m.focus < 0 || componentCapturing(c) {
		return out
	}
	for _, sec := range m.verbSections(m.tree.Order[m.focus]) {
		out = append(out, sec.Bindings...)
	}
	return out
}

// HelpSections is the grouped form, for tuilib's key overlay (`?`).
//
// Without it the shell wraps Help() in one unnamed group and titles it
// with the owner — which puts the screen's name over every binding it
// has, so "Pods" ends up as the heading above a table's scroll keys.
// Headings are supposed to name what the keys DO. The focused component
// already describes itself that way (Navigate / Scroll / Filter / Sort /
// Select / …); this adds the screen's own activation verb on top.
//
// Only the focused component contributes, matching Help() — which is
// also why nothing here calls help.Qualify: a qualifier earns its noise
// only when more than one owner is on screen at once.
func (m *Model) HelpSections() []help.Section {
	c := m.current()
	if c == nil {
		return nil
	}
	secs := componentHelpSections(c)
	if m.focus < 0 || componentCapturing(c) {
		return help.Sections(secs...)
	}
	return help.Sections(append(secs, m.verbSections(m.tree.Order[m.focus])...)...)
}

// verbSections is the screen's own contribution to the help list: the
// `enter` binding, and only that.
//
// Every other verb is reached through the action menu, and tuilib
// deliberately keeps a menu shortcut out of Help() — moving discovery
// off the footer and into the menu is most of the point, and a footer
// listing nine verbs it can no longer fire would be actively wrong.
// `enter` is the exception because it is still a real direct key.
func (m *Model) verbSections(source string) []help.Section {
	for _, b := range m.actions {
		if b.Key != "enter" {
			continue
		}
		if b.From != "" && b.From != source {
			continue
		}
		return []help.Section{help.Group(helpSectionOpen,
			key.NewBinding(key.WithKeys("enter"),
				key.WithHelp("⏎", labelOr(b.Label, b.Action))))}
	}
	return nil
}

// componentHelpSections asks the focused component to describe its own
// bindings as groups. Every tuilib component implements it, so this is a
// pure dispatch — the vocabulary lives upstream, which is what makes
// "Navigate" mean the same thing in a list, a table and a tree.
func componentHelpSections(c *build.Component) []help.Section {
	switch c.Kind {
	case build.KList:
		return c.List.HelpSections()
	case build.KTable:
		return c.Table.HelpSections()
	case build.KLogview:
		return c.Logview.HelpSections()
	case build.KTree:
		return c.Tree.HelpSections()
	case build.KInspector:
		return c.Inspector.HelpSections()
	case build.KTextview:
		return c.Textview.HelpSections()
	case build.KEventlog:
		return c.Eventlog.HelpSections()
	case build.KInput:
		return c.Input.HelpSections()
	case build.KToggle:
		return c.Toggle.HelpSections()
	}
	return nil
}

// SetTheme rebuilds each component against the new palette; each one
// carries its data and view state across via State / Restore.
func (m *Model) SetTheme(t theme.Theme) {
	m.th = t
	for _, c := range m.tree.All() {
		c.Rebuild(t)
	}
	// A rebuild takes the configured title back; put the outage marker
	// back on what's still failing.
	for name, entry := range m.sources {
		if entry.failing && entry.src.Refresh() > 0 {
			for _, compName := range m.sourceUsers[name] {
				build.SetTitleSuffix(m.tree.Components[compName], " · reads failing")
			}
		}
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
	// Only enter commits, so text typed and then tabbed away from is
	// put back.
	if c := all[m.focus]; c.Kind == build.KInput {
		c.Input.SetValue(m.inputs[m.tree.Order[m.focus]])
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
	case build.KEventlog:
		return c.Eventlog
	case build.KInput:
		return c.Input
	case build.KToggle:
		return c.Toggle
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
	case build.KEventlog:
		return c.Eventlog.IsCapturingKeys()
	case build.KInput:
		return c.Input.IsCapturingKeys()
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
	case build.KEventlog:
		return c.Eventlog.Help()
	case build.KInput:
		return c.Input.Help()
	case build.KToggle:
		return c.Toggle.Help()
	}
	return nil
}

// activate runs the verb bound to `enter` against the focused
// component. Keyboard enter and a double click both route through here,
// so the two spellings of the same gesture can't drift apart.
//
// This is the whole of the direct-key path now. Every other verb is
// reached through the action menu, which is where discovery belongs: a
// footer holds one row and a screen can easily have nine verbs.
//
// Suppressed while a component is capturing keys, so enter inside a
// filter stays a filter key. A binding with `from:` only fires while
// that pane holds focus — that's what lets enter mean different things
// in different panes.
func (m *Model) activate() (tea.Cmd, bool) {
	if len(m.actions) == 0 {
		return nil, false
	}
	cur := m.current()
	if cur != nil && componentCapturing(cur) {
		return nil, false
	}
	focusedName := ""
	if m.focus >= 0 && m.focus < len(m.tree.Order) {
		focusedName = m.tree.Order[m.focus]
	}
	for _, b := range m.actions {
		if b.Key != "enter" {
			continue
		}
		if b.From != "" && b.From != focusedName {
			continue
		}
		return m.startAction(b)
	}
	return nil, false
}

// startAction runs one binding, from wherever it was picked — a direct
// key or the action menu. Splitting it out is what keeps the two entry
// points honest: they cannot drift on binding resolution, input
// collection, confirm or dispatch, because there is only one copy.
func (m *Model) startAction(b cfg.ActionBinding) (tea.Cmd, bool) {
	b = m.bindInputs(b)
	cur := m.current()
	focusedName := ""
	if m.focus >= 0 && m.focus < len(m.tree.Order) {
		focusedName = m.tree.Order[m.focus]
	}
	def := m.actionDefs[b.Action]
	if def == nil {
		// Unreachable via Load (the validator resolves every reference),
		// so this only fires for a hand-built Model in a test. Report
		// rather than panic.
		return app.Error(fmt.Sprintf("action %q is not defined", b.Action)), true
	}
	var sels []build.Selection
	if b.From == "" || b.From == focusedName {
		sels = m.selectionsFor(b, cur)
	}
	sel := build.Selection{}
	if len(sels) > 0 {
		sel = sels[0]
	}
	if def.Kind() == "push" {
		return m.pushAction(b, def, sel)
	}
	inputs := resolveBinds(b, def, sel)

	// Anything the call site didn't bind gets collected from the user.
	// The form is generated from the action's own declared inputs —
	// there is no separate prompts: schema to keep in sync.
	if missing := unboundInputs(def, inputs); len(missing) > 0 {
		f := m.newInputForm(def, missing)
		m.formModal = &f
		m.pendingBinding = b
		m.pendingInputs = inputs
		m.pendingSel = sel
		m.pendingSels = sels
		m.pendingFields = len(missing)
		return f.Init(), true
	}
	return m.actionAfterInputs(b, def, sel, sels, inputs), true
}

// actionAfterInputs is the second leg of dispatch — runs once every
// input has a value, whether from bind: or from the generated form.
// Resolves the action into something concrete, then either pops the
// confirm modal or dispatches.
func (m *Model) actionAfterInputs(b cfg.ActionBinding, def *cfg.Action, sel build.Selection, sels []build.Selection, inputs action.Inputs) tea.Cmd {
	// Fan-out after the form: the answers just typed apply to every
	// marked row, while each row's bind: templates still resolve against
	// that row. Confirm once, then N runs.
	if def.Multi && len(sels) > 1 {
		a := m.menuAction(b, def, m.current(), b.From)
		fan := func() tea.Cmd { return m.fanOut(b, def, a, sels, inputs) }
		if b.Confirm.Message != "" {
			msg := build.SubstituteAll([]string{b.Confirm.Message}, sel, inputs)[0]
			msg = fmt.Sprintf("%s\n\nRuns %d times, once per marked row.", msg, len(sels))
			modal := m.newConfirmModal(labelOr(b.Label, b.Action), msg, b.Confirm.Default == "yes")
			m.confirmModal = &modal
			m.pendingFan = fan
			return nil
		}
		return fan()
	}

	resolved, err := action.Resolve(m.inputDef(def), inputs)
	if err != nil {
		return app.Error(fmt.Sprintf("%s: %v", b.Action, err))
	}
	// A claiming action goes out as a tagged run, so its start and end
	// can open and close the rows' claim. Confirm first, like any other.
	if def.Claim != nil {
		tag := newClaimTag(b.Action)
		m.registerClaim(tag, b.From, claimKeys(sels), def, b.Action, resolved.Receipt())
		label := labelOr(b.Label, b.Action)
		launch := func() tea.Cmd {
			return runner.GoWith(runner.GoOptions{
				Label: label, Detail: label + " · " + sel.String, Tag: tag, Run: runFunc(resolved),
			})
		}
		if b.Confirm.Message != "" {
			msg := build.SubstituteAll([]string{b.Confirm.Message}, sel, resolved.Values())[0]
			modal := m.newConfirmModal(label, msg, b.Confirm.Default == "yes")
			m.confirmModal = &modal
			m.pendingFan = launch
			return nil
		}
		return launch()
	}
	// Preview text substitutes the RESOLVED values, not the raw bind map:
	// defaults are applied inside Resolve, so an input with a default
	// would otherwise render empty in the confirm while the argv got the
	// real value — a confirm that disagrees with what runs.
	vals := resolved.Values()
	notice := build.SubstituteAll([]string{b.Notice}, sel, vals)[0]

	if b.Confirm.Message != "" {
		msg := build.SubstituteAll([]string{b.Confirm.Message}, sel, vals)[0]
		modal := m.newConfirmModal(labelOr(b.Label, b.Action), msg, b.Confirm.Default == "yes")
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
	m.pendingFan = nil
}

// clearPendingForm drops the form modal and every piece of action
// context it was carrying.
func (m *Model) clearPendingForm() {
	m.formModal = nil
	m.pendingBinding = cfg.ActionBinding{}
	m.pendingInputs = nil
	m.pendingSel = build.Selection{}
	m.pendingSels = nil
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
	m.launched(cmd)
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
		fields[i] = build.FormField(name, labelOr(p.Label, name), *p, p.Default)
	}
	opts := build.FormOptions(m.th).With(fields)
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
func (m *Model) newConfirmModal(label, message string, startOnYes bool) confirm.Model {
	opts := m.th.Confirm()
	// Cancel starts highlighted unless the binding says `default: yes` —
	// the safe side for a destructive action.
	opts.Initial = startOnYes
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

// componentActivated reports whether msg is c's own activation. Only
// lists and tables emit one, which is also all the validator allows as
// an action source.
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
	case build.KEventlog:
		return c.Eventlog.IsActivate(msg)
	}
	return false
}

// pushAction opens the screen a `push:` action names, filling the
// destination's parameters from the binding's bind: map against sel.
// The bool is always true — a push is handled even when it fails, since
// the failure surfaces as an error cmd rather than falling through.
func (m *Model) pushAction(b cfg.ActionBinding, def *cfg.Action, sel build.Selection) (tea.Cmd, bool) {
	if m.multi == nil {
		return app.Error(fmt.Sprintf("%s: push actions need a multi-screen config", b.Action)), true
	}
	// Resolve the bind: block against the focused row. Always a non-nil
	// map, even when empty — a push IS a binding context whether or not
	// the author declared one, which lets applyBindParams report a
	// missing required param instead of silently building a screen that
	// 404s on its first fetch.
	params := make(map[string]string, len(b.Bind))
	for k, v := range b.Bind {
		params[k] = build.Substitute(v, sel)
	}
	child, err := BuildMulti(def.Push, m.multi, sel, params, m.th)
	if err != nil {
		return app.Error(fmt.Sprintf("%s: %v", def.Push, err)), true
	}
	m.covered = true
	if cancel := cancelOf(child); cancel != nil {
		m.pushed = append(m.pushed, cancel)
	}
	return tscreen.Push(child), true
}

// selectionFrom extracts a Selection from a list or table component. The
// validator restricts action sources to lists and tables, so other kinds
// return a zero Selection.
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
		return build.Selection{String: xansi.Strip(s), Key: build.CursorKey(c)}
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
		sel := build.Selection{String: first, Cells: cells, Columns: titles, Key: build.CursorKey(c)}
		// A metric column's cell is glyphs; the selection wants its value.
		return build.RawSelection(c, sel)
	case build.KEventlog:
		it, ok := c.Eventlog.Selected()
		if !ok || it.Hole {
			return build.Selection{}
		}
		_, key := build.SplitAnchoredKey(it.Key)
		return build.ItemSelection(key, it.Data)
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
	if x.err != nil || x.done {
		// The stream is over, cleanly or not. Drop the channel so `r`
		// can re-subscribe; cancel the context so any lingering
		// goroutines in the source can exit; mark the break in the log.
		if entry.cancel != nil {
			entry.cancel()
			entry.cancel = nil
		}
		entry.stream = nil
		m.markStream(x.source, "— stream ended —")
		if x.err != nil {
			return app.Error(fmt.Sprintf("%s: %v", x.source, x.err))
		}
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
		if c.Remote != nil {
			// Every message, not just keys: remote routes its own fetch
			// results and timers through here.
			cmd := c.Remote.Update(msg)
			c.LandAtNewest()
			return cmd
		}
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
	case build.KEventlog:
		if c.RemoteLog != nil {
			return c.RemoteLog.Update(msg)
		}
		m, cmd := c.Eventlog.Update(msg)
		*c.Eventlog = m
		return cmd
	case build.KInput:
		m, cmd := c.Input.Update(msg)
		*c.Input = m
		return cmd
	case build.KToggle:
		m, cmd := c.Toggle.Update(msg)
		*c.Toggle = m
		return cmd
	}
	return nil
}

// cursorBinding is the resolved form of a component's `on_cursor:`
// declaration. Compared against every driver's tagged RowFocusedMsg
// to decide which dependent source should refetch.
type cursorBinding struct {
	driver string            // driver component name (a table); "" when only inputs feed it
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
	case table.ViewportChangedMsg, table.QueryChangedMsg:
		// Windowed-source traffic. Tagged here rather than in its own
		// wrapper because this is already the one place that knows which
		// component's Update produced a message, batch unwrap included.
		return translateWindowMsg(msg, name)
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
	m.inputs = map[string]string{}
	for _, name := range m.tree.Order {
		if c := m.tree.Components[name]; c.Kind == build.KInput || c.Kind == build.KToggle {
			m.inputs[name] = build.InputValue(c)
		}
	}
	for _, name := range m.tree.Order {
		comp := components[name]
		if comp == nil || (comp.OnCursor == nil && comp.OnInput == nil) {
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
		// on_cursor and on_input feed one binding: the source is fetched
		// with every parameter either fills, whichever of them moved.
		b := cursorBinding{target: name, source: srcName, bind: map[string]string{}}
		if comp.OnCursor != nil {
			b.driver = comp.OnCursor.Source
			for k, v := range comp.OnCursor.Bind {
				b.bind[k] = v
			}
		}
		if comp.OnInput != nil {
			for k, v := range comp.OnInput.Bind {
				b.bind[k] = v
			}
		}
		m.cursorBindings = append(m.cursorBindings, b)
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
		// The focus message carries drawn cells; a metric column's value
		// comes from the focused row's item instead.
		m.cursorState[t.driver] = build.RawSelection(m.tree.Components[t.driver], t.sel)
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
		params[k] = build.SubstituteCursor(build.SubstituteInputValues(tmpl, m.inputs), sel)
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
