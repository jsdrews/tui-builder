# tui-builder

Renders a config as an interactive terminal UI.

```sh
tui-builder <config.yaml>
```

It takes one argument, the config file, and no flags.

## What happens at startup

1. The config is loaded and validated. Any schema error (an unknown
   field value, a missing required field, a bad reference) stops here
   with a message naming the field.
2. `app.env` is checked: a missing required variable stops the load,
   defaults are applied, and undeclared-but-referenced variables print a
   warning.
3. If `app.prompts` is set, its form is shown. Each answer is set as an
   environment variable; cancelling exits.
4. `${env.*}` is substituted across the config.
5. The first screen (`tui.screen`, or `tui.initial` from `tui.screens`)
   is built and its sources start fetching.

## Environment

- Any variable the config reads as `${env.NAME}`. A boot prompt whose
  variable is already set starts pre-filled, so
  `GH_USER=octocat tui-builder repos.yaml` lets you skip straight
  through the form.

## Keys and mouse

The shell's keys are listed in [Guide → App](../guide/app.md#the-app-shell);
`?` shows every key the focused component accepts.

The mouse focuses panes and moves cursors, the wheel scrolls, a double
click is `enter`, and a right click opens the action menu on the row
under the pointer. Hold `shift` (or `alt` in iTerm2) while dragging to
select text for copying.

## Exit status

`0` on a normal quit (`q`); `1` with the error on stderr if the config
fails to load or the program fails to start.

## Related

- [`wrangl`](wrangl.md) runs the same config's data layer without a UI.
- `example-launcher` (or `task examples` in a clone) lists every example
  and runs the one you pick.
