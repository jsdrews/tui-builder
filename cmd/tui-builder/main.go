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

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/form"
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

	// Pre-flight: if the config declares app-level prompts, collect
	// them BEFORE building the screen so URL / argv / header
	// substitutions resolve cleanly via ${env.*}. The prompts run as
	// a tiny separate tea.Program with just a form; on submit we
	// os.Setenv each value, on cancel we exit cleanly.
	if len(c.App.Prompts) > 0 {
		if err := collectAppPrompts(c.App.Prompts, initial); err != nil {
			return err
		}
	}

	themes = reorderThemes(themes, initial.Name)

	var root tscreen.Screen
	if len(c.Screens) > 0 {
		multi := &tqscreen.Multi{
			Screens:     c.Screens,
			Components:  c.Components,
			DataSources: c.DataSources,
			Pipelines:   c.Pipelines,
		}
		root, err = tqscreen.NewMulti(c.Initial, multi, build.Selection{}, nil, initial)
	} else {
		root, err = tqscreen.New(&c.Screen, c.Components, c.DataSources, c.Pipelines, initial)
	}
	if err != nil {
		return err
	}

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

// collectAppPrompts opens a one-screen tea.Program with just a form
// (one field per prompt). On submit, each value is os.Setenv'd under
// the prompt's Key so ${env.<KEY>} downstream resolves to the user's
// input. On cancel (esc), we return an error so the caller exits.
//
// Pre-populating: each text prompt's Initial defaults to the current
// env value if one is set, so `SYMBOLS=… tui-builder …` skips the
// modal in spirit — you can see the value and just press enter.
func collectAppPrompts(prompts []cfg.Prompt, th theme.Theme) error {
	fields := make([]form.Field, len(prompts))
	for i, p := range prompts {
		label := p.Label
		if label == "" {
			label = p.Key
		}
		switch p.Type {
		case "select":
			fields[i] = form.Select(form.SelectOptions{
				Key:     p.Key,
				Label:   label,
				Options: append([]string(nil), p.Options...),
				Initial: p.InitialIdx,
			})
		case "confirm":
			fields[i] = form.Confirm(form.ConfirmOptions{
				Key:     p.Key,
				Label:   label,
				Initial: p.InitialBool,
			})
		default: // text
			initial := p.Initial
			if v := os.Getenv(p.Key); v != "" {
				initial = v
			}
			fields[i] = form.Text(form.TextOptions{
				Key:         p.Key,
				Label:       label,
				Placeholder: p.Placeholder,
				Initial:     initial,
			})
		}
	}

	m := &promptModel{form: form.New(th.Form().With(fields))}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return err
	}
	if m.cancelled {
		return fmt.Errorf("startup prompts cancelled")
	}
	for k, v := range m.values {
		os.Setenv(k, stringify(v))
	}
	return nil
}

// stringify normalises a form value (bool/string/anything) to the
// string form ${env.*} will return when read via os.Getenv.
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

// promptModel is the one-form tea program used for boot-time prompts.
// It exists only long enough to render the form, capture submit /
// cancel, and exit.
type promptModel struct {
	form      form.Model
	w, h      int
	values    map[string]any
	cancelled bool
}

func (m *promptModel) Init() tea.Cmd { return tea.Batch(textinput.Blink, m.form.Init()) }

func (m *promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = x.Width, x.Height
		return m, nil
	case form.SubmittedMsg:
		m.values = x.Values
		return m, tea.Quit
	case form.CancelledMsg:
		m.cancelled = true
		return m, tea.Quit
	}
	next, cmd := m.form.Update(msg)
	m.form = next
	return m, cmd
}

func (m *promptModel) View() string {
	if m.w == 0 {
		return ""
	}
	m.form.SetDimensions(m.w-4, m.h-2)
	return m.form.View()
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
