package screen

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	taction "github.com/jsdrews/tuilib/pkg/action"
	"github.com/jsdrews/tuilib/pkg/help"
	"github.com/jsdrews/tuilib/pkg/layout"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/tab"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Tabbed is a screen whose body is tabs: tuilib's pkg/tab hosting one
// Model per tab. Each tab is a small screen of its own — its layout, its
// sources, its bindings — and tuilib does the routing: keys and clicks
// go to the active tab, everything else (polls, fetch results, streams)
// to every tab, so hidden tabs keep their data current. A tab pane isn't
// a navigation level: it never touches the screen stack, which is why
// the breadcrumb shows only the screen's title.
type Tabbed struct {
	title  string
	tabs   tab.Model
	bodies []*Model
	// active is the tab the bodies' cover state was last set for.
	active int
}

// Build builds the screen a single-screen config declares: a Tabbed host
// when it has `tabs:`, a Model otherwise.
func Build(s *cfg.Screen, components map[string]*cfg.Component, entries map[string]*cfg.Source, actions map[string]*cfg.Action, th theme.Theme) (tscreen.Screen, error) {
	if len(s.Tabs) == 0 {
		m, err := New(s, components, entries, actions, th)
		if err != nil {
			return nil, err
		}
		return m, nil
	}
	return newTabbed(s, components, entries, actions, th, nil, build.Selection{}, nil)
}

// BuildMulti is Build for one screen of a multi-screen config, including
// a screen pushed by an action.
func BuildMulti(screenName string, multi *Multi, sel build.Selection, params map[string]string, th theme.Theme) (tscreen.Screen, error) {
	s, ok := multi.Screens[screenName]
	if ok && len(s.Tabs) > 0 {
		return newTabbed(s, multi.Components, multi.Sources, multi.Actions, th, multi, sel, params)
	}
	m, err := NewMulti(screenName, multi, sel, params, th)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func newTabbed(s *cfg.Screen, components map[string]*cfg.Component, entries map[string]*cfg.Source, actions map[string]*cfg.Action, th theme.Theme, multi *Multi, sel build.Selection, params map[string]string) (*Tabbed, error) {
	h := &Tabbed{}
	tabs := make([]tab.Tab, 0, len(s.Tabs))
	for i, t := range s.Tabs {
		// Each tab is built exactly as a screen with that layout and
		// those bindings would be, so a push's selection and bind:
		// reach the tab's sources the same way.
		pane := &cfg.Screen{Title: s.Title, Layout: t.Layout, Actions: t.Actions}
		subS, subC, subE, err := build.SubstituteScreen(pane, components, entries, sel, params)
		if err != nil {
			return nil, err
		}
		m, err := build_(subS, subC, subE, actions, th, multi)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			h.title = subS.Title
		}
		h.bodies = append(h.bodies, m)
		tabs = append(tabs, tab.Tab{Label: t.Label, Body: m})
	}
	opts := tab.Options{
		Tabs:              tabs,
		Theme:             th,
		PrevKey:           tabKey(s.PrevKey),
		NextKey:           tabKey(s.NextKey),
		DisableNumberKeys: s.NumberKeys != nil && !*s.NumberKeys,
	}
	if s.StripPos == "bottom" {
		opts.StripPos = tab.StripBottom
	}
	for i, t := range s.Tabs {
		if t.Label == s.Initial {
			opts.Initial = i
		}
	}
	h.tabs = tab.New(opts)
	h.active = h.tabs.ActiveTab()
	h.syncCover()
	return h, nil
}

// tabKey turns a configured switch key into the binding tab.Options
// wants: zero for tuilib's default, a binding that never matches for
// "-".
func tabKey(k string) key.Binding {
	switch k {
	case "":
		return key.Binding{}
	case "-":
		return key.NewBinding(key.WithKeys(k), key.WithDisabled())
	}
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, "tab"))
}

// syncCover marks every tab but the active one as covered. A finished
// background run is claimed by the screen that isn't covered, and a
// hidden tab is no more the place the user started it than a screen
// under a push is; without this, two tabs would both report it.
func (h *Tabbed) syncCover() {
	for i, b := range h.bodies {
		b.covered = i != h.active
	}
}

// Title satisfies screen.Screen.
func (h *Tabbed) Title() string { return h.title }

// Init runs every tab's Init and the active tab's OnEnter.
func (h *Tabbed) Init() tea.Cmd { return h.tabs.Init() }

// Update hands the message to the tab pane, which routes it.
func (h *Tabbed) Update(msg tea.Msg) (tscreen.Screen, tea.Cmd) {
	var cmd tea.Cmd
	h.tabs, cmd = h.tabs.Update(msg)
	if a := h.tabs.ActiveTab(); a != h.active {
		h.active = a
		h.syncCover()
	}
	return h, cmd
}

// Layout fills the screen with the strip and the active tab.
func (h *Tabbed) Layout() layout.Node { return layout.Sized(&h.tabs) }

// OnEnter tells the active tab — the one a push left from — that it's on
// top again.
func (h *Tabbed) OnEnter(result any) tea.Cmd {
	h.syncCover()
	return h.tabs.OnEnterActive(result)
}

// SetTheme reaches every tab, hidden ones included.
func (h *Tabbed) SetTheme(t theme.Theme) { h.tabs.SetTheme(t) }

// IsCapturingKeys is the active tab's, so `q` typed into a filter stays
// in the filter.
func (h *Tabbed) IsCapturingKeys() bool { return h.tabs.IsCapturingKeys() }

// Help is the strip's keys and the active tab's.
func (h *Tabbed) Help() []key.Binding {
	return append(h.tabs.Help(), h.current().Help()...)
}

// HelpSections is the strip's keys and the active tab's.
func (h *Tabbed) HelpSections() []help.Section {
	return append(h.tabs.HelpSections(), h.current().HelpSections()...)
}

// Actions is the active tab's verbs: the action menu acts on what's on
// screen.
func (h *Tabbed) Actions() taction.Set { return h.current().Actions() }

func (h *Tabbed) current() *Model { return h.bodies[h.tabs.ActiveTab()] }
