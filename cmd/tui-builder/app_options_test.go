package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/palette"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Most specific wins: env (this run), then app.theme (the config's
// author), then the user's tuilib config (their machine), then the
// first theme. Before, the user's file beat app.theme.
func TestInitialPalettePrecedence(t *testing.T) {
	palettes := palette.All()
	first := palettes[0].Name
	cases := []struct {
		env, appTheme, user, want string
	}{
		{"", "", "", first},
		{"", "", "nord", "nord"},
		{"", "dracula", "nord", "dracula"},
		{"gruvbox", "dracula", "nord", "gruvbox"},
		{"nope", "dracula", "nord", "dracula"}, // unknown falls through
		{"", "nope", "nope", first},
	}
	for _, tc := range cases {
		if got := initialPalette(palettes, tc.env, tc.appTheme, tc.user).Name; got != tc.want {
			t.Errorf("env=%q app=%q user=%q: got %q, want %q", tc.env, tc.appTheme, tc.user, got, tc.want)
		}
	}
}

func TestDoubleClickFallsBackToTheUserFile(t *testing.T) {
	if got := doubleClick("250ms", 600); got != 250*time.Millisecond {
		t.Errorf("app.double_click should win, got %v", got)
	}
	if got := doubleClick("", 600); got != 600*time.Millisecond {
		t.Errorf("unset should use double_click_ms, got %v", got)
	}
	if got := doubleClick("", 0); got != 0 {
		t.Errorf("neither set should leave tuilib's default, got %v", got)
	}
}

func TestShellOptionMapping(t *testing.T) {
	if mouseMode("off") != app.MouseOff || mouseMode("") != app.MouseClick {
		t.Error("mouse: off → MouseOff, unset → MouseClick")
	}
	if o := outputOptions(&cfg.OutputConfig{MaxRecords: 50, ExportDir: "/x"}); o.MaxRecords != 50 || o.ExportDir != "/x" {
		t.Errorf("output options = %+v", o)
	}
	if o := outputOptions(nil); o.MaxRecords != 0 {
		t.Errorf("unset app.output should leave the console's defaults, got %+v", o)
	}
	// A rebound quit key keeps ctrl+c.
	if got := keyBinding("Q", "quit", "ctrl+c").Keys(); len(got) != 2 || got[1] != "ctrl+c" {
		t.Errorf("quit keys = %v, want [Q ctrl+c]", got)
	}
	if got := keyBinding("", "suspend").Keys(); len(got) != 0 {
		t.Errorf("an empty key should be the shell's off switch, got %v", got)
	}
}

type nopModel struct{}

func (nopModel) Init() tea.Cmd                       { return nil }
func (nopModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return nopModel{}, nil }
func (nopModel) View() string                        { return "" }

// app.title reaches the terminal as the window title when the program
// starts, and the wrapper otherwise stays out of the way.
func TestTitledSetsTheWindowTitle(t *testing.T) {
	m := titled{Model: nopModel{}, title: "My tool"}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command")
	}
	want := tea.SetWindowTitle("My tool")()
	found := false
	for _, msg := range flatten(cmd) {
		if msg == want {
			found = true
		}
	}
	if !found {
		t.Error("Init should set the window title")
	}
	if next, _ := m.Update(nil); next.(titled).title != "My tool" {
		t.Error("Update should keep the wrapper")
	}
}

// flatten runs a command and any batch it returns.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flatten(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// Same precedence as the palette: env, app.surface, the user's file,
// then outlined; an unknown name falls through.
func TestInitialSurfacePrecedence(t *testing.T) {
	for _, tc := range []struct{ env, app, user, want string }{
		{"", "", "", "outlined"},
		{"", "", "filled", "filled"},
		{"", "banded", "filled", "banded"},
		{"elevated", "banded", "filled", "elevated"},
		{"nope", "banded", "filled", "banded"},
	} {
		if got := initialSurface(tc.env, tc.app, tc.user); got != tc.want {
			t.Errorf("env=%q app=%q user=%q: got %q, want %q", tc.env, tc.app, tc.user, got, tc.want)
		}
	}
}

// The env var beats app.colors; auto, unset or an unknown env value
// leaves detection alone (an unknown env value falls through to the
// config).
func TestColorProfile(t *testing.T) {
	for _, tc := range []struct {
		env, app string
		want     termenv.Profile
		ok       bool
	}{
		{"", "", 0, false},
		{"", "auto", 0, false},
		{"", "truecolor", termenv.TrueColor, true},
		{"", "256", termenv.ANSI256, true},
		{"", "16", termenv.ANSI, true},
		{"256", "truecolor", termenv.ANSI256, true},
		{"auto", "truecolor", 0, false},
		{"lots", "truecolor", termenv.TrueColor, true},
	} {
		got, ok := colorProfile(tc.env, tc.app)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("env=%q app=%q: got %v %v, want %v %v", tc.env, tc.app, got, ok, tc.want, tc.ok)
		}
	}
}
