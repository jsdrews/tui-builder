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

	"github.com/jsdrews/tuilib/pkg/action"
	"github.com/jsdrews/tuilib/pkg/app"
	tconfig "github.com/jsdrews/tuilib/pkg/config"
	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/geom"
	"github.com/jsdrews/tuilib/pkg/mouse"
	"github.com/jsdrews/tuilib/pkg/output"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	tqscreen "github.com/jsdrews/tui-builder/internal/screen"
	"github.com/jsdrews/tui-builder/internal/version"
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
	if os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Println("tui-builder version", version.String())
		return nil
	}

	c, err := cfg.Load(os.Args[1])
	if err != nil {
		return err
	}

	// app.glyphs / app.borders land on every palette, not just the one
	// app.theme names — cycling themes (`t`) walks the whole slice, and
	// the chrome vocabulary shouldn't change halfway through.
	themes := build.ApplyChrome(theme.All(), &c.App)
	// The keymap, form spacing and mask reach inside components and
	// forms, which every builder (and every theme-swap rebuild) reads.
	build.SetAppChrome(&c.App)
	// The user's own tuilib config file. A missing or unreadable file is
	// the normal case and means "no preferences".
	user, _ := tconfig.Load()
	initial := initialTheme(themes, os.Getenv(themeEnvVar), c.App.Theme, user.Theme)

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
		root, err = tqscreen.BuildMulti(c.TUI.Initial, multi, build.Selection{}, nil, initial)
	} else {
		root, err = tqscreen.Build(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, initial)
	}
	if err != nil {
		return err
	}

	shell := app.New(app.Options{
		Root:    root,
		Themes:  themes,
		Version: c.App.Version,
		// tui-builder resolves the initial theme itself (initialTheme)
		// so app.theme beats the user's config file; tuilib's own
		// resolution would put the file first.
		SkipConfig:          true,
		DoubleClickInterval: doubleClick(c.App.DoubleClick, user.DoubleClickMs),
		QuitKey:             keyBinding(c.App.QuitKeyName(), "quit", "ctrl+c"),
		HelpKey:             keyBinding(c.App.HelpKeyName(), "help"),
		SuspendKey:          keyBinding(c.App.SuspendKeyName(), "suspend"),
		DisableSuspend:      c.App.SuspendKeyName() == "",
		DisableAutoEscPop:   !c.App.EscPopsScreens(),
		Actions:             action.Options{Title: c.App.ActionsTitle},
		Output:              outputOptions(c.App.Output),
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
		// The action menu. One switch for the whole feature: the
		// overlay, the confirm modal, the goroutine, cancellation,
		// the console entry, the badge, the kill picker and the
		// per-target exclusivity check all come from the shell once
		// this is bound. The screen supplies verbs by implementing
		// action.Provider.
		ActionsKey: keyBinding(c.App.ActionMenuKey(), "actions"),
		// Every component we build (list / table / tree / logview /
		// inspector / textview) hit-tests mouse events against its
		// own rect, so clicking is uniformly useful. The cost is the
		// terminal's native click-drag text selection, which mouse
		// reporting takes over — hold shift (or alt on iTerm2) to get
		// it back for a copy. app.mouse: off hands it back for good.
		Mouse: mouseMode(c.App.Mouse),
	})
	var model tea.Model = shell
	if c.App.Title != "" {
		model = titled{Model: shell, title: c.App.Title}
	}
	prog := tea.NewProgram(model, tea.WithAltScreen())
	_, err = prog.Run()
	return err
}

// themeEnvVar picks the starting palette for one run, ahead of the config.
const themeEnvVar = "TUI_BUILDER_THEME"

// initialTheme picks the starting palette. Most specific wins: the person
// running it right now (env), then the config's author (app.theme), then
// the user's machine-wide default (their tuilib config file), then the
// first theme. An unknown name at any step falls through to the next.
func initialTheme(themes []theme.Theme, env, appTheme, userTheme string) theme.Theme {
	for _, name := range []string{env, appTheme, userTheme} {
		if name == "" {
			continue
		}
		if t, ok := theme.ByName(themes, name); ok {
			return t
		}
	}
	return themes[0]
}

// doubleClick is app.double_click, else the user's tuilib config file's
// double_click_ms, else zero (tuilib's default). Validation has already
// rejected an unparseable app.double_click.
func doubleClick(app string, userMs int) time.Duration {
	if d, err := time.ParseDuration(app); err == nil && d > 0 {
		return d
	}
	if userMs > 0 {
		return time.Duration(userMs) * time.Millisecond
	}
	return 0
}

// mouseMode maps app.mouse onto the shell's modes.
func mouseMode(m string) app.MouseMode {
	if m == "off" {
		return app.MouseOff
	}
	return app.MouseClick
}

// outputOptions carries app.output's non-visual knobs to the console;
// the shell fills in the theme's colors around them.
func outputOptions(o *cfg.OutputConfig) output.Options {
	if o == nil {
		return output.Options{}
	}
	return output.Options{MaxRecords: o.MaxRecords, ExportDir: o.ExportDir}
}

// titled sets the terminal window's title (app.title) when the program
// starts, and is otherwise the shell unchanged.
type titled struct {
	tea.Model
	title string
}

func (t titled) Init() tea.Cmd { return tea.Batch(t.Model.Init(), tea.SetWindowTitle(t.title)) }

func (t titled) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := t.Model.Update(msg)
	t.Model = m
	return t, cmd
}

// keyBinding turns a configured global key into the binding app.Options
// wants. A zero Binding is the shell's "off" switch for each of these,
// so an empty key must produce one rather than a binding on "".
func keyBinding(k, help string, also ...string) key.Binding {
	if k == "" {
		return key.Binding{}
	}
	return key.NewBinding(key.WithKeys(append([]string{k}, also...)...), key.WithHelp(k, help))
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
	m := &promptModel{
		form:  form.New(build.FormOptions(th).With(promptFields(prompts))),
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

// promptFields builds one form field per prompt. Split from
// collectAppPrompts so the fields can be checked without a terminal.
func promptFields(prompts []cfg.Prompt) []form.Field {
	fields := make([]form.Field, len(prompts))
	for i, p := range prompts {
		label := p.Label
		if label == "" {
			label = p.Key
		}
		// The same field an action input gets, so the widget, required
		// and validate behave identically. A text field pre-fills from
		// the environment.
		fields[i] = build.FormField(p.Key, label, p.Parameter, initialOr(p.Key, p.Default))
	}
	return fields
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
