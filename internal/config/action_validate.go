package config

import (
	"fmt"
	"sort"
	"strings"
)

// reservedKeys are keys the running app already owns. Binding an action
// to one is always a config bug, but the two halves fail differently and
// both are silent, which is why this is a hard load error rather than a
// doc note.
//
// The app-shell keys (q / ctrl+c / ctrl+z / ? / esc) are consumed before
// the screen's Update ever sees them, so an action bound there simply
// never fires — the user presses the key and the app quits instead. The
// screen keys (tab / shift+tab / r) do reach us, and an action would
// shadow them, silently costing focus cycling or manual refresh.
//
// The output-console, theme-cycle and action-menu keys are NOT in this
// map: all three are configurable (`app.output_key`, `app.theme_key`,
// `app.actions_key`), so their reserved value depends on the config
// being validated. validateActionBindings takes them as arguments and
// reports them with the knob to change, which a static map can't do.
//
// Deliberately NOT reserved:
//
//   - enter: routed through activate(), the one direct key an action
//     still fires from. Binding it is the documented drilldown idiom;
//     every other key is a menu shortcut.
//   - j / k / /: component-internal keys. They only matter while a list
//     or table holds focus, and an action on a different pane has every
//     right to them. componentCapturing already keeps filter typing
//     from being hijacked.
//
// `t` used to be listed above as deliberately-not-reserved, on the
// grounds that tui-builder never set app.Options.ThemeKey so nothing
// consumed it. That was true when this branch was written and is not
// any more — main bound it, which is exactly why the note said the
// README's claim was false. It is now a configurable key like `o`.
var reservedKeys = map[string]string{
	"q":         "quits the app",
	"ctrl+c":    "quits the app",
	"ctrl+z":    "suspends the app",
	"?":         "opens the key overlay",
	"esc":       "pops the current screen",
	"tab":       "cycles focus forward",
	"shift+tab": "cycles focus backward",
	"r":         "refreshes every bound source",
}

// hoistInlineActions moves every inline action declaration up into
// c.Actions and rewrites its binding into a reference. After this runs,
// nothing downstream — validator, screen, wrangl — has to care whether
// an action was written inline or named: there is one registry and one
// lookup.
//
// Called from Load before Validate, so validation sees the hoisted shape.
func hoistInlineActions(c *Config) error {
	screens := map[string]*Screen{}
	if c.TUI.Screen.Layout.set() > 0 {
		screens["tui.screen"] = &c.TUI.Screen
	}
	for name, s := range c.TUI.Screens {
		if s != nil {
			screens["tui.screens."+name] = s
		}
	}
	// Deterministic order so a config with two colliding inline names
	// reports the same one every run.
	paths := make([]string, 0, len(screens))
	for p := range screens {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		s := screens[path]
		for i := range s.Actions {
			b := &s.Actions[i]
			if !b.IsInline() {
				continue
			}
			if b.Name == "" {
				return fmt.Errorf("%s.actions[%d]: an inline action needs `name:` (or use `action:` to reference one from the top-level actions: map)", path, i)
			}
			if _, taken := c.Actions[b.Name]; taken {
				return fmt.Errorf("%s.actions[%d]: inline action name %q is already defined in the top-level actions: map", path, i, b.Name)
			}
			if c.Actions == nil {
				c.Actions = map[string]*Action{}
			}
			hoisted := b.Inline
			c.Actions[b.Name] = &hoisted
			b.Action = b.Name
			b.Inline = Action{}
		}
	}
	return nil
}

// validateActionDefs checks every entry in the top-level actions: map.
// Runs after hoisting, so it covers inline declarations too.
func validateActionDefs(actions map[string]*Action) error {
	names := make([]string, 0, len(actions))
	for name := range actions {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		a := actions[name]
		path := "actions." + name
		if a == nil {
			return fmt.Errorf("%s: empty definition", path)
		}
		switch a.Kind() {
		case "exec":
			if len(a.Run) == 0 {
				return fmt.Errorf("%s: `run:` is required (non-empty argv) for an exec action", path)
			}
			if a.URL != "" || a.Method != "" || len(a.Headers) > 0 || a.Body != "" {
				return fmt.Errorf("%s: http fields (url / method / headers / body) are not valid on an exec action", path)
			}
		case "http":
			if a.URL == "" {
				return fmt.Errorf("%s: `url:` is required for an http action", path)
			}
			if len(a.Run) > 0 {
				return fmt.Errorf("%s: `run:` is not valid on an http action", path)
			}
		case "push":
			if a.Multi {
				return fmt.Errorf("%s: `multi: true` is meaningless on a push — a push replaces what is on top of the stack, so there is no second screen to open", path)
			}
			if a.Push == "" {
				return fmt.Errorf("%s: `push:` is required for a push action — name a screen in tui.screens", path)
			}
			if len(a.Run) > 0 || a.URL != "" {
				return fmt.Errorf("%s: a push action opens a screen; `run:` and `url:` are not valid on it", path)
			}
			if len(a.Inputs) > 0 {
				return fmt.Errorf("%s: a push action has no `inputs:` — the binding's `bind:` fills the DESTINATION screen's parameters, which the destination declares", path)
			}
		default:
			return fmt.Errorf("%s: unknown type %q (want exec, http or push)", path, a.Type)
		}
		for pname, p := range a.Inputs {
			if p == nil {
				return fmt.Errorf("%s.inputs.%s: empty definition", path, pname)
			}
			if p.Required && p.Default != "" {
				return fmt.Errorf("%s.inputs.%s: `required: true` and `default:` are mutually exclusive — a default makes the input optional", path, pname)
			}
			// The type picks the generated form's widget AND its
			// validator. A typo falls through to an unvalidated text
			// field, which is the same silent failure a mistyped
			// boot-prompt type used to have.
			switch p.Type {
			case "", "string", "int", "bool", "duration":
			default:
				return fmt.Errorf("%s.inputs.%s: unknown type %q (want string|int|bool|duration — the data type, not the widget: `options:` makes it a select and `mask: true` masks it)", path, pname, p.Type)
			}
			if p.Mask && len(p.Options) > 0 {
				return fmt.Errorf("%s.inputs.%s: `mask: true` and `options:` conflict — a select shows every choice on screen, so there is nothing to mask", path, pname)
			}
		}
	}
	return nil
}

