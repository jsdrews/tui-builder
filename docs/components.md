# Components — what tui-builder exposes vs. tuilib capability

tui-builder binds a subset of [tuilib](https://github.com/jsdrews/tuilib) to a
YAML schema. This doc lists every component currently supported, the
schema fields available for each, and the gaps between the schema and the
underlying tuilib component.

Legend: ✓ exposed · ⚠ partially exposed · ✗ not exposed

---

## Round 1 — initial gap analysis

This is the state of the schema **before** the round of additions for
styled cells, initial values, logview, and tree.

### `list` (`pkg/list`)

| Tuilib feature | Exposed? |
|---|---|
| `Title`, `Items`, `Filterable` | ✓ |
| `LoadingLabel` + `SetLoading(true)` | ✗ |
| `SetKeyedItems` (stable-cursor refresh) | ✗ |
| Initial cursor / pre-filter value | ✗ |
| Per-list border style / slot brackets | ✗ |
| Color overrides (selected/active/inactive) | ✗ |
| `HScrollbar` toggle | ✗ |
| Pane loading spinner style | ✗ |

### `table` (`pkg/table`)

| Tuilib feature | Exposed? |
|---|---|
| `Title`, `Columns`, `Rows`, `Filterable` | ✓ |
| `Column.Width` / `Flex` / `MaxWidth` / `Align` / `Sortable` | ✓ |
| `Column.Less` | ⚠ string only (default lex) |
| `SetKeyedRows` (stable cursor) | ✗ |
| `LoadingLabel` / `SetLoading` | ✗ |
| Initial sort column + direction | ✗ |
| Initial cursor row | ✗ |
| Filter placeholder text | ✗ |
| `Borders.Vertical` / `HeaderRule` glyphs | ✗ |
| Header / Selected / Cell style overrides | ✗ |
| `HScrollbar` toggle | ✗ |
| Per-cell `ansi.CellColor` (status cells) | ✗ |
| `ansi.Hyperlink` (clickable URL cells) | ✗ |

### Layout (`pkg/layout`)

| Tuilib feature | Exposed? |
|---|---|
| `VStack`, `HStack`, `ZStack`, `Fixed`, `Flex` | ✓ |
| `layout.Center(naturalW, naturalH, child)` | ✗ |
| `layout.Bar(...)` | n/a — no bar-shaped components yet |

### App shell (`pkg/app`)

| Tuilib feature | Exposed? |
|---|---|
| `Title`, `Version`, `Theme` | ✓ |
| `QuitKey`, `ThemeKey` custom bindings | ✗ |
| `ThemeEnvVar` / `SkipConfig` / `DisableAutoEscPop` | ✗ |
| Inline custom themes | ✗ |
| `theme.Terminal()` | ✗ |

### Cross-cutting (not in schema)

- Multi-screen / stack
- `input`, `form`, `logview`, `tree`, `inspector`, `toggle`,
  `confirm`, `alert`, `tab`, `metrics`, `runner`, `poll`,
  `breadcrumb` components
- Data sources
- Component-to-component bindings

---

## Round 2 — after this round

This round added:

- **Per-cell styling** for tables — colored values and OSC 8 hyperlinks.
- **Sort modes** for table columns — `string` / `number` / `si`.
- **Initial state** — cursor, filter value, table sort, filter placeholder,
  logview/tree query, tree expansion depth.
- **`logview`** component — streaming-log pane with `/`-search, n/N
  jump, `\` filter mode, follow-the-tail.
- **`tree`** component — hierarchical view with expand/collapse, search,
  filter mode.

Below is the current state of each component.

### `list` (`pkg/list`)

| Tuilib feature | Exposed? | Schema field |
|---|---|---|
| `Title`, `Items`, `Filterable` | ✓ | `title`, `items`, `filterable` |
| Initial cursor | ✓ | `initial_cursor` |
| Initial filter value | ✓ | `initial_filter` |
| Filter placeholder | ✓ | `filter_placeholder` |
| `LoadingLabel` + `SetLoading(true)` | ✗ | needs data sources |
| `SetKeyedItems` (stable-cursor refresh) | ✗ | needs data sources |
| Border style / slot brackets | ✗ | theme default only |
| Color overrides (selected/active/inactive) | ✗ | theme only |
| `HScrollbar` toggle | ✗ | theme enables by default |
| Spinner style | ✗ | theme only |

### `table` (`pkg/table`)

| Tuilib feature | Exposed? | Schema field |
|---|---|---|
| `Title`, `Columns`, `Rows`, `Filterable` | ✓ | `title`, `columns`, `rows`, `filterable` |
| `Column.Width` / `Flex` / `MaxWidth` / `Align` / `Sortable` | ✓ | per-column |
| `Column.Less` | ⚠ | `sort: string \| number \| si` |
| Per-cell `ansi.CellColor` | ✓ | `{value: X, color: red}` |
| `ansi.Hyperlink` | ✓ | `{label: X, url: …}` |
| Initial cursor | ✓ | `initial_cursor` |
| Initial filter value | ✓ | `initial_filter` |
| Filter placeholder | ✓ | `filter_placeholder` |
| Initial sort | ✓ | `initial_sort: {column, desc}` |
| `SetWindow` + `FilterRemote` / `SortRemote` | ✓ | implicit — binding `source:` to an entry with `window:` switches the table into windowed mode. See [Windowed sources](data-layer.md#windowed-sources) |
| `SetDistinct` (filter hints from a facet endpoint) | ✗ | hints still come from resident rows |
| `SetKeyedRows` (stable cursor) | ✗ | needs data sources |
| `LoadingLabel` / `SetLoading` | ✗ | needs data sources |
| `Borders.Vertical` / `HeaderRule` glyphs | ✗ | theme default only |
| Header / Selected / Cell style overrides | ✗ | theme only |
| `HScrollbar` toggle | ✗ | theme enables by default |
| Date / custom comparator | ✗ | extend `sort:` modes |

### `logview` (`pkg/logview`)  — new

| Tuilib feature | Exposed? | Schema field |
|---|---|---|
| `Title`, `Searchable`, `MaxLines`, `FilterMode` | ✓ | `title`, `searchable`, `max_lines`, `filter_mode` |
| Initial lines | ✓ | `lines: [...]` |
| Initial query | ✓ | `initial_query` |
| Filter placeholder | ✓ | `filter_placeholder` |
| `MatchStyle` / `CurrentLineStyle` overrides | ✗ | theme only |
| Border / pane overrides | ✗ | theme only |
| Streaming append (live) | ✗ | needs data sources |
| `SetLoading` / `LoadingLabel` | ✗ | needs data sources |

### `inspector` (`pkg/inspector`)  — new

| Tuilib feature | Exposed? | Schema field |
|---|---|---|
| `Title`, `Filterable`, `InitialDepth` | ✓ | `title`, `filterable`, `initial_depth` |
| Recursive `Fields` (label/value/children) | ✓ | `fields: [{label, value, children}]` |
| Initial query | ✓ | `initial_query` |
| Initial cursor | ✓ | `initial_cursor` |
| Filter placeholder | ✓ | `filter_placeholder` |
| `FromAny` / `FromMap` (JSON → Fields) | ✗ | needs data sources |
| `MatchStyle` / `CurrentLineStyle` overrides | ✗ | theme only |
| `SetLoading` / `LoadingLabel` | ✗ | needs data sources |
| Custom Keys | ✗ | tuilib default keymap |

### `tree` (`pkg/tree`)  — new

| Tuilib feature | Exposed? | Schema field |
|---|---|---|
| `Title`, `Searchable`, `InitialDepth` | ✓ | `title`, `searchable`, `initial_depth` |
| Root + Children (recursive Node interface) | ✓ | `root: {label, children: [...]}` |
| Initial query | ✓ | `initial_query` |
| Initial cursor | ✓ | `initial_cursor` |
| Filter placeholder | ✓ | `filter_placeholder` |
| `MatchStyle` / `CurrentLineStyle` overrides | ✗ | theme only |
| Custom row data beyond Label | ✗ | tuilib's `Node` only exposes Label/Children for the schema's purposes |
| `SetLoading` / `LoadingLabel` | ✗ | needs data sources |

### Layout (`pkg/layout`)

Unchanged from round 1. `layout.Center` lands when modal components
(`confirm` / `alert`) enter the schema.

### App shell (`pkg/app`)

| Tuilib feature | Exposed? | Schema field |
|---|---|---|
| `Title`, `Version`, `Theme` | ✓ | `app.title`, `app.version`, `app.theme` |
| `HelpVerbose` (legacy inline footer) | ✓ | `app.help_verbose` (default false → minimal "? help" footer; press `?` for full panel) |
| `QuitKey`, `ThemeKey`, `HelpKey` custom bindings | ✗ | defaults locked |
| `HelpMaxRows` (cap expanded help panel) | ✗ | defaults to 6 |
| `ThemeEnvVar` / `SkipConfig` / `DisableAutoEscPop` | ✗ | |
| Inline custom themes | ✗ | |
| `theme.Terminal()` | ✗ | |

---

## Remaining cross-cutting gaps

| Area | Status |
|---|---|
| Multi-screen / stack | Not yet — v2 |
| Other components: `input`, `form`, `inspector`, `toggle`, `confirm`, `alert`, `tab`, `metrics`, `runner`, `poll`, `breadcrumb` | Out of scope until they have a binding partner (data source, dedicated screen, or modal layer) |
| Data sources | Project goal — pending |
| Component-to-component binding | Lands with data sources |
| Theming: inline custom palette, `theme.Terminal()` | Not yet |
| App-shell knobs: `QuitKey`, `ThemeKey`, `ThemeEnvVar` | Not yet |
| `LoadingLabel` / `SetLoading` everywhere | Lands with data sources |

## Schema cheat sheet (current)

> **Note**: this cheat sheet is grouped by field category for
> readability. In the actual config, the `sources:` map lives under
> `data:` and `components:` / `screen[s]:` / `initial:` live under
> `tui:`. Every entry in `data.sources:` carries a `type:` that
> picks its kind — leaf sources (http / exec / file / websocket /
> static / merge) and operator pipelines (filter / project / derive
> / sort / union / compose / join / cache / passthrough) share the
> single map. See [data-layer.md](data-layer.md#top-level-config-shape)
> for the top-level shape.

```yaml
app:
  title: <string>             # breadcrumb prefix
  version: <string>           # statusbar right
  theme: <name>               # one of theme.All() names
  help_verbose: <bool>        # true = legacy inline footer, false (default) = minimal "? help"
  output_key: <key>           # opens the output console — scrollback of every
                              # statusbar message and everything a subprocess
                              # streams, with a statusbar badge counting events
                              # and a picker ("x") for killing what's running.
                              # Default "o"; set "-" to disable. No action may
                              # bind this key — the shell claims it globally,
                              # so the validator rejects the collision.
  glyphs:                     # optional — the single-character marks components
                              # draw. Every field is optional and an unset one
                              # keeps tuilib's default, so overriding one arrow
                              # doesn't blank the other twelve. Each value must
                              # be exactly one character: a two-character cursor
                              # shifts every row it's drawn on.
    cursor:         <char>    # focused row in list / logview / action menu
    mark:           <char>    # marked row where multi-select is enabled
    expand_open:    <char>    # tree + inspector disclosure arrows
    expand_closed:  <char>
    rule:           <char>    # horizontal line under an inline filter
    scroll_thumb:   <char>    # vertical scrollbar
    scroll_track:   <char>
    h_scroll_thumb: <char>    # horizontal scrollbar
    h_scroll_track: <char>
    sort_asc:       <char>    # follows the active column's title in a table
    sort_desc:      <char>
    column_sep:     <char>    # divides table columns
    placeholder:    <char>    # fills a row a windowed table hasn't received yet
  borders:                    # optional — border shapes. Unset keeps tuilib's
                              # default: normal for components, thick for overlays.
    active:   normal | rounded | thick | double | hidden | block | ascii
    inactive: <same>          # defaults to matching `active`'s shape upstream:
                              # focus is signalled by border *color*, so a pane
                              # that changed weight on focus would move the eye
                              # for a reason the user didn't ask about
    overlay:  <same>          # confirm + alert dialogs and the action menu —
                              # what floats above a screen. The output console
                              # is a pushed screen, so it takes `active`.
    slot_brackets: none | corners | tees
                              # how a pane's title meets the border line:
                              #   none     ── title ──   (default)
                              #   corners  ┐ title ┌     reads as a labelled tab
                              #   tees    ─┤ title ├─
                              # Both blocks apply to EVERY palette, not just the
                              # one `theme:` names — cycling themes with `t`
                              # shouldn't change the chrome vocabulary midway.
  prompts:                    # optional — boot-time params collected via a form modal
                              # BEFORE the main screen renders. Each value is set as
                              # an env var keyed by `key`, so ${env.<KEY>} works
                              # downstream. Pre-fills from any existing env var of
                              # the same name. Cancel (esc) aborts the program.
    - key:   <string>         # env var name + token key
      label: <string>         # field label shown in the form
      type:  text | password | select | confirm   # default text
      placeholder:   <string> # text / password only
      initial:       <string> # text / password default (overridden by current env if set)
      options:       [<string>, ...]   # select choices
      initial_index: <int>    # select default index
      initial_bool:  <bool>   # confirm default

sources:                      # optional — components bind to these via `source:`
  <name>:
    type: http | exec | file | websocket | static | merge | passthrough | filter | project | derive | sort | union | compose | join | cache
    # ---- shared by every type ----
    root: <dot-path>          # slice into the response (empty = use whole result)
    format: json | text       # default json; use text for plain-text endpoints
                              # (kube pod logs etc.) — required for logview bindings
                              # against non-JSON sources
    refresh: <duration>       # polling interval (e.g. "5m"); omit / 0s = fetch once
    timeout: <duration>       # default 10s for http/exec, n/a for file/merge

    # ---- http ----
    url: <string>             # may contain ${selection.*}/${env.*}
    method: GET               # default
    headers: {<k>: <v>, ...}
    body: <string>            # sent as-is

    # ---- exec ----
    # Runs a subprocess; one-shot (parse stdout) or streaming
    # (line-by-line follow). Inherits the program's env plus any `env:`
    # overrides on top.
    command: [<argv>, ...]    # first element looked up in $PATH
    env:   {<k>: <v>, ...}
    follow: <bool>            # default false. true: stream mode — process
                              # is long-running, stdout lines arrive as
                              # Events into a bound logview. Covers
                              # `kubectl logs -f`, `tail -f`,
                              # `journalctl -f`, `docker logs -f`.

    # ---- file ----
    # Reads a file from disk on each fetch.
    path: <string>            # e.g. ./examples/fixtures/x.json

    # ---- websocket ----
    # Opens a long-lived connection; each text frame is delivered as
    # an Event to a bound logview. Headers apply on the initial HTTP
    # upgrade request (good for `Authorization: Bearer …` auth).
    # initial_messages are text frames sent immediately after the
    # connection upgrades — required by protocols (bitstamp, coinbase,
    # kraken, many custom buses) where the server doesn't emit until
    # the client subscribes. ${selection.*} / ${env.*} substitute per
    # entry; frames are sent in order.
    url: <ws-or-wss-url>
    headers: {<k>: <v>, ...}
    initial_messages:
      - <string>          # e.g. '{"event":"bts:subscribe","data":{"channel":"..."}}'

    # ---- merge ----
    # Fans out to N children concurrently, unions their results into a
    # []any. Children can be any source type (including other merges).
    sources:  [<name>, ...]   # references other top-level source names
    tag_field: <string>       # optional — injects {<TagField>: <child-name>}
                              # into every map-shaped item so downstream
                              # bindings can identify which child a row
                              # came from. Non-map items pass through.
    on_error: fail | skip     # default fail. skip: drop failed children
                              # and return the surviving union (only
                              # errors when EVERY child fails).
    # merge auto-detects whether its children are streaming. When ALL
    # children implement Subscribe (e.g. websocket / exec follow),
    # merge fans events from every child into one channel — events
    # arrive as they happen, no polling. When any child can't stream,
    # merge falls back to the polling Fetch path (existing behavior).
    # The two modes share `tag_field` / `on_error` semantics.

components:
  <name>:
    type: list | table | logview | tree | inspector | textview
    title: <string>
    source: <data-source-name>  # optional — populates the component dynamically;
                                # static items/rows/fields are ignored when set
    item: <dot-path>            # list: where to pluck each display string (when source: is set)
    color_rules:                # list / logview only — applies to every item/line.
                                # (table → per Column, inspector → per InspectorField.)
      - {when: <string>, color: <color>}
    colors:                     # optional — per-component color overrides, all fields optional.
                                # Values accept:
                                #   named colors (red, bright_green, gray, ...)
                                #   0-255 indices ("160")
                                #   hex ("#ff8800")
                                #   theme tokens: theme:accent | theme:current | theme:muted |
                                #                 theme:subtle | theme:key | theme:bar-bg |
                                #                 theme:bar-fg | theme:border-active |
                                #                 theme:border-inactive | theme:info-bg |
                                #                 theme:info-fg | theme:error-bg | theme:error-fg
                                # Theme tokens re-resolve every time the user cycles themes (`t`),
                                # so chrome stays consistent across palettes.
      border_active:   <color>  # all components
      border_inactive: <color>  # all components
      spinner:         <color>  # all components
      selected:        <color>  # list
      header:          <color>  # table
      selected_fg:     <color>  # table
      selected_bg:     <color>  # table
      cell:            <color>  # table
      column_separator: <color> # table
      header_rule:     <color>  # table
      label:           <color>  # inspector
      value:           <color>  # inspector
      match:           <color>  # inspector / logview / tree
      current_line_bg: <color>  # inspector / logview / tree

    # list / table / logview / tree — filter knobs
    filterable: <bool>        # list, table — '/' filter
    searchable: <bool>        # logview, tree — '/' search
    filter_placeholder: <string>
    initial_filter: <string>  # list/table — pre-populate filter value
    initial_query:   <string> # logview/tree — pre-populate search query
    initial_cursor:  <int>    # list, table, tree

    # list
    items: [string, ...]

    # table
    max_rows: <int>            # streaming-table only: ring buffer size
                               # (default 100). Each arriving JSON frame
                               # prepends a row; oldest trimmed past this.
                               # -1 = unbounded. Ignored when row_key is set.
    row_key: <dot-path>        # streaming-table only: switches table to
                               # KEYED UPSERT mode (L1 / status-grid pattern).
                               # Each event identifies its row via this path;
                               # matching keys update in place, new keys
                               # append. Without row_key, streaming tables
                               # work as a prepend+trim ring buffer.
    columns:
      - title: <string>
        width: <int>           # 0=auto, >0=fixed
        flex: <int>            # leftover-share weight
        max_width: <int>       # flex cap
        align: left | right | center
        sortable: <bool>
        sort: string | number | si
        value: <dot-path>      # when source: is set — pluck this cell from each item
        hidden: <bool>         # optional — omit from render + width computation.
                               # Row payload still carries the cell so
                               # RowFocusedMsg.Cells / SelectedRow expose it —
                               # `${cursor.<Title>}` and `${selection.<Title>}`
                               # still resolve. Filter matching still hits it
                               # (both bare terms and `key:value` scopes).
                               # Use for identity columns you need for
                               # drilldown/reactive binding but don't want
                               # to consume screen real estate.
        color_rules:           # optional — data-driven cell coloring; rules eval in order,
                               # first match wraps the cell with ansi.CellColor.
                               # `when:` syntax:
                               #   ""            wildcard (terminal default rule)
                               #   "Running"     exact case-insensitive string match
                               #   "~^Run"       case-insensitive regex
                               #   ">5" / ">=2M" numeric comparison (K/M/B/G/T suffix ok);
                               #                 operators: > >= < <= == !=
                               # `color:` accepts the same values as colors.* above
                               # (named, 0-255, hex, theme:token).
          - {when: <string>, color: <color>}
    rows:
      - [<cell>, <cell>, ...]   # cell is string OR
                                # {value: <string>, color: <name|0-255>} OR
                                # {label: <string>, url: <string>}
    initial_sort: {column: <title-prefix|1-based-index>, desc: <bool>}

    # logview
    lines:       [string, ...]
    max_lines:   <int>          # 0=default 10000, -1=unbounded
    filter_mode: <bool>

    # textview (static-text viewer — SetContent on refresh, no follow)
    content:     <string>        # optional — initial buffer for static
                                 # mode; overridden by SetContent when
                                 # source: is set.
    wrap:        <bool>          # optional — default false; runtime
                                 # toggle via `w`.
    searchable:  <bool>          # optional — /-search, n/N navigate

    # tree (static — declare the root inline)
    root:
      label: <string>
      children: [<TreeNode>, ...]
    initial_depth: <int>         # 0=root only, 1=root expanded, ...
    # tree (source-bound — data-driven, live updates via SetRoot)
    source:     <name>           # data.sources.<name>
    label:      <dot-path>       # each node's display label (required when source: set)
    children:   <dot-path>       # optional — recursive walk: dot-path
                                 # on each node pointing at its list of
                                 # children. Enables nested source shapes
                                 # (filesystem trees, k8s owner refs,
                                 # org charts). Mutually exclusive with
                                 # group_by.
    group_by:   <dot-path>       # optional — bucket the flat iterable
                                 # by this value; buckets become parent
                                 # nodes labeled with the bucket value.
                                 # kubectl-shape: `group_by: kind` renders
                                 # resources categorized by type.
    root_label: <string>         # optional — root node's display label
                                 # (supports ${selection.*}/${env.*}/
                                 # ${prompt.*}). Defaults to `title:`,
                                 # then to the source name.

    # inspector
    fields:
      - label: <string>
        value: <string>            # optional — empty for header rows; static
        path:  <dot-path>          # optional — when source: is set, overrides value
        color_rules:               # optional — same syntax as Column.color_rules
          - {when: <string>, color: <color>}
        children: [<InspectorField>, ...]
    auto: <bool>                   # optional — when true, skip `fields:` and derive
                                   # the field tree from the fetched value directly.
                                   # Requires source:. Nested maps expand into
                                   # Children; arrays get [0], [1] labels; scalars
                                   # render naturally. Mutually exclusive with fields:.
    initial_depth: <int>           # shared with tree

    # ── Reactive binding (any source-bound component) ─────────────────
    # Wire a driver's cursor state to another component's source. Every
    # focus-change message from the driver rebinds the target's
    # parameterized source via the Bind map and refetches through a
    # params-aware LRU (feature G's ParamCache). Enables the
    # "kubectl describe on hover" pattern without pushing a new screen.
    #
    # Valid driver kinds: `table`, `list`, `tree`. All three emit
    # tuilib focus-change messages (RowFocusedMsg / SelectedChangedMsg).
    on_cursor:
      source: <driver-component>   # table / list / tree in the same layout
      bind:                        # target source's params <- driver cursor
        <param>: <template>        # ${cursor.*} + ${env.*} substituted;
                                   # ${selection.*} passes through literal
                                   # (this fires mid-screen, not at push time).

# Single-screen mode:
screen:
  title: <string>
  layout: <Node>

# OR multi-screen mode (mutually exclusive with `screen:`):
screens:
  <name>:
    title: <string>            # may contain ${selection} when pushed
    layout: <Node>
    on_key:                    # optional — a keypress on Source pushes Push
      - source: <component>    # list or table
        push:   <screen-name>  # destination screen name
        key:    <string>       # REQUIRED — spell out the trigger key.
                               # `enter` for the classic drilldown; any
                               # tea.KeyMsg string works (`d`, `l`,
                               # `ctrl+r`, ...). Multiple bindings on
                               # the same source are allowed if their
                               # (source, key) pairs differ.
                               # A double click on a row is the mouse
                               # spelling of `enter`, so an `enter`
                               # binding is reachable both ways.
        label:  <string>       # optional — custom help-strip label.
                               # Defaults to "open".
        bind:                  # optional — templated params forwarded to
                               # the pushed screen's parameterized sources.
          <param>: ${selection.*}
    actions:                   # optional — bind a key to a subprocess
                               # (kubectl exec, $EDITOR, open, ...) via pkg/runner
      - key:         <string>  # dispatch key (avoid q/t/?/tab/esc/r//j/k)
                               # `enter` is allowed — a double click on a row
                               # fires it too. If the same source also has an
                               # on_key enter push, the push wins.
        label:       <string>  # shown in the help strip
        source:      <component>  # which list/table's selection feeds ${selection.*}
        confirm:     <string>  # optional yes/no modal message before dispatch
                               # (${selection.*}/${env.*}/${prompt.*} substituted)
        notice:      <string>  # optional banner during slow handoffs (interactive only)
        interactive: <bool>    # default true (uses pkg/runner — TTY handoff, brief
                               # flicker, right for vim/ssh/kubectl exec).
                               # false runs cmd.Run() in a goroutine, captures
                               # stdout/stderr, never suspends — right for one-shot
                               # commands (delete/scale/open). Errors surface in
                               # the alert; success goes to the statusbar.
        prompts:               # optional — collect input via a form modal BEFORE
                               # dispatch. Values feed ${prompt.<key>} into run argv
                               # AND confirm message AND notice. Same field shapes
                               # as app.prompts. Cancel from the form aborts the
                               # action. Field shapes per `type:`:
                               #
                               # text (default):
                               #   - {key: <string>, label: <string>,
                               #      placeholder: <string>, initial: <string>}
                               #
                               # password (text, rendered masked — for tokens
                               # and anything else you'd rather not type in the
                               # clear on a shared screen. Substitution sees the
                               # real value):
                               #   - {key: <string>, label: <string>,
                               #      placeholder: <string>, initial: <string>}
                               #
                               # select (selection popup — good for "pick a target
                               # before running" or gating destructive commands
                               # behind an explicit choice; scope, environment,
                               # container, replica-count, etc.):
                               #   - {key: <string>, label: <string>, type: select,
                               #      options: [<string>, ...],
                               #      initial_index: <int>}
                               #
                               # confirm (yes/no toggle — value is "true" or
                               # "false" when substituted):
                               #   - {key: <string>, label: <string>, type: confirm,
                               #      initial_bool: <bool>}
        run:         [<argv...>] # ${selection.*} + ${env.*} + ${prompt.*} substituted
                               # at fire time (after any prompts have been collected)
                               #
                               # Confirming destructive actions: `confirm:` is a
                               # yes/no modal shown AFTER prompts and BEFORE run.
                               # Reference ${prompt.*} in the message to include
                               # the collected values in the preview — e.g.
                               # "Delete pod ${selection.Name} in ${prompt.namespace}?"
initial: <screen-name>         # required when `screens:` is set

# Template tokens (substituted in titles, URLs, headers, body, items,
# values, paths — anywhere a string lives in the schema):
#
#   ${selection}            — list source: selected item;
#                             table source: first cell
#   ${selection.N}          — table source: 1-based cell index
#   ${selection.COLNAME}    — table source: cell by column-title prefix
#   ${cursor}               — LIVE: the driver's current focus.
#                             Table:  first cell of the focused row
#                             List:   the focused item's string
#                             Tree:   the focused node's label
#                             Refetches when the driver's cursor moves.
#                             Only meaningful on components that declare
#                             `on_cursor:` (the driver names which
#                             component's cursor to follow).
#   ${cursor.N}             — 1-based index into the driver's cells.
#                             Table:  cell by column position
#                             List:   `${cursor.1}` = the item
#                             Tree:   path[N-1] (root at .1)
#   ${cursor.COLNAME}       — Table:  cell by column title (case-
#                             insensitive exact match).
#                             List:   `${cursor.item}` (Columns=["item"]).
#                             Tree:   n/a — trees have no columns; use
#                             .N indexing or the special-cased keys.
#   ${cursor.label}         — Tree/List: explicit alias for bare ${cursor}.
#   ${cursor.depth}         — Tree: number of path elements (0 for root,
#                             1 for its children, ...) as a string.
#                             Table/List: length of Cells (usually 1
#                             for lists; the row width for tables).
#   ${cursor.path}          — Tree: Cells joined with "/" — the actual
#                             filesystem-shape path from root to the
#                             focused node ("./cmd/wrangl/main.go"). Fed
#                             directly to shell tools like `stat`.
#                             Table/List: same join; usually not what
#                             you want but handy for logging.
#                             Unresolved lookups resolve to "" (not the
#                             literal token) since cursor moves are
#                             high-frequency and a broken URL would
#                             404 storm.
#   ${env.NAME}             — os.Getenv("NAME") (empty when unset).
#                             Use for tokens / API keys / boot-time params
#                             (app.prompts values are written to env, so
#                             they share this namespace).
#   ${prompt.KEY}           — value collected from an action's `prompts:`
#                             form. Substituted at action-fire time, after
#                             the form submits.
#
# Selection tokens substitute at push time; env tokens substitute at push
# time too (boot params already in env by that point); prompt tokens
# substitute at action-fire time; cursor tokens substitute at every
# RowFocusedMsg from the declared driver — the reactive path.

# Node is one of:
#   {vstack: [<Item>, ...]}
#   {hstack: [<Item>, ...]}
#   {zstack: {base: <Node>, overlay: <Node>}}
#   {component: <name>}     # leaf — refers to components map
#
# Item is a Node + sizing hint:
#   {flex: <int>,  vstack|hstack|zstack|component: ...}
#   {fixed: <int>, ...}
```

## Example index

| File | Demonstrates |
|---|---|
| `examples/list.yaml` | Filterable list, navigation keys |
| `examples/table.yaml` | Filterable + sortable table, filter syntax |
| `examples/table_columns.yaml` | Column sizing (fixed / auto / flex / max_width) + alignment |
| `examples/table_styled.yaml` | Colored cells + clickable hyperlinks, `initial_sort` |
| `examples/logview.yaml` | Streaming-log pane, `/`-search, filter mode, `initial_query` |
| `examples/textview.yaml` | Static-text viewer (`type: textview`): a help pane using inline `content:` + a source-bound `date` clock refreshing every 3s. `/`-search, `w` wrap toggle |
| `examples/tree.yaml` | Hierarchical view, expand/collapse, search, `initial_depth` |
| `examples/tree_source.yaml` | Data-driven tree: a `type: file` source of people bucketed by `group_by: team`; cursor + expand state survive `refresh: 5s` polling |
| `examples/inspector.yaml` | Two-column label/value record viewer, nested groups |
| `examples/inspector_auto.yaml` | `auto: true` — inspector derives fields from any JSON response (GitHub repo record). Nested maps/arrays expand instead of stringifying |
| `examples/on_cursor.yaml` | On-hover detail: GitHub repos table on top, `on_cursor:`-bound inspector below. Scrolling the table refetches the detail pane via `${cursor.*}` tokens + params-aware cache. Uses a hidden `Owner` column for identity binding |
| `examples/on_cursor_fs.yaml` | Same pattern but tree-driven on a real filesystem: `tree -J ./cmd` walks a recursive JSON tree via `children: contents`; `${cursor.path}` joins the ancestor labels into a real path and pipes it to `stat` in a textview |
| `examples/table_wide.yaml` | Wide table demonstrating horizontal scroll (`←`/`→`, `shift+←`/`shift+→`, `0`/`$`) |
| `examples/layout.yaml` | Nested layouts, mixed flex weights |
| `examples/themes.yaml` | Built-in theme picker reference |
| `examples/chrome.yaml` | `app.glyphs` + `app.borders` — ASCII-safe glyph vocabulary, rounded panes, a double-bordered overlay and `slot_brackets: corners`. Press `t` to confirm the chrome survives a palette swap |
| `examples/multi.yaml` | Multi-screen drilldown (Regions → Cities → Detail) with breadcrumbing and `${selection}` substitution |
| `examples/on_key_push.yaml` | `on_key:` block — GitHub users list where `key: enter` pushes to repos and `key: s` pushes to starred, both binding `${selection}`; the repos table then pushes a repo-detail inspector, binding `${selection.Repo}` |
| `examples/http_countries.yaml` | Table backed by restcountries.com REST API; `refresh: 5m` polling; per-column `value:` dot-paths |
| `examples/http_github.yaml` | Multi-screen drilldown over the GitHub API: users → repos (via `/users/${selection}/repos`) → repo inspector (via `/repos/${selection.Repo}`). Shows URL templating from list and table selections |
| `examples/http_github_auth.yaml` | Authenticated GitHub: `/user/starred` → repo inspector. Uses `${env.GITHUB_TOKEN}` in the Authorization header — token stays out of YAML |
| `examples/http_refresh.yaml` | Live crypto prices via CoinGecko, `refresh: 10s`. Watch the `Updated` column flip every cycle; filter/sort/cursor survive each refresh. Press `r` to refetch on demand |
| `examples/colors.yaml` | Per-component `colors:` overrides across list / table / inspector — different token per pane to show what each field affects |
| `examples/kube.yaml` | Kubernetes namespaces → pods → pod inspector + tailing logview. Talks to `http://localhost:8001` (run `kubectl proxy --port=8001` first). Uses `format: text` + logview binding for the log tail |
| `examples/exec_local.yaml` | `type: exec` — runs `git log` (via sh) and renders the last 20 commits in a table. Demonstrates how any JSON-emitting CLI becomes a tui-builder source |
| `examples/file_fixture.yaml` | `type: file` — reads `examples/fixtures/people.json` with `refresh: 5s`; edit the file in another window and watch the table update |
| `examples/merge_sources.yaml` | `type: merge` — composes a file + two exec sources into a single table with `tag_field: source`. Same primitive composes cross-cluster / cross-account / cross-anything |
| `examples/stream_exec.yaml` | `type: exec` with `follow: true` — long-running subprocess; stdout lines stream into a logview as they arrive. Substitute `kubectl logs -f`, `tail -f`, etc. for the demo's tick loop |
| `examples/stream_websocket.yaml` | `type: websocket` — connects on activate; each text frame appends to a logview. Headers handle auth on the upgrade request |
| `examples/stream_trades_table.yaml` | Same websocket stream as above, but feeding a **live table** with `max_rows: 100`. Each JSON frame projects into a row via column `value:` paths and prepends to a ring buffer. Plus a side-by-side logview showing raw frames + connection state |
| `examples/stream_l1.yaml` | **L1 ticker JOINED from two streams**: Binance.us bookTicker (fast bid/ask) + @ticker (slower last-price + 24h stats) merged by symbol via `row_key: data.s`. Deep-merge keeps both sources' fields alive on each row. Demonstrates streaming + merge + keyed upsert together |
| `examples/prompts_boot.yaml` | **Boot-time params via `app.prompts`**: form modal collects GitHub username + sort field + an archived-repos toggle + a masked token before the main screen renders. Values become env vars and feed `${env.USER}` into the URL, the title, and a column |
| `examples/action_prompts.yaml` | **Action prompts**: keys fire a form modal that collects values before the action's subprocess dispatches. `${prompt.<key>}` substitutes into run argv + confirm message + notice |
| `examples/kube_multi.yaml` | Multi-cluster: 3 kube clusters merged into one pods table via `type: merge`. Tagged + colored by cluster. Use `task kube:multi:up && task kube:multi:proxy:all && task kube:multi:demo` |
| `examples/demo.yaml` | Kitchen-sink: list + table side-by-side |
