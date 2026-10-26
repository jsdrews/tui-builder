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
| `presets` | map of preset | — | — | Named, partial leaf sources that sources `extends:` to share settings. A preset produces nothing and can't be bound or run. See [presets](sources.md#presets). |

## `app`

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `title` | string | — | — | The terminal window's title, set when the app starts. The breadcrumb shows each screen's own `title:`. |
| `version` | string | — | — | Shown on the right of the statusbar. |
| `theme` | string | first built-in | — | The starting palette. The `TUI_BUILDER_THEME` environment variable overrides it for one run; when neither is set, the `theme:` in your own `$XDG_CONFIG_HOME/tuilib/config.yaml` is used. An unknown name falls through to the next of those, then to the first theme. Names: `dark`, `accent`, `light`, `solarized`, `nord`, `dracula`, `gruvbox`, `catppuccin-mocha`, `catppuccin-latte`, `tokyo-night`, `rose-pine`, `rose-pine-dawn`, `one-dark`, `monokai`, `everforest-dark`, `base16-ocean`, `base16-eighties`, `base16-railscasts`, `base16-tomorrow-night`. |
| `surface` | string | `outlined` | — | The starting surface: how every pane's edge and body are drawn, independent of the palette. `outlined` (line borders, no fill), `filled` (solid cards), `banded` (a card under a coloured header band), `elevated` (a card with a drop shadow) or `accented` (a bar down the left edge). The `TUI_BUILDER_SURFACE` environment variable overrides it for one run; when neither is set, the `surface:` in your tuilib config file is used. Filled surfaces need 256 colours (and `filled` / `elevated` block glyphs) and fall back toward `outlined` on a terminal without them. |
| `colors` | string | `auto` | — | The colour depth: `auto` detects it from `TERM` and `COLORTERM`; `truecolor`, `256` or `16` forces it. For a terminal that supports more than it says, which is common inside tmux when `COLORTERM` isn't passed through. Forcing more than the terminal can show draws wrong colours. The `TUI_BUILDER_COLORS` environment variable overrides it for one run. It also decides whether `filled`, `banded` and `elevated` surfaces fall back. |
| `glyphs` | map | — | — | Overrides the single-character marks components draw. See [`app.glyphs`](#appglyphs). |
| `borders` | map | — | — | Border shapes and how a pane title meets its border. See [`app.borders`](#appborders). |
| `actions_key` | string | `a` | — | Opens the action menu. `-` turns the menu off, which also turns off right-click targeting and menu shortcuts. No action may bind this key. |
| `theme_key` | string | `t` | — | Cycles through every built-in palette, live. `-` pins the app to `theme:`. No action may bind this key. |
| `surface_key` | string | `T` | — | Cycles through every built-in surface, live, starting from `surface:`. `-` pins the app to `surface:`. No action may bind this key. |
| `output_key` | string | `o` | — | Opens the output console: every statusbar message and everything a subprocess streams, with a badge counting events and a picker for killing running work. `-` disables it. No action may bind this key. |
| `quit_key` | string | `q` | — | Quits from the root screen. `ctrl+c` always quits too. Can be rebound but not turned off. |
| `suspend_key` | string | `ctrl+z` | — | Suspends the app to the shell. `-` turns suspending off. |
| `help_key` | string | `?` | — | Opens the key overlay. Can be rebound but not turned off. |
| `mouse` | string | `click` | — | `click` makes components clickable; `off` leaves the mouse to the terminal, so click-drag selects text without holding shift. |
| `double_click` | duration | your tuilib config's `double_click_ms`, else tuilib's | — | How quickly a second click must follow to count as a double click. |
| `esc_pops` | bool | `true` | — | `esc` goes back one screen. `false` leaves going back to actions. |
| `output` | map | — | — | The output console's scrollback cap and export directory. See [`app.output`](#appoutput). |
| `actions_title` | string | `Actions` | — | The action menu's title. |
| `action_groups` | list of string | — | — | The order of the action menu's group headings, e.g. `[read, write]`. Groups it doesn't list follow in the order a screen's bindings name them. Each name must be a group some binding falls under (its `group:`, or `read` / `write` by default); a name no binding uses, or one listed twice, is a load error. |
| `form_spacing` | int | `0` | — | Blank lines between fields in action-input forms and boot prompts. |
| `keys` | map | — | — | Remaps navigation keys in every list, table and tree. See [`app.keys`](#appkeys). |
| `prompts` | list | — | — | A form shown at boot, before any screen renders. See [`app.prompts`](#appprompts). |
| `env` | list | — | — | Environment variables the config depends on, checked at load. See [`app.env`](#appenv). |

`glyphs:` and `borders:` apply to every palette, not just the one
`theme:` names: cycling themes changes color, never vocabulary.

### `app.glyphs`

Every field is optional; an unset one keeps tuilib's default. They apply everywhere, including the key overlay, the action menu and the output console, and stay the same as palettes and surfaces cycle.

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
| `mask` | string | tuilib's | — | Drawn for each typed character in a masked input or prompt. |
| `activity_ok` | string | tuilib's | — | Shown when a row's work finished. |
| `activity_fail` | string | tuilib's | — | Shown when a row's work failed. |
| `activity_changed` | string | tuilib's | — | Shown when a row's work finished and changed it. |

**Validation:** each value must be exactly one character. A wider glyph
shifts every row it's drawn on, so it's rejected.

### `app.borders`

The line shapes are the `outlined` surface's: they apply whenever it is drawn, whether chosen, cycled to, or fallen back to from another surface. `slot_brackets` applies on every surface.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `active` | string | `normal` | — | Border shape of every component, focused or not; focus is shown by colour. |
| `inactive` | — | — | — | No longer supported, and a load error: every pane is drawn with the same lines. Set `active`. |
| `overlay` | string | `thick` | — | Border shape of what floats over a screen: the key overlay, confirm and alert dialogs, the action menu. |
| `slot_brackets` | string | `none` | — | How a pane's title meets the border line: `none` (`── title ──`), `corners` (`┐ title ┌`, reads as a tab), `tees` (`─┤ title ├─`). |

**Validation:** shapes are one of `normal`, `rounded`, `thick`,
`double`, `hidden`, `block`, `ascii`; `slot_brackets` is one of `none`,
`corners`, `tees`; `inactive` can't be set.

```yaml
app:
  glyphs: {cursor: ">", mark: "*"}
  borders: {active: rounded, overlay: double, slot_brackets: corners}
```

### `app.output`

The console's colors come from the theme; these are its other settings.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `max_records` | int | tuilib's | — | How many entries the console keeps before dropping the oldest. |
| `export_dir` | string | tuilib's | — | Where the console writes an exported log. |

### `app.keys`

Remaps navigation keys in every list, table and tree, by binding name.
Each value is one key or a list of keys, and replaces that binding's
default keys. There are no per-component keymaps: a remap is a habit,
not a per-pane choice.

| Binding | Default | On |
|---|---|---|
| `up`, `down` | `↑`/`k`, `↓`/`j` | list, table, tree |
| `top`, `bottom` | `g`, `G` | list, table, tree |
| `half_up`, `half_down` | `ctrl+u`, `ctrl+d` | list, table |
| `filter` | `/` | list, table; on a tree, its `/` search |
| `mark`, `mark_all`, `clear_marks`, `mark_range` | `x`, `A`, `D`, `X` | list, table, tree with `markable:` |
| `sort_prev`, `sort_next`, `sort_dir` | `[`, `]`, `s` | table |
| `col_prev`, `col_next` | `shift+left`, `shift+right` | table |

```yaml
app:
  keys:
    up: [k, up]
    down: [j, down]
    filter: /
```

**Validation:** names must be from the table; each needs at least one
key; a key can't be one the shell or the screen already owns (the
quit, suspend, help, console, theme and menu keys, `ctrl+c`, `esc`,
`tab`, `shift+tab`, `r`).

### Shell keys

**Validation:** `quit_key` and `help_key` can't be `-`. Two shell
features can't share a key, and none can be `esc`, `tab`, `shift+tab` or
`r`. `mouse` is `click` or `off`; `double_click` is a positive duration;
`form_spacing` and `output.max_records` can't be negative.

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
| `required` | bool | `false` | not with `default` | The form won't submit while the field is empty. On a select, nothing starts highlighted, so the answer is picked on purpose. |
| `description` | string | — | — | One-line summary, shown by `wrangl --describe`. |
| `placeholder` | string | — | text inputs | Hint shown inside an empty text input. |
| `options` | list of string | — | not with `mask` | Turns the field into a select over these values. |
| `mask` | bool | `false` | not with `options` | Shows typed characters as bullets. Display only: the value is still the real string everywhere else. |
| `order` | int | `0` | — | Sort position in the form. Ties break alphabetically. |
| `validate` | expr | — | — | An expression over `value`, true when the answer is acceptable; the form won't submit until it is. See [action inputs](actions.md#inputs). |
| `invalid` | string | `not accepted` | `validate` | The message shown when `validate` is false. |

**Validation:** `key` is required; `type` must be one of the four data
types; `required` and `default` are mutually exclusive; `mask` and
`options` conflict; `validate` must compile, and `invalid` needs it.

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
| `from` | map | — | not with `default` | Reads the value from a file or a command when the variable is unset. See [values from a file or a command](#values-from-a-file-or-a-command). |

A `${env.X}` that is referenced, unset and not declared (here or as a
prompt `key`) prints a warning at load, not an error: an empty value is
legitimate for optional headers and flags.

```yaml
app:
  env:
    - {name: AWX_HOST, required: true, description: "Tower base URL, e.g. https://awx.example.com"}
    - {name: DEBUG, default: "0"}
```

#### Values from a file or a command

`from:` gives a variable a value without exporting it first: a token
kept in a file, or one a password manager prints.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `file` | string | — | not with `command` | A file whose contents are the value. `~` and `${env.X}` expand; a relative path is relative to the config file. |
| `command` | list of string | — | not with `file` | A program and its arguments, run without a shell; what it prints is the value. It shares the terminal's input and error output, so it can ask for a password. |
| `timeout` | duration | `60s` | `command` | How long the command may run. |

- A value already in the environment wins, and its `from:` doesn't run.
- Entries are read once, in list order, so a `file:` path can use an
  earlier entry through `${env.X}`.
- The value is trimmed. An empty value, a missing file, a failed command
  or a timeout is a failure: with `required: true` the program stops
  with an error naming the variable and the file or command; otherwise
  it prints a warning and the variable stays unset. The value itself is
  never printed.
- `tui-builder` reads them before [`app.prompts`](#appprompts), so a
  prompt with the same `key` starts with the value. `wrangl` reads them
  only when it fetches a target, not for `--list`, `--describe` or the
  action listings. Loading a config on its own never runs a command.

**Validation:** exactly one of `file` / `command`; not with `default`
(`required` is fine); `command` needs a program; `timeout` is a positive
duration and only applies to `command`.

```yaml
app:
  env:
    - {name: API_TOKEN, required: true, from: {file: ~/.config/myapi/token}}
    - {name: DB_PASS, from: {command: [pass, show, db], timeout: 90s}}
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
| `layout` | node | — | one of `layout` / `tabs` | The root of the screen's layout tree. |
| `actions` | list of binding | — | not with `tabs` | Action bindings: which actions this screen offers, on which keys, fed by which component's selection. See [actions.md](actions.md#bindings). |
| `tabs` | list | — | one of `layout` / `tabs` | Makes this a tabbed screen. See [tabs](#tabs). |

**Validation:** each component may be placed at most once per screen (per
tab, on a tabbed screen).

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

### Tabs

A screen can hold tabs instead of one layout. Each tab is a small screen
of its own, with a label, a layout and actions, shown one at a time under
a strip.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `label` | string | — | required | The tab's name in the strip. Unique within the screen. |
| `layout` | node | — | required | The tab's layout tree. |
| `actions` | list of binding | — | — | The tab's own bindings; `from:` names a component in this tab. |

The strip is set on the screen, beside `tabs:`:

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `strip_pos` | string | `top` | `tabs` | `top` or `bottom`. |
| `initial` | string | the first tab | `tabs` | The label of the tab the screen opens on. |
| `prev_key` | string | `shift+left` | `tabs` | Switches to the previous tab. `-` turns it off. |
| `next_key` | string | `shift+right` | `tabs` | Switches to the next tab. `-` turns it off. |
| `number_keys` | bool | `true` | `tabs` | `1`–`9` jump straight to a tab. |

Keys and clicks go to the active tab; `tab` / `shift+tab` move focus
within it. A tab first fetches when it's first opened, then keeps
polling while hidden, and switching back doesn't refetch. Each tab keeps
its own sources, so a source bound in two tabs is fetched twice, as on
two pushed screens; put a `cache` operator in front to share one fetch.
The breadcrumb shows the screen's `title:`, since switching tabs isn't
moving between screens. A push from inside a tab keeps the tabs
underneath it.

```yaml
tui:
  screen:
    title: Cluster
    initial: Services
    tabs:
      - label: Pods
        layout: {component: pods_table}
        actions:
          - {key: enter, action: describe_pod, from: pods_table, bind: {name: "${selection}"}}
      - label: Services
        layout: {component: services_table}
```

**Validation:** `tabs:` and `layout:` are exclusive, and a tabbed screen
has no `actions:` of its own. Each tab needs a unique `label` and a
layout. `initial` must be a label, `strip_pos` is `top` or `bottom`, and
a switch key can't be one the shell or the screen already owns. The
strip fields are errors without `tabs:`. The same component may appear in
two tabs; each builds its own copy.

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
| `fixed` | int | — | set one of `flex` / `fixed` | Exact size in rows (vstack) or columns (hstack). An input or toggle in a vstack is always `3` rows. |

**Validation:** setting both `flex` and `fixed` on one item is an error.

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
