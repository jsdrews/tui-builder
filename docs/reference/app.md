# App, screens and layout

A config has four top-level blocks. `data:` is the data layer, read by
both `wrangl` and `tui-builder`. `tui:` is the presentation layer, read
only by `tui-builder`. `actions:` is the write side, kept apart from
`data:` so nothing that re-fetches can ever re-run a mutation.

```yaml
app:
  title: Pods
data:
  sources:
    pods: {type: http, url: http://localhost:8001/api/v1/pods, root: items, refresh: 5s}
tui:
  components:
    pods_table:
      type: table
      source: pods
      columns:
        - {title: Name,  value: metadata.name}
        - {title: Phase, value: status.phase}
  screen:
    layout: {component: pods_table}
actions:
  delete_pod:
    run: [kubectl, delete, pod, "${inputs.name}"]
    inputs: {name: {required: true}}
```

## Top level

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `app` | map | — | — | App shell settings. See [`app`](#app). |
| `data` | map | — | — | The data layer. Holds `sources:`, the single map of every leaf source and operator. See [`data`](#data). |
| `tui` | map | — | — | Components and screens. See [`tui`](#tui). Ignored by `wrangl`. |
| `actions` | map of action | — | — | Named units of work, bound to keys by screens. See [actions.md](actions.md). |

### `data`

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `sources` | map of source | — | — | Every data entry, keyed by name. Each entry's `type:` picks a leaf kind ([sources.md](sources.md)) or an operator ([pipelines.md](pipelines.md)). Components and `wrangl` address entries by these names. |

## `app`

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `title` | string | — | — | Prefixes the breadcrumb; the current screen's title follows it. |
| `version` | string | — | — | Shown on the right of the statusbar. |
| `theme` | string | first built-in | — | The starting palette. An unknown name falls back to the first theme. Names: `dark`, `accent`, `light`, `solarized`, `nord`, `dracula`, `gruvbox`, `catppuccin-mocha`, `catppuccin-latte`, `tokyo-night`, `rose-pine`, `rose-pine-dawn`, `one-dark`, `monokai`, `everforest-dark`, `base16-ocean`, `base16-eighties`, `base16-railscasts`, `base16-tomorrow-night`. |
| `glyphs` | map | — | — | Overrides the single-character marks components draw. See [`app.glyphs`](#appglyphs). |
| `borders` | map | — | — | Border shapes and how a pane title meets its border. See [`app.borders`](#appborders). |
| `actions_key` | string | `a` | — | Opens the action menu. `-` turns the menu off, which also turns off right-click targeting and menu shortcuts. No action may bind this key. |
| `theme_key` | string | `t` | — | Cycles through every built-in palette, live. `-` pins the app to `theme:`. No action may bind this key. |
| `output_key` | string | `o` | — | Opens the output console: every statusbar message and everything a subprocess streams, with a badge counting events and a picker for killing running work. `-` disables it. No action may bind this key. |
| `prompts` | list | — | — | A form shown at boot, before any screen renders. See [`app.prompts`](#appprompts). |
| `env` | list | — | — | Environment variables the config depends on, checked at load. See [`app.env`](#appenv). |

`glyphs:` and `borders:` apply to every palette, not just the one
`theme:` names: cycling themes changes color, never vocabulary.

### `app.glyphs`

Every field is optional; an unset one keeps tuilib's default.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `cursor` | string | tuilib's | — | Marks the focused row in list, logview and the action menu. |
| `mark` | string | tuilib's | — | Marks a selected row where `markable:` is on. |
| `expand_open` | string | tuilib's | — | Disclosure arrow for an open node in tree and inspector. |
| `expand_closed` | string | tuilib's | — | Disclosure arrow for a closed node. |
| `rule` | string | tuilib's | — | The horizontal line under an inline filter. |
| `scroll_thumb` | string | tuilib's | — | Vertical scrollbar thumb. |
| `scroll_track` | string | tuilib's | — | Vertical scrollbar track. |
| `h_scroll_thumb` | string | tuilib's | — | Horizontal scrollbar thumb. |
| `h_scroll_track` | string | tuilib's | — | Horizontal scrollbar track. |
| `sort_asc` | string | tuilib's | — | Follows the sorted column's title, ascending. |
| `sort_desc` | string | tuilib's | — | Follows the sorted column's title, descending. |
| `column_sep` | string | tuilib's | — | Divides table columns. |
| `placeholder` | string | tuilib's | — | Fills a row a windowed table hasn't received yet. |

**Validation:** each value must be exactly one character. A wider glyph
shifts every row it's drawn on, so it's rejected.

### `app.borders`

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `active` | string | `normal` | — | Border shape of the focused component. |
| `inactive` | string | `normal` | — | Border shape of unfocused components. Focus is signalled by color, so set this differently from `active` only if you want panes to change weight on focus. |
| `overlay` | string | `thick` | — | Border shape of what floats over a screen: the key overlay, confirm and alert dialogs, the action menu. |
| `slot_brackets` | string | `none` | — | How a pane's title meets the border line: `none` (`── title ──`), `corners` (`┐ title ┌`, reads as a tab), `tees` (`─┤ title ├─`). |

**Validation:** shapes are one of `normal`, `rounded`, `thick`,
`double`, `hidden`, `block`, `ascii`; `slot_brackets` is one of `none`,
`corners`, `tees`.

```yaml
app:
  glyphs: {cursor: ">", mark: "*"}
  borders: {active: rounded, inactive: rounded, overlay: double, slot_brackets: corners}
```

### `app.prompts`

An ordered list of fields collected in a form before the first screen
renders. Each value is written to the environment variable named by
`key`, so the rest of the config reads it as `${env.KEY}`. If that
variable is already set, the field starts with its value. Cancelling the
form exits the program.

A prompt is a `key` plus the same field vocabulary an action input uses
(see [actions.md](actions.md#inputs)).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `key` | string | — | required | The environment variable the value is written to. |
| `type` | string | `string` | — | The data type: `string`, `int`, `bool` or `duration`. It also picks the widget: `bool` is a yes/no toggle, anything else a text input, unless `options:` makes it a select. |
| `label` | string | the key | — | The field's caption. |
| `default` | string | — | not with `required` | The starting value. For `bool`, `"true"` / `"false"`; with `options:`, one of the options. |
| `required` | bool | `false` | not with `default` | The form won't submit while the field is empty. |
| `description` | string | — | — | One-line summary, shown by `wrangl --describe`. |
| `placeholder` | string | — | text inputs | Hint shown inside an empty text input. |
| `options` | list of string | — | not with `mask` | Turns the field into a select over these values. |
| `mask` | bool | `false` | not with `options` | Shows typed characters as bullets. Display only: the value is still the real string everywhere else. |
| `order` | int | `0` | — | Sort position in the form. Ties break alphabetically. |

**Validation:** `key` is required; `type` must be one of the four data
types; `required` and `default` are mutually exclusive; `mask` and
`options` conflict.

```yaml
app:
  prompts:
    - {key: GH_USER, label: GitHub user, required: true}
    - {key: SORT, options: [updated, stars], default: updated}
    - {key: GH_TOKEN, mask: true}
```

### `app.env`

Declares environment variables the config reads with `${env.NAME}`, so a
missing one fails at load with a clear message instead of a 401 or a
double-slash URL at first fetch. Applies to `wrangl` too.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `name` | string | — | required | The variable name, e.g. `AWX_TOKEN`. |
| `required` | bool | `false` | not with `default` | Load fails if the variable is unset. Every missing variable is reported in one message. |
| `default` | string | — | not with `required` | Set into the environment when the variable is unset. |
| `description` | string | — | — | Shown in the missing-variable error, so the user knows what to set. |

A `${env.X}` that is referenced, unset and not declared (here or as a
prompt `key`) prints a warning at load, not an error: an empty value is
legitimate for optional headers and flags.

```yaml
app:
  env:
    - {name: AWX_HOST, required: true, description: "Tower base URL, e.g. https://awx.example.com"}
    - {name: DEBUG, default: "0"}
```

## `tui`

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `components` | map of component | — | — | Every component, keyed by name. Layouts place them by name. See [components.md](components.md). |
| `screen` | screen | — | not with `screens` | Single-screen mode: the one screen the app shows. |
| `screens` | map of screen | — | not with `screen`; needs `initial` | Multi-screen mode: screens keyed by name, reached by push actions. |
| `initial` | string | — | required with `screens` | The name of the root screen in `screens:`. |

**Validation:** exactly one of `screen:` and `screens:` is set;
`initial:` names a defined screen.

### Screens

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `title` | template | — | — | Shown in the breadcrumb. On a pushed screen it can use `${selection…}` from the row that opened it. |
| `layout` | node | — | required | The root of the screen's layout tree. |
| `actions` | list of binding | — | — | Action bindings: which actions this screen offers, on which keys, fed by which component's selection. See [actions.md](actions.md#bindings). |

**Validation:** each component may be placed at most once per screen.

A pushed screen's sources can take values from the row that opened it,
either by `${selection…}` tokens in their URL, argv or body, or through
the push binding's `bind:`, which fills their `parameters:`. See
[actions.md](actions.md#push-actions).

```yaml
tui:
  initial: users
  screens:
    users:
      title: Users
      layout: {component: users_list}
      actions:
        - {key: enter, action: open_repos, from: users_list, bind: {user: "${selection}"}}
    repos:
      title: "${selection}'s repos"
      layout: {component: repos_table}
```

## Layout nodes

A node is exactly one of a vertical stack, a horizontal stack, an
overlay, or a component.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `vstack` | list of item | — | exactly one per node | Children stacked top to bottom. |
| `hstack` | list of item | — | exactly one per node | Children side by side, left to right. |
| `zstack` | map | — | exactly one per node | Draws `overlay` on top of `base`; both fill the node. |
| `component` | string | — | exactly one per node | A component name from `tui.components`. |

### Stack items

An item is a node plus a sizing hint.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `flex` | int | `1` | set one of `flex` / `fixed` | Share of the space left after fixed items. Used when neither is set. |
| `fixed` | int | — | set one of `flex` / `fixed` | Exact size in rows (vstack) or columns (hstack). |

### `zstack`

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `base` | node | — | required | The node underneath. |
| `overlay` | node | — | required | The node drawn on top. |

```yaml
layout:
  vstack:
    - fixed: 10
      component: summary
    - flex: 1
      hstack:
        - {flex: 2, component: pods_table}
        - {flex: 1, component: pod_logs}
```
