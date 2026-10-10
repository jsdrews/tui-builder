# tui-builder

Build terminal UIs by writing YAML. Wire HTTP APIs, commands, files and
WebSockets into tables, lists, trees and log panes; reshape the data with
filters, joins and projections; drill between screens; and bind actions
that change things, all from one config file.

```
┌ All pods (3 clusters merged) ────────────────────────────────────┐
│ Cluster      │ Namespace │ Name                    │ Status      │
├──────────────┼───────────┼─────────────────────────┼─────────────┤
│ pods_prod    │ default   │ nginx-56c45fd5ff-7p9mw  │ Running     │
│ pods_staging │ default   │ api-7d8b6c5b4-x2k7p     │ Running     │
│ pods_dev     │ default   │ broken-544795c8b5-5hxfg │ CrashLoop…  │
└──────────────────────────────────────────────────────────────────┘
```

## Two binaries, one config

- **`tui-builder`** renders a config as an interactive terminal UI.
- **`wrangl`** runs only the data layer of the same config and writes
  JSON or NDJSON to stdout, for `jq`, scripts and notebooks. It never
  loads any terminal-UI code.

The data is the product; the TUI is one place it goes.

## What a config holds

```yaml
app:      # title, theme, keys, boot prompts, required env vars
data:
  sources:  # where data comes from, and how it's reshaped
tui:
  components:  # how data is shown
  screen:      # how components are laid out (or `screens:` for several)
actions:  # what the user can do to the things on screen
```

## Where to go

<div class="grid cards" markdown>

- **[Quick start](quickstart.md)**: install, write a first config, add
  live data and an action.
- **[Guide](guide/app.md)**: how each part of a config works and fits
  together.
- **[Reference](reference/README.md)**: every field, its type, default
  and validation rules.
- **[CLIs](cli/tui-builder.md)**: `tui-builder` and `wrangl` usage.
- **[Examples](examples.md)**: every example config, ready to run.

</div>

tui-builder is built on [tuilib](https://github.com/jsdrews/tuilib), which
does the rendering and theming.
