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
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/geom"
	"github.com/jsdrews/tuilib/pkg/mouse"
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

	// app.glyphs / app.borders land on every palette, not just the one
	// app.theme names — cycling themes (`t`) walks the whole slice, and
	// the chrome vocabulary shouldn't change halfway through.
	themes := build.ApplyChrome(theme.All(), &c.App)
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

	// Resolve ${env.*} now that the environment is final — after the
	// prompts above have os.Setenv'd their answers, before any screen is
	// built. Doing it here rather than in cfg.Load is what lets a prompt
	// supply a var that the config references.
	c.SubstituteEnv()

	themes = reorderThemes(themes, initial.Name)

	var root tscreen.Screen
	if len(c.TUI.Screens) > 0 {
		multi := &tqscreen.Multi{
			Screens:    c.TUI.Screens,
			Components: c.TUI.Components,
			Sources:    c.Data.Sources,
			Actions:    c.Actions,
		}
		root, err = tqscreen.NewMulti(c.TUI.Initial, multi, build.Selection{}, nil, initial)
	} else {
		root, err = tqscreen.New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, initial)
	}
	if err != nil {
		return err
	}

	prog := tea.NewProgram(
		app.New(app.Options{
			Root:    root,
			Themes:  themes,
			Version: c.App.Version,
			// The output console. Everything a subprocess streams and
			// every statusbar message lands here, with a badge counting
			// events and a picker for killing what's still in flight.
			// Zero binding (app.output_key: "-") leaves it off entirely.
			OutputKey: outputBinding(c.App.OutputConsoleKey()),
			// Theme cycling. A zero binding disables it, which is what
			// this was until now — so `theme.All()`, reorderThemes and
			// every "press t" in the docs described something that
			// couldn't happen. Zero binding (app.theme_key: "-") pins
			// the palette.
			ThemeKey: keyBinding(c.App.ThemeCycleKey(), "theme"),
			// Every component we build (list / table / tree / logview /
			// inspector / textview) hit-tests mouse events against its
			// own rect, so clicking is uniformly useful. The cost is the
			// terminal's native click-drag text selection, which mouse
			// reporting takes over — hold shift (or alt on iTerm2) to get
			// it back for a copy.
			Mouse: app.MouseClick,
		}),
		tea.WithAltScreen(),
	)
	_, err = prog.Run()
	return err
}

// keyBinding turns a configured global key into the binding app.Options
// wants. A zero Binding is the shell's "off" switch for each of these,
// so an empty key must produce one rather than a binding on "".
func keyBinding(k, help string) key.Binding {
	if k == "" {
		return key.Binding{}
	}
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, help))
}

// outputBinding is keyBinding for the console key.
func outputBinding(k string) key.Binding { return keyBinding(k, "output") }

// initialOr pre-fills a prompt field from the environment variable the
// prompt is keyed on, falling back to the config's `initial:`. Exporting
// the var is how you skip a boot prompt you've already answered.
func initialOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
		// Widget follows the data type, same rule the generated action
		// input form uses: Options means select, bool means toggle,
		// everything else is a text input.
		switch {
		case len(p.Options) > 0:
			initial := 0
			for j, o := range p.Options {
				if o == p.Default {
					initial = j
				}
			}
			fields[i] = form.Select(form.SelectOptions{
				Key:     p.Key,
				Label:   label,
				Options: append([]string(nil), p.Options...),
				Initial: initial,
			})
		case p.Type == "bool":
			fields[i] = form.Confirm(form.ConfirmOptions{
				Key:     p.Key,
				Label:   label,
				Initial: p.Default == "true",
			})
		case p.Mask:
			// Pre-fills from the environment like a text prompt does, so
			// `TOKEN=… tui-builder …` still skips the typing. The value
			// is masked either way, so pre-filling doesn't put it on
			// screen.
			fields[i] = form.Password(form.PasswordOptions{
				Key:         p.Key,
				Label:       label,
				Placeholder: p.Placeholder,
				Initial:     initialOr(p.Key, p.Default),
			})
		default:
			fields[i] = form.Text(form.TextOptions{
				Key:         p.Key,
				Label:       label,
				Placeholder: p.Placeholder,
				Initial:     initialOr(p.Key, p.Default),
			})
		}
	}

	m := &promptModel{
		form:  form.New(th.Form().With(fields)),
		mouse: mouse.NewTracker(0),
	}
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
	form form.Model
	w, h int
	// This form is its own tea program rather than a screen in the app
	// shell, so nothing upstream resolves raw events into mouse.Msg —
	// it keeps its own tracker to do the click-count bookkeeping the
	// shell would otherwise do.
	mouse     mouse.Tracker
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
	case tea.MouseMsg:
		msg = m.mouse.Track(x, time.Now())
	}
	next, cmd := m.form.Update(msg)
	m.form = next
	return m, cmd
}

func (m *promptModel) View() string {
	if m.w == 0 {
		return ""
	}
	// This model is its own tea program, so it roots the frame: one
	// generation per render, seeded at the terminal origin.
	geom.NextGen()
	m.form.SetRect(geom.New(0, 0, m.w-4, m.h-2))
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
