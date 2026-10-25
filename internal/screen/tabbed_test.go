package screen

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/palette"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// pump feeds msgs (and whatever they produce) to s until it settles.
func pump(t *testing.T, s tscreen.Screen, msgs ...tea.Msg) {
	t.Helper()
	inbox := append([]tea.Msg{tea.WindowSizeMsg{Width: 120, Height: 40}}, msgs...)
	for i := 0; i < 300 && len(inbox) > 0; i++ {
		msg := inbox[0]
		inbox = inbox[1:]
		_, cmd := s.Update(msg)
		inbox = append(inbox, drainCmd(cmd)...)
	}
}

func tabsExample(t *testing.T, set func(*cfg.Screen)) *Tabbed {
	t.Helper()
	c, err := cfg.Load(filepath.Join("..", "..", "examples", "tabs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// The Events tab polls every 2s; the pump would wait out each tick.
	c.Data.Sources["events"].Refresh = ""
	if set != nil {
		set(&c.TUI.Screen)
	}
	s, err := Build(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	h, ok := s.(*Tabbed)
	if !ok {
		t.Fatalf("Build gave %T, want *Tabbed", s)
	}
	pump(t, h, drainCmd(h.Init())...)
	pump(t, h, drainCmd(h.OnEnter(nil))...)
	return h
}

var shiftRight = tea.KeyMsg{Type: tea.KeyShiftRight}

func TestTabbedOpensOnItsInitialTab(t *testing.T) {
	h := tabsExample(t, func(s *cfg.Screen) { s.Initial = "Services" })
	if h.tabs.ActiveLabel() != "Services" {
		t.Errorf("active = %q, want Services", h.tabs.ActiveLabel())
	}
	if h.Title() != "Cluster" {
		t.Errorf("title = %q, want the screen's", h.Title())
	}
}

// A hidden tab first fetches when it's opened; only the active tab
// counts as uncovered, so a finished run is claimed once.
func TestTabbedFetchesLazilyAndCoversHiddenTabs(t *testing.T) {
	h := tabsExample(t, nil)
	pods, services := h.bodies[0], h.bodies[1]
	if !pods.started || services.started {
		t.Fatalf("started: pods=%v services=%v, want only the active tab", pods.started, services.started)
	}
	if pods.covered || !services.covered {
		t.Fatalf("covered: pods=%v services=%v, want only hidden tabs covered", pods.covered, services.covered)
	}

	pump(t, h, shiftRight)
	if h.tabs.ActiveLabel() != "Services" {
		t.Fatalf("shift+right: active = %q", h.tabs.ActiveLabel())
	}
	if !services.started {
		t.Error("opening a tab should start its first fetch")
	}
	if !pods.covered || services.covered {
		t.Errorf("after switching, covered: pods=%v services=%v", pods.covered, services.covered)
	}
	if got := len(services.tree.Components["services_table"].Table.Rows()); got != 2 {
		t.Errorf("services table has %d rows, want 2", got)
	}
}

// The action menu, help and key capture are the active tab's.
func TestTabbedForwardsToTheActiveTab(t *testing.T) {
	h := tabsExample(t, nil)
	if n := len(h.Actions().Actions); n != 1 {
		t.Errorf("Pods tab: %d actions, want its describe binding", n)
	}
	pump(t, h, shiftRight)
	if n := len(h.Actions().Actions); n != 0 {
		t.Errorf("Services tab: %d actions, want none", n)
	}
}

func TestTabbedSwitchKeys(t *testing.T) {
	no := false
	h := tabsExample(t, func(s *cfg.Screen) { s.NextKey, s.NumberKeys = "-", &no })
	pump(t, h, shiftRight, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if h.tabs.ActiveLabel() != "Pods" {
		t.Errorf("with next_key and number_keys off, active = %q, want Pods", h.tabs.ActiveLabel())
	}
	h = tabsExample(t, nil)
	pump(t, h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if h.tabs.ActiveLabel() != "Events" {
		t.Errorf("3 should jump to the third tab, active = %q", h.tabs.ActiveLabel())
	}
}

func TestTabbedThemeSwapKeepsTheActiveTab(t *testing.T) {
	h := tabsExample(t, nil)
	pump(t, h, shiftRight)
	h.SetTheme(theme.New(palette.Dark()))
	if h.tabs.ActiveLabel() != "Services" {
		t.Errorf("after a theme swap, active = %q", h.tabs.ActiveLabel())
	}
	if got := len(h.bodies[1].tree.Components["services_table"].Table.Rows()); got != 2 {
		t.Errorf("services rows after a theme swap = %d, want 2", got)
	}
}

// A pushed screen can be tabbed: BuildMulti builds the host, and the
// push's selection reaches every tab.
func TestBuildMultiBuildsATabbedScreen(t *testing.T) {
	multi := &Multi{
		Screens: map[string]*cfg.Screen{
			"detail": {Title: "${selection}", Tabs: []cfg.Tab{
				{Label: "A", Layout: cfg.Node{Component: "a"}},
				{Label: "B", Layout: cfg.Node{Component: "b"}},
			}},
		},
		Components: map[string]*cfg.Component{
			"a": {Type: "list", Items: []string{"one"}},
			"b": {Type: "list", Items: []string{"two"}},
		},
		Sources: map[string]*cfg.Source{},
	}
	s, err := BuildMulti("detail", multi, build.Selection{String: "web"}, nil, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	h, ok := s.(*Tabbed)
	if !ok {
		t.Fatalf("got %T, want *Tabbed", s)
	}
	if h.Title() != "web" {
		t.Errorf("title = %q, want the selection substituted", h.Title())
	}
	if len(h.bodies) != 2 || h.bodies[0].multi != multi {
		t.Error("each tab should be built with the multi context, so it can push in turn")
	}
}
