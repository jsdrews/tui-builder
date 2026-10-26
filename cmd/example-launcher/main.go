// Command example-launcher opens a TUI that lists every YAML config in
// examples/ and lets the user pick one to run. The picked config's screen
// is pushed onto the same app stack — esc returns to the launcher; q quits
// at the root.
//
// By default it looks for examples in ./examples then ../examples (so it
// runs cleanly from the project root or from cmd/example-launcher).
// Override with -dir to point at any directory of *.yaml files.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/datasource"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", "", "directory of *.yaml configs (defaults to ./examples or ../examples)")
	flag.Parse()

	paths, err := findYAMLs(*dir)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		if *dir != "" {
			return fmt.Errorf("no *.yaml files found in %s", *dir)
		}
		return fmt.Errorf("no examples found — pass -dir <path> to point at a configs directory")
	}

	palettes := palette.All()
	// The shell picks the starting palette and hands the root its
	// composed theme straight away; this one only lasts until then.
	root := NewLauncher(paths, theme.New(palettes[0]))

	prog := tea.NewProgram(
		app.New(app.Options{
			Root:     root,
			Palettes: palettes,
			// Every built-in surface, so an example's `surface:` can be
			// switched to as it opens, and `T` cycles them as in
			// tui-builder.
			Surfaces:   build.Surfaces(&cfg.App{}, ""),
			SurfaceKey: key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "surface")),
			// tuilib's action menu, on `a` as in tui-builder: every
			// example's bindings, grouped read / write. Without a binding
			// the shell never opens it, so `a` did nothing here.
			ActionsKey: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions")),
			// Same default as tui-builder: `t` cycles the palette.
			// Without a binding the Palettes list above is inert.
			ThemeKey: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "theme")),
			// Match tui-builder: the launcher pushes the same screens, so
			// mouse must be on here too or clicking would work only for
			// configs opened directly.
			Mouse: app.MouseClick,
			// Same reasoning for the output console — a config opened
			// through the launcher must have the same sink for action
			// results and fetch errors as one opened directly.
			OutputKey: key.NewBinding(
				key.WithKeys("o"),
				key.WithHelp("o", "output"),
			),
		}),
		tea.WithAltScreen(),
	)
	// A source's passing notices (a retry, a walk stopped early) go to the
	// output console, as under tui-builder. Send is safe from the fetch
	// goroutines.
	datasource.Notify = func(msg string) { prog.Send(app.StatusInfoMsg{Text: msg}) }
	defer func() { datasource.Notify = nil }()
	_, err = prog.Run()
	return err
}

func findYAMLs(explicit string) ([]string, error) {
	dirs := []string{explicit}
	if explicit == "" {
		dirs = []string{"examples", filepath.Join("..", "examples")}
	}
	for _, d := range dirs {
		matches, err := filepath.Glob(filepath.Join(d, "*.yaml"))
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			sort.Strings(matches)
			return matches, nil
		}
	}
	return nil, nil
}
