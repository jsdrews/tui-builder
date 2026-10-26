package main

// launcher.go: the example-launcher's filterable-list screen. This
// type used to live in internal/screen but it's a convenience for this
// one binary, not a primitive other code consumes — so it belongs
// next to its caller. internal/screen stays focused on the
// config-driven screen impls that the main tui-builder binary uses.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/layout"
	"github.com/jsdrews/tuilib/pkg/list"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	tqscreen "github.com/jsdrews/tui-builder/internal/screen"
)

// Launcher is a filterable list of YAML configs. Enter loads the selected
// config and pushes its screen onto the stack; esc (handled by the app
// shell) pops back to the launcher.
type Launcher struct {
	th    theme.Theme
	list  list.Model
	paths []string // parallel to the list's source Items — indexed by SelectedIndex
}

// NewLauncher builds a launcher over the given YAML paths. Display labels
// are the file's base name with the `.yaml` extension stripped.
func NewLauncher(paths []string, th theme.Theme) *Launcher {
	items := make([]string, len(paths))
	for i, p := range paths {
		items[i] = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	}
	opts := th.List()
	opts.Title = "Examples"
	opts.Items = items
	opts.Filterable = true
	li := list.New(opts)
	li.Focus()
	return &Launcher{
		th:    th,
		list:  li,
		paths: append([]string(nil), paths...),
	}
}

func (l *Launcher) Title() string         { return "Examples" }
func (l *Launcher) Init() tea.Cmd         { return nil }
func (l *Launcher) OnEnter(any) tea.Cmd   { return nil }
func (l *Launcher) Layout() layout.Node   { return layout.Sized(&l.list) }
func (l *Launcher) IsCapturingKeys() bool { return l.list.Filtering() }

func (l *Launcher) Help() []key.Binding {
	return append(l.list.Help(),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "run")),
	)
}

// SetTheme rebuilds the list against the new palette, preserving cursor +
// filter value.
func (l *Launcher) SetTheme(t theme.Theme) {
	l.th = t
	cursor := l.list.Cursor()
	value := l.list.Value()
	items := l.list.Items()
	opts := t.List()
	opts.Title = "Examples"
	opts.Items = items
	opts.Filterable = true
	l.list = list.New(opts)
	l.list.Focus()
	if value != "" {
		l.list.SetValue(value)
	}
	l.list.SetCursor(cursor)
}

// Update intercepts enter (when not filtering) to load + push the picked
// example, and forwards everything else to the list.
func (l *Launcher) Update(msg tea.Msg) (tscreen.Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		if k.String() == "enter" && !l.list.Filtering() {
			idx, ok := l.list.SelectedIndex()
			if !ok {
				return l, nil
			}
			path := l.paths[idx]
			child, surf, warnings, err := loadScreen(path, l.th)
			if err != nil {
				return l, app.Error(fmt.Sprintf("%s: %v", filepath.Base(path), err))
			}
			// Load-time warnings go to the console: the TUI owns the
			// terminal, and a line written to stderr would paint over it.
			var warn tea.Cmd
			if warnings != "" {
				head, _, _ := strings.Cut(warnings, "\n")
				warn = app.InfoDetail(fmt.Sprintf("%s: %s", filepath.Base(path), strings.TrimPrefix(head, "warning: ")), warnings)
			}
			// The launcher owns the shell, so an example's `surface:`
			// becomes a switch of the shell's surface as it opens. It
			// stays switched after esc; `T` cycles it.
			if surf != "" {
				return l, tea.Batch(warn, tea.Sequence(tscreen.Push(child), app.SetSurface(surf)))
			}
			return l, tea.Batch(warn, tscreen.Push(child))
		}
	}
	m, cmd := l.list.Update(msg)
	l.list = m
	return l, cmd
}

// loadScreen reads a YAML file and builds the screen.Screen for it,
// honouring multi-screen mode when the config declares `screens:`. surf
// is the config's `app.surface`, "" when it names none; warnings are
// what loading it would have printed to stderr.
func loadScreen(path string, th theme.Theme) (s tscreen.Screen, surf, warnings string, err error) {
	var warn strings.Builder
	c, err := cfg.LoadTo(path, &warn)
	if err != nil {
		return nil, "", "", err
	}
	warnings = strings.TrimSpace(warn.String())
	// The example's keymap, form spacing and mask reach inside its
	// components and forms, so they can follow it here. Its shell
	// settings (keys, mouse, title) can't: the launcher owns the shell.
	build.SetAppChrome(&c.App)
	if len(c.TUI.Screens) > 0 {
		multi := &tqscreen.Multi{
			Screens:    c.TUI.Screens,
			Components: c.TUI.Components,
			Sources:    c.Data.Sources,
			Actions:    c.Actions,
		}
		s, err = tqscreen.BuildMulti(c.TUI.Initial, multi, build.Selection{}, nil, th)
		return s, c.App.Surface, warnings, err
	}
	s, err = tqscreen.Build(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, th)
	return s, c.App.Surface, warnings, err
}
