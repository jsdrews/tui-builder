# Adopting every tuilib v0.35 component

tui-builder is already on tuilib `v0.35.0`. This plan isn't about a bump:
it gives every tuilib package, and every `Options` field on the
components we already wrap, a verdict. It's locked: every decision was
made on the map
[Map: adopting every tuilib v0.35 component](https://github.com/jsdrews/tui-builder/issues/47),
and each workstream links the ticket that holds its detail. Eleven PRs,
in the order at the bottom.

The facts behind it are two research notes, kept on their branches:

- [Inventory: tuilib v0.35 surface vs what tui-builder exposes](https://github.com/jsdrews/tui-builder/issues/48)
  ([`research/tuilib-0.35-inventory`](https://github.com/jsdrews/tui-builder/blob/research/tuilib-0.35-inventory/plans/research/tuilib-0.35-inventory.md)).
  12 of 38 packages were never imported, though most run inside the app
  shell or inside components we build. Every wrapped component has
  options we don't expose.
- [Docs audit: where the zensical site and reference are stale](https://github.com/jsdrews/tui-builder/issues/49)
  ([`research/docs-audit`](https://github.com/jsdrews/tui-builder/blob/research/docs-audit/plans/research/docs-audit.md)).
  The site builds clean; the gaps are in values, rules and prose.

## How verdicts were reached

- **Default to adopt.** Something is left out only if it's plumbing with
  no YAML meaning, or AGENTS.md rule 1 (YAML is the API) or rule 2 (thin
  wrapper) argues against it. A package doesn't need a use case first.
- **Code is the source of truth** where docs and code disagree, unless
  the code's behaviour is a design bug; then the code changes to match
  the docs
  ([Docs vs code disagreements: which side is right](https://github.com/jsdrews/tui-builder/issues/53)).
- Each item gets one of: **adopt** (schema decided below), **adopt
  internally** (no YAML), **already covered**, **never expose** (with a
  reason), or **deferred**.

## Package verdicts

| Package | Verdict | Workstream |
|---|---|---|
| `tab` | adopt: screen-level `tabs:` | 7 |
| `metrics` | adopt: `metric:` on columns and inspector fields | 6 |
| `input`, `toggle` | adopt: layout components that drive source params | 8 |
| `resume` | adopt: `resume:` on exec and websocket streams | 10 |
| `poll` | adopt internally: replaces our refresh ticker | 11 |
| `breadcrumb`, `statusbar`, `alert` | already covered: drawn by the app shell, no options of their own | — |
| `output` | already covered; console options in Workstream 3 | 3 |
| `config` | already covered; its precedence changes in Workstream 3 | 3 |
| `filter` | already covered: inside list, table, eventlog via `filterable:` | — |
| `mouse` | already covered; `app.mouse` / `app.double_click` in Workstream 3 | 3 |
| `geom`, `ansi`, `glyph` | already covered (`glyph` through `app.glyphs`) | — |

Every other package was already wrapped; their unexposed options are
covered by Workstreams 3–5 and 9.

---

## Workstream 1 — this plan and the docs fixes

This PR. Merging it locks the plan.

- `plans/tuilib-0.35.md` (this file). `plans/tabs.md` is deleted: it
  assumed `pkg/tab` holds components, but each tab body is a whole
  screen (Workstream 7 replaces it).
- Every gap from the docs audit, on the code's side of each
  disagreement:
  - `paginate.max_pages`: `0` or unset means 20; there is no unlimited.
  - `pipe:` works on leaves and `passthrough` only; other operators
    chain with `from:`.
  - Action `from:` accepts an eventlog; a span table can be `markable`.
  - Expression contexts gain `activity.busy_when` and
    `window.growing.while`; functions gain `parseTime`.
  - `${window.*}` in Anchored http bodies; `${selection}` on an eventlog.
  - `sort_debounce` and `window.follow_every` in the durations list.
  - Anchored http windows may carry the filter in the body; `anchor:`
    needs `cursor:`.
  - Eventlog in the guide's filterable/searchable lists; the reference
    README lists eventlog and activity.
  - `data-layer.md` documents the windowed and Anchored interfaces and
    how windowed sources fetch.
  - The guide says component state survives a theme swap, and lists
    `ctrl+c` / `ctrl+z`.
  - `example-launcher -dir`.
- The two disagreements that are design bugs (`flex` + `fixed`, colour
  rules after a theme swap) keep their docs; Workstream 2 fixes the
  code.
- **Wider coverage tests:**
  - `internal/config`: every value in a code registry is named on its
    reference page: component kinds (new `ComponentKinds` registry,
    now used by validation), leaf and operator kinds, border shapes,
    slot brackets.
  - `internal/expr`: every built-in this package adds to expressions is
    named on `reference/templating.md`.
  - `cmd/wrangl`: every flag (and shorthand) is on `cli/wrangl.md`.
  - Template tokens aren't checked: there's no single registry to read
    them from. Row content (types, defaults) isn't checked either; the
    test would just copy the docs.

## Workstream 2 — correctness fixes

Each of these is a design bug: the docs describe the right behaviour.

- **`flex` and `fixed` both set** on a layout item is a validation error
  (today `fixed` silently wins).
  ([Docs vs code disagreements](https://github.com/jsdrews/tui-builder/issues/53))
- **Colour rules follow a theme swap.** Each component keeps its last
  applied data, and `Rebuild` re-runs `ApplyData` with the new theme
  before restoring state. Today `theme:` tokens in `color_rules` keep the
  old palette until the next fetch, which on a static source is forever.
  ([Docs vs code disagreements](https://github.com/jsdrews/tui-builder/issues/53))
- **A binding's `key:` becomes a menu shortcut.** `menuAction` sets
  `action.Action.Key` for every key except `enter`, so the menu shows it
  and runs it while open. A non-`enter` key that is one of the menu's
  own keys (`j`, `k`, `up`, `down`, `g`, `G`, `ctrl+u`, `ctrl+d`, `esc`)
  is a load error, and the reserved-keys table lists them as reserved
  inside the menu. Fix the stale "Key is the trigger" comment in
  `internal/config/action.go`.
  ([Action binding key: doesn't fire](https://github.com/jsdrews/tui-builder/issues/54))
- **Boot prompts wire `required:`** (and, after Workstream 5,
  `validate:`). The docs already show `required: true` on prompts.
  ([Form, confirm and action menu option gaps](https://github.com/jsdrews/tui-builder/issues/59))

**Tests.** flex+fixed rejected; a static table's coloured cell changes
on `SetTheme`; a `key: d` binding runs from the open menu and `key: j` is
rejected; a required prompt can't be submitted empty.

## Workstream 3 — app-level options

Decided in
[App shell options: what app: exposes](https://github.com/jsdrews/tui-builder/issues/55),
plus the app-wide pieces of
[List, table and tree option gaps](https://github.com/jsdrews/tui-builder/issues/56)
and
[Form, confirm and action menu option gaps](https://github.com/jsdrews/tui-builder/issues/59).

```yaml
app:
  title: My tool          # sets the terminal window title (tea.SetWindowTitle)
  quit_key: q             # rebindable; "-" is an error
  suspend_key: ctrl+z     # "-" → DisableSuspend
  help_key: "?"           # rebindable; "-" is an error
  mouse: click            # click | off
  double_click: 400ms     # beats the tuilib config file's double_click_ms
  esc_pops: true          # false → DisableAutoEscPop
  output: {max_records: 1000, export_dir: ~/tui-logs}
  actions_title: Actions  # the action menu's title
  form_spacing: 1         # blank lines between form fields
  keys:                   # app-wide keymap for every list, table and tree
    up: [k, up]
    filter: /
  glyphs:
    mask: "•"
    activity_ok: "✓"
    activity_fail: "✗"
    activity_changed: "~"
```

- **Theme precedence** becomes: `TUI_BUILDER_THEME` env var (tuilib's
  `ThemeEnvVar`) > `app.theme` > the user's
  `$XDG_CONFIG_HOME/tuilib/config.yaml` > the default. Today the user
  file beats `app.theme`. The file still supplies the theme when
  `app.theme` is unset, and still supplies `double_click_ms`.
- **`app.keys` names:** `up`, `down`, `top`, `bottom`, `half_up`,
  `half_down`, `filter`, `mark`, `mark_all`, `clear_marks`, `mark_range`,
  `sort_prev`, `sort_next`, `sort_dir`, `col_prev`, `col_next`. No
  per-component keymaps.
- **Validation:** rebound keys update the reserved-keys table and its
  checks; an `app.keys` value that clashes with a reserved or app key is
  an error; `quit_key: "-"` and `help_key: "-"` are errors.
- **Never expose:** `HelpVerbose` and `DisableHelpSearch` (the help
  overlay always stays fully featured), `output.SourceWidth` (layout),
  and the console's colours (theme).

**Tests.** Each key maps to its tuilib field; theme precedence in all
four combinations; `app.title` emits the window-title command; `app.keys`
reaches a table's keymap and rejects a clash.

## Workstream 4 — component options

Decided in
[List, table and tree option gaps](https://github.com/jsdrews/tui-builder/issues/56)
and
[Logview and eventlog option gaps](https://github.com/jsdrews/tui-builder/issues/57).

| tuilib | YAML | On |
|---|---|---|
| `Revision` | `activity.revision: <path>` | list, table, tree |
| `MarkStyle` | `colors.mark` | list, table, tree |
| `LoadingLabel` | `loading_label:` (`${env.*}`, `${selection.*}`) | list, table, tree |
| `SetDistinct` | `columns[].distinct: [values]` or `distinct: {source: <name>}` | columns of windowed tables only |
| `LineNumbers` / `GutterStyle` | `line_numbers: true` / `colors.gutter` | logview |
| `NoGutter` | `gutter: false` | eventlog |
| `NewFor` | `new_for: <duration>` (`"-"` = never) | eventlog |
| eventlog styles | `colors.selected_fg`, `selected_bg`, `cell`, `gutter`, `new` | eventlog |

- **`distinct: {source:}`**: the named source returns a list of strings
  and is fetched alongside the table. `distinct` anywhere but a windowed
  table's column is an error (a local filter recomputes candidates from
  rows anyway).
- **Logview `AppendMarker`, adopted with no YAML:** the screen appends
  "— stream ended —" when a bound stream ends and "— reconnected —" when
  `r` (or Workstream 10's resume) reconnects it.
- **Already covered:** table and eventlog `Placeholder`
  (`app.glyphs.placeholder`); logview `SetFollow` (`follow:` plus
  logview's own toggle key).
- **Never expose:** logview `Prepend`. Logview is bound to streams, and
  older history is eventlog's job (the 0.31 decision).
- The `colors:` block stays the only shape for per-component style
  overrides.

**Tests.** Each field reaches its option; `distinct` rejected off a
windowed table; the stream-ended and reconnected markers.

## Workstream 5 — forms, confirm and menu internals

Decided in
[Form, confirm and action menu option gaps](https://github.com/jsdrews/tui-builder/issues/59).

```yaml
inputs:
  name:
    validate: "value matches '^[a-z0-9-]+$'"
    invalid: lowercase letters, digits and dashes only
  replicas: {type: int, validate: "int(value) <= 10", invalid: at most 10}
  region: {options: [eu, us], required: true}   # → RequirePick
confirm: {message: "Delete ${selection}?", default: yes}
```

- **`validate:` / `invalid:`** on parameters (action inputs and
  `app.prompts`) map to tuilib's `Validate` for every field type: text,
  int, select and confirm. Expressions use `internal/expr` with `value`
  in scope.
- **`required: true` on a select** maps to `RequirePick` (nothing
  highlighted until the user picks).
- **`confirm:`** stays a string, or takes `{message:, default: yes}`
  (`Initial`). The default stays on Cancel.
- **Adopt internally:** `Action.ID` from the binding's registry name;
  `Set.Targets` from the selection's row keys, so "exclusive" applies
  per row; fanned-out claims use `DispatchEachWith`.
- **Never expose:** `SelectOptions.Height` (auto-fits up to 4 rows, then
  scrolls) and the menu's `MultiReason` / `RunningReason` (tuilib's
  wording).

**Tests.** A failing `validate` blocks submit with the `invalid`
message on each field type; a required select starts unpicked; the
confirm default; two rows of one exclusive action run independently.

## Workstream 6 — metrics

Decided in
[Inline metrics in cells, rows and inspector values](https://github.com/jsdrews/tui-builder/issues/51).

```yaml
columns:
  - {title: Ready,  metric: {ratio: [status.readyReplicas, spec.replicas]}}
  - {title: Health, metric: {badge: [ok, warn, down]}}
  - {title: CPU,    metric: {bar: cpu_pct, max: 100, color: blue}}   # max: number or path
  - {title: Trend,  metric: {spark: latency_series}}                 # path to a list of numbers
```

- On **table columns and inspector fields**. Exactly one kind key
  (`badge`, `ratio`, `bar`, `spark`); inputs in tuilib's argument order;
  **paths only** (anything computed belongs in `derive`).
- `width:` defaults to the column's `width`, or 10.
- `color:` (name or index) picks the `*Styled` variant; `bar`/`spark`
  only.
- `${selection.<col>}` captures the first input's raw value, not the
  glyphs (rule 5).
- **Validation errors:** `metric:` with `value:`, `color_rules:` or
  `sortable: true`; more than one kind key or the wrong number of inputs;
  `color:` on `badge` / `ratio`.
- **Never expose** on list rows and tree nodes: a row is one string, so
  a metric would replace the whole label. A table already does this.

**Tests.** Each kind renders through `pkg/metrics`; selection gets the
raw value; each validation error.

## Workstream 7 — tabs

Decided in
[Tabs container schema](https://github.com/jsdrews/tui-builder/issues/50).
Replaces `plans/tabs.md`.

```yaml
screens:
  cluster:
    title: Cluster
    strip_pos: top          # top | bottom
    initial: Services       # a tab label; default the first
    prev_key: shift+left    # "-" disables
    next_key: shift+right   # "-" disables
    number_keys: false      # default true (1–9 jump to a tab)
    tabs:
      - label: Pods
        layout: {component: pods_table}
        actions: [...]
      - label: Services
        layout: {component: services_table}
```

- A screen declares `tabs:` **instead of** `layout:`. Each tab is a small
  screen (`label`, `layout`, `actions`) built as the existing
  `screen.Model`; `pkg/tab` hosts them.
- **Behaviour comes from tuilib and the existing screen**, with no new
  machinery:
  - keys and mouse go to the active tab only; `tab` / `shift+tab` cycle
    focus inside it
  - a hidden tab first fetches when first opened, then keeps polling
    while hidden; switching back doesn't refetch
  - each tab keeps its own sources; a source bound in two tabs is
    fetched twice, like pushed screens today (`cache` dedupes)
  - a push from inside a tab keeps the tab pane underneath it
  - `SetTheme` reaches every tab
- The breadcrumb shows the screen's `title:` only. The tabbed screen
  forwards Help, HelpSections and the action provider to the active tab.
- **Validation:** `tabs:` and `layout:` are exclusive and one is
  required; a tabbed screen has no screen-level `actions:`; at least one
  tab; unique labels; `initial:` names one; `strip_pos` in
  `top|bottom`. The same component may appear in two tabs (each builds
  its own copy).
- **Not adopted:** `separator` and strip style overrides (theme); tabs as
  a layout node beside other panes (revisit if a header above tabs is
  needed).

**Tests.** Key routing to the active tab; lazy first fetch then hidden
polling; theme swap keeps the active tab; each validation rule.

## Workstream 8 — input and toggle

Decided in
[Standalone input and toggle components](https://github.com/jsdrews/tui-builder/issues/52).

```yaml
search:       {type: input,  title: Search, placeholder: name…, initial: "${env.Q}"}
show_deleted: {type: toggle, title: Deleted?, yes_label: shown, no_label: hidden}
pods_table:
  source: pods
  on_input:
    bind: {q: "${input.search}", deleted: "${input.show_deleted}"}
```

- **`${input.<component>}`** is allowed in any `bind:` map, including
  `on_cursor.bind`. `on_input: {bind:}` serves components with no cursor
  driver. A component refetches when any input it references commits,
  through the existing param-cache fetch path.
- **Commit:** enter commits an input (esc reverts to the last committed
  value; no refetching on every keystroke); each toggle flip commits.
  `initial:` values (which can use `${env.*}`) are bound for the first
  fetch.
- **Focus:** both join the `tab` cycle. A focused input captures every
  key; a toggle takes only its own.
- **Fields:** input `title`, `placeholder`, `initial`, `char_limit`,
  `mask`; toggle `title`, `initial`, `yes_label`, `no_label` (binds as
  `"true"` / `"false"`). Neither takes `source:`.
- **Elsewhere:** `${input.*}` in action argv and URLs resolves when the
  action runs; in push templates, when the screen is pushed (rule 4).
- **Validation:** a layout item holding either defaults to `fixed: 3`,
  and any other size is an error; `${input.x}` must name an input or
  toggle on the same screen; `on_input` and `on_cursor` can't bind the
  same param.

**Tests.** Enter refetches with the new value and esc reverts; a toggle
flip refetches; mixed cursor + input binding; action argv reads the
committed value; State/Restore across a theme swap.

## Workstream 9 — remote extras

Decided in
[Remote data extras: Find, re-anchor, follow](https://github.com/jsdrews/tui-builder/issues/58).
Needs Workstream 8 (templated anchors read `${input.*}`).

```yaml
window:
  cursor: {...}
  anchor: "${input.jump_to}"     # newest | oldest | a template resolving to a cursor
  find: {params: {q: "${window.search}", limit: 1}}   # or body:, like older:/newer:
  # Seekable: find: {params: {...}, position: <path>}
  viewport_delay: 150ms          # "-" asks at once
```

- **Server-side Find** for both kinds of remote data. The patch merges
  into params or body; the hit is the reply's first item: its cursor
  (through the window's `cursor` path) on Anchored sources, after which
  the view re-anchors there; `position: <path>` on Seekable sources,
  where it's required (and an error on Anchored ones). No items means no
  match.
- **Jump to a cursor:** a templated `anchor:` maps to `source.At(...)`,
  and re-anchors through `SetAnchor` when an input it references
  commits.
- **Adopt internally:** each screen passes its own `Context`, so popping
  a screen cancels its in-flight requests.
- **Never expose** `Follow` on windowed tables while Growing stays an
  error on tables (the 0.31 decision).

**Tests.** Find re-anchors an Anchored eventlog and seeks a Seekable
table; `position:` rules; a committed input re-anchors; popping cancels
requests.

## Workstream 10 — stream resume

Decided in
[Verdicts for plumbing packages](https://github.com/jsdrews/tui-builder/issues/60).

```yaml
pod_logs:
  type: exec
  follow: true
  command: [kubectl, logs, -f, --timestamps, "--since-time=${resume.since}", "${params.pod}"]
  resume:
    timestamp: '^(\S+) '   # regex capturing each line's timestamp
    granularity: 1s        # default 1s
```

- With `resume:`, a stream reconnects on `r` **and automatically when it
  ends**, skipping repeated lines through `resume.Tracker`
  (`Drop` / `Observe`). `${resume.since}` is empty on the first connect.
  Each reconnect adds the "— reconnected —" marker (Workstream 4).
- Without `resume:`, streams behave exactly as today.
- **Validation:** `${resume.since}` without `resume:`; `resume:` on a
  non-streaming source; a `timestamp:` regex with no capture group.

**Tests.** A reconnect inside the same second skips exactly the lines
already seen; automatic reconnect on stream end; each validation error.

## Workstream 11 — `poll` refactor

Decided in
[Verdicts for plumbing packages](https://github.com/jsdrews/tui-builder/issues/60).
Replace the screen's own `tea.Tick` refresh ticker
(`internal/screen/screen.go`) with `pkg/poll`, per rule 2. No YAML. It
goes last so it doesn't collide with the other screen changes.

**Tests.** The existing refresh tests pass unchanged.

---

## Implementation order

Decided in [PR split and order](https://github.com/jsdrews/tui-builder/issues/61).

1 → 2, then four tracks that can run in parallel:

- **Options:** 3 → 4 → 5
- **Input:** 8 → 9
- **Independent features:** 6 and 7, in either order
- **Stream resume:** 10

11 goes last.

Every feature PR updates `docs/reference/` (the coverage tests enforce
the field rows, registered enum values, expression built-ins and wrangl
flags) and the guide page for its area. A new component or YAML block
also gets an example under `examples/`: tabs, metrics, input/toggle,
resume and find.

## Never expose

| Item | Why |
|---|---|
| `HelpVerbose`, `DisableHelpSearch` | The help overlay always stays fully featured. |
| `output.SourceWidth`, console colours | Layout detail; colours come from the theme. |
| `SelectOptions.Height` | The select auto-fits up to 4 rows, then scrolls. |
| `MultiReason`, `RunningReason` | tuilib's wording for a disabled action. |
| logview `Prepend` | Logview is bound to streams; older history is eventlog's job. |
| Metrics on list rows and tree nodes | A row is one string; a table already pairs text with a metric. |
| `Follow` on windowed tables | Growing is an error on tables. |
| tab `separator`, strip styles | The theme covers them. |

## Deferred

- **Eventlog holes** (`Item.Hole`). Needs sources that know which
  positions are missing (numbered event APIs like AWX), and no config
  needs it yet.
- Still deferred from the 0.31 plan: activity on streaming and windowed
  tables; eventlog totals from a second request; Anchored tail overlap
  for late arrivals; terminal recordings for the docs site.

## Out of scope

- **Pushing a screen open at a particular tab** (`tab:` on a push
  action). New tui-builder behaviour, not tuilib adoption; `initial:`
  covers the static case.
- **Sparkline history collected across polls** (a ring buffer per row).
  tuilib leaves history to the caller; it would fit as a data-layer
  operator if it's ever wanted.