// validateActionBindings checks one screen's action bindings against the
// action registry and the components actually placed in its layout.
func validateActionBindings(bindings []ActionBinding, refs map[string]int, components map[string]*Component, actions map[string]*Action, path, outputKey, themeKey, actionsKey string) error {
	seen := map[string]int{}
	for i, b := range bindings {
		bp := fmt.Sprintf("%s.actions[%d]", path, i)
		// `key:` is optional. A binding without one is reachable from
		// the action menu and nowhere else, which is the point of
		// having a menu: the alphabet stops being the ceiling on how
		// many verbs a screen can have, so a verb no longer has to earn
		// a letter to exist. Everything in this block is about a key
		// that exists.
		if b.Key != "" {
			if why, bad := reservedKeys[b.Key]; bad {
				return fmt.Errorf("%s: key %q is reserved — it %s, so an action bound to it would never fire", bp, b.Key, why)
			}
			// The two the shell claims but the config can move.
			if outputKey != "" && b.Key == outputKey {
				return fmt.Errorf("%s: key %q opens the output console (app.output_key) — pick another, or set app.output_key to \"-\" to disable the console", bp, b.Key)
			}
			if themeKey != "" && b.Key == themeKey {
				return fmt.Errorf("%s: key %q cycles the theme (app.theme_key) — pick another, or set app.theme_key to \"-\" to pin the palette", bp, b.Key)
			}
			if actionsKey != "" && b.Key == actionsKey {
				return fmt.Errorf("%s: key %q opens the action menu (app.actions_key) — pick another, or set app.actions_key to \"-\" to turn the menu off", bp, b.Key)
			}
			if prev, dup := seen[b.Key]; dup {
				return fmt.Errorf("%s: key %q already bound at %s.actions[%d]", bp, b.Key, path, prev)
			}
			seen[b.Key] = i
		}

		if b.Action == "" {
			return fmt.Errorf("%s: needs either `action:` (a name from the top-level actions: map) or an inline declaration with `name:`", bp)
		}
		a, ok := actions[b.Action]
		if !ok {
			return fmt.Errorf("%s: action %q is not defined in the top-level actions: map", bp, b.Action)
		}
		if a.Multi && b.Interactive != nil && *b.Interactive {
			return fmt.Errorf("%s: `interactive: true` and a multi action conflict — the TTY can only be handed to one process at a time", bp)
		}
		if a.Kind() == "http" && b.Interactive != nil && *b.Interactive {
			return fmt.Errorf("%s: `interactive: true` is meaningless for an http action — there is no terminal to hand over", bp)
		}

		// `from:` is derived, not declared: required exactly when a bind
		// template reads a selection, forbidden otherwise. Declaring it
		// when nothing consumes it would silently narrow the binding to
		// one focused pane, which is the surprising half of the old
		// required-`source:` behaviour.
		// A push always reads the selection, declared templates or not:
		// the focused row is what the drilldown is drilling into, and
		// the destination's title and sources see it as ${selection}.
		needsSel := a.Kind() == "push"
		for _, tmpl := range b.Bind {
			if strings.Contains(tmpl, "${selection") {
				needsSel = true
				break
			}
		}
		if strings.Contains(b.Confirm, "${selection") {
			needsSel = true
		}
		switch {
		case needsSel && b.From == "":
			return fmt.Errorf("%s: `from:` is required because a bind or confirm template reads ${selection.*} — name the list / table / tree whose focused row supplies it", bp)
		case !needsSel && b.From != "":
			return fmt.Errorf("%s: `from: %s` is set but nothing reads ${selection.*} — drop it, and the key fires regardless of which pane has focus", bp, b.From)
		}
		if b.From != "" {
			if refs[b.From] == 0 {
				return fmt.Errorf("%s: from %q not used in this screen's layout", bp, b.From)
			}
			switch components[b.From].Type {
			case "list", "table", "tree":
			default:
				return fmt.Errorf("%s: from %q must be a list, table or tree (got %s) — nothing else has a selected row", bp, b.From, components[b.From].Type)
			}
		}

		// There is deliberately no "required input is unfillable" check.
		// Every declared input is promptable — the form is generated from
		// the input's own Parameter fields — so an input the call site
		// doesn't bind is collected from the user instead. The failure
		// mode is designed out rather than detected.
		//
		// The reverse typo does need catching: binding a name the action
		// never declared would otherwise vanish silently.
		//
		// A push is the exception: its bind: fills the DESTINATION
		// screen's `parameters:`, which that screen's sources declare,
		// not this action's inputs. Those are checked at push time by
		// applyBindParams, which is the only place that knows what the
		// destination wants.
		for pname := range b.Bind {
			if a.Kind() == "push" {
				break
			}
			if _, declared := a.Inputs[pname]; !declared {
				return fmt.Errorf("%s: binds %q, which action %q does not declare as an input", bp, pname, b.Action)
			}
		}
	}
	return nil
}
