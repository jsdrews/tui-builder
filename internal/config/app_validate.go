package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// shellKey is a key the app shell claims for the whole run, so no action
// binding or keymap entry may use it.
type shellKey struct {
	key  string
	does string // "quits the app"
	fix  string // what to change instead
}

// shellKeys lists the keys the shell claims under this config, with the
// defaults applied. ctrl+c always quits; the rest follow their knob.
func (a *App) shellKeys() []shellKey {
	out := []shellKey{{"ctrl+c", "quits the app", "pick another key"}}
	add := func(k, does, fix string) {
		if k != "" {
			out = append(out, shellKey{k, does, fix})
		}
	}
	add(a.QuitKeyName(), "quits the app (app.quit_key)", "pick another, or move app.quit_key")
	add(a.SuspendKeyName(), "suspends the app (app.suspend_key)", "pick another, or set app.suspend_key to \"-\" to turn suspending off")
	add(a.HelpKeyName(), "opens the key overlay (app.help_key)", "pick another, or move app.help_key")
	add(a.OutputConsoleKey(), "opens the output console (app.output_key)", "pick another, or set app.output_key to \"-\" to disable the console")
	add(a.ThemeCycleKey(), "cycles the theme (app.theme_key)", "pick another, or set app.theme_key to \"-\" to pin the palette")
	add(a.SurfaceCycleKey(), "cycles the surface (app.surface_key)", "pick another, or set app.surface_key to \"-\" to pin the surface")
	add(a.ActionMenuKey(), "opens the action menu (app.actions_key)", "pick another, or set app.actions_key to \"-\" to turn the menu off")
	return out
}

// validateShell checks the app shell's own settings: its keys, mouse,
// output console, form spacing and the app-wide keymap.
func validateShell(a *App) error {
	if a.QuitKey == "-" {
		return fmt.Errorf("app.quit_key: can't be turned off — rebind it instead (ctrl+c also quits)")
	}
	if a.Colors != "" && !slices.Contains(ColorNames, a.Colors) {
		return fmt.Errorf("app.colors: unknown value %q (want %s)", a.Colors, strings.Join(ColorNames, "|"))
	}
	if a.Surface != "" && !slices.Contains(SurfaceNames, a.Surface) {
		return fmt.Errorf("app.surface: unknown surface %q (want %s)", a.Surface, strings.Join(SurfaceNames, "|"))
	}
	if a.HelpKey == "-" {
		return fmt.Errorf("app.help_key: can't be turned off — rebind it instead (the overlay is where every binding is listed)")
	}
	// Two shell features on one key: whichever the shell checks first
	// wins and the other silently never fires.
	shell := a.shellKeys()
	for i, x := range shell {
		for _, y := range shell[:i] {
			if x.key == y.key {
				return fmt.Errorf("app: key %q both %s and %s", x.key, y.does, x.does)
			}
		}
	}
	for _, sk := range shell {
		if why, bad := reservedKeys[sk.key]; bad {
			return fmt.Errorf("app: key %q %s, but it already %s", sk.key, sk.does, why)
		}
	}
	switch a.Mouse {
	case "", "click", "off":
	default:
		return fmt.Errorf("app.mouse: unknown mode %q (want click|off)", a.Mouse)
	}
	if a.DoubleClick != "" {
		d, err := time.ParseDuration(a.DoubleClick)
		if err != nil || d <= 0 {
			return fmt.Errorf("app.double_click: %q is not a positive duration (e.g. 400ms)", a.DoubleClick)
		}
	}
	if a.Output != nil && a.Output.MaxRecords < 0 {
		return fmt.Errorf("app.output.max_records: can't be negative")
	}
	if a.FormSpacing < 0 {
		return fmt.Errorf("app.form_spacing: can't be negative")
	}
	return validateKeymap(a, shell)
}

// validateKeymap checks `app.keys`: known names, at least one key each,
// and no key the shell or the screen already owns.
func validateKeymap(a *App, shell []shellKey) error {
	names := make([]string, 0, len(a.Keys))
	for n := range a.Keys {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !slices.Contains(KeymapNames, n) {
			return fmt.Errorf("app.keys.%s: unknown binding (want one of %s)", n, strings.Join(KeymapNames, ", "))
		}
		keys := a.Keys[n]
		if len(keys) == 0 {
			return fmt.Errorf("app.keys.%s: needs at least one key", n)
		}
		for _, k := range keys {
			if k == "" {
				return fmt.Errorf("app.keys.%s: empty key", n)
			}
			if why, bad := reservedKeys[k]; bad {
				return fmt.Errorf("app.keys.%s: key %q is reserved — it %s", n, k, why)
			}
			for _, sk := range shell {
				if k == sk.key {
					return fmt.Errorf("app.keys.%s: key %q %s — %s", n, k, sk.does, sk.fix)
				}
			}
		}
	}
	return nil
}
