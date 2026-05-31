// Command tui-builder renders a TUI declared in a YAML config file.
//
//	tui-builder <config.yaml>
//
// The config schema is described in internal/config. See examples/ for
// sample configurations, or run `example-launcher` for a TUI that lists
// every example and lets you pick one.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	tqscreen "github.com/jsdrews/tui-builder/internal/screen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: tui-builder <config.yaml>")
	}

	c, err := cfg.Load(os.Args[1])
	if err != nil {
		return err
	}

	themes := theme.All()
	initial := themes[0]
	if c.App.Theme != "" {
		if t, ok := theme.ByName(themes, c.App.Theme); ok {
			initial = t
		}
	}

	var root tscreen.Screen
	if len(c.Screens) > 0 {
		multi := &tqscreen.Multi{
			Screens:     c.Screens,
			Components:  c.Components,
			DataSources: c.DataSources,
		}
		root, err = tqscreen.NewMulti(c.Initial, multi, build.Selection{}, initial)
	} else {
		root, err = tqscreen.New(&c.Screen, c.Components, c.DataSources, initial)
	}
	if err != nil {
		return err
	}

	themes = reorderThemes(themes, initial.Name)

	prog := tea.NewProgram(
		app.New(app.Options{
			Root:        root,
			Themes:      themes,
			Version:     c.App.Version,
			HelpVerbose: c.App.HelpVerbose,
		}),
		tea.WithAltScreen(),
	)
	_, err = prog.Run()
	return err
}

func reorderThemes(in []theme.Theme, head string) []theme.Theme {
	if head == "" {
		return in
	}
	for i, t := range in {
		if t.Name == head {
			if i == 0 {
				return in
			}
			out := make([]theme.Theme, 0, len(in))
			out = append(out, t)
			out = append(out, in[:i]...)
			out = append(out, in[i+1:]...)
			return out
		}
	}
	return in
}
