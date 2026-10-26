# Gaps found running generated configs: the locked spec

What tuilib and tui-builder need so that a config generated from an
OpenAPI 3 description runs well against a large REST API. These are the
bugs and gaps found by running such configs. The test case is a
FastAPI-style service whose lists take `page` / `per_page` / `order_by`
and answer `{total, entries}`. Its spec leaves things out (undocumented
`<field>__icontains` filters) and lists sort columns the server then
refuses with a 400.

It's locked: every decision was made on the map
[Map: tuilib table bugs and tui-builder gaps from generated configs](https://github.com/jsdrews/tui-builder/issues/105).
Each section links the ticket that holds its detail and reasoning.
Six tuilib PRs and a release come first, then nine tui-builder PRs, in
the [order at the bottom](#implementation-order).

**All of it is built**, on tuilib v0.43.0. What follows is the record of
what was decided; where the build departed from it, or found something
the plan didn't foresee, is under
[What changed while building it](#what-changed-while-building-it).

Terms follow [`CONTEXT.md`](../CONTEXT.md), which gains **Refusal**,
**Action group**, **Menu** and **Comparison**.

Every new field must map cleanly from OpenAPI 3:

| OpenAPI | tui-builder |
|---|---|
| an `order_by` parameter's enum | `window.sort_fields` |
| a list's filterable properties (or a known lookup convention) | `window.filter_ops`, `window.filter_except` |
| non-text properties (integers, enums, ids, timestamps) | `window.filter_except`, or per-op `filters:` entries |
| `tags` on an operation | `group:` on a menu item |
| a `GET` vs a `POST` / `PATCH` / `DELETE` | the default action group (`read` / `write`) |
| `securitySchemes` with a token the user already has | `app.env` entry with `from: {file:}` or `from: {command:}` |

## 1. Table fixes in tuilib

Decided while charting the map (no ticket).

- **Sort caret clipping (`pkg/table`):** a `Sortable` column's base width
  gets 2 extra cells, in both the explicit-`Width` and auto branches of
  `recomputeWidths`, so `formatCell` never cuts the ▲ / ▼.
- **Hidden columns in the sort cycle:** `sortableColumns` skips `Hidden`
  columns.
- **Action input form frame:** `form.Options` gains `Title`, and the
  form draws a pane the way `confirm` does. tui-builder passes the
  action label as the title, which fixes both the action form
  (`newInputForm`) and the startup prompt form.

## 2. Recovering from a refused remote sort

From [Recovering from a refused remote sort](https://github.com/jsdrews/tui-builder/issues/106).

**Telling a refusal from a failure**
- tuilib: `remote.Refused(err)` marks an error, and
  `errors.Is(err, remote.ErrRefused)` checks for it. The source decides
  what counts; tuilib never guesses.
- tui-builder's http source marks an `*HTTPError` with status **400 or
  422** as refused. Everything else stays an ordinary failure (401,
  403, 404, 429, 5xx, timeouts, network errors). Exec windows never
  refuse.
- It's a **sort refusal** only when the failed query differs from the
  answered one (the query the rows on screen answer) in its sort alone.
  Anything else is handled as an ordinary failure, as today.

**On a sort refusal (`pkg/table` / `pkg/remote`)**
- The sort goes back to the answered query's sort, with no request.
  The rows are no longer dimmed and the border goes back to normal.
- The column becomes **unavailable** for the life of the table: `[` /
  `]` / `s` and header clicks skip it, and its header title is dimmed.
  This survives `Restyle` through `State` / `Restore`, and resets when
  the screen is rebuilt.
- If the first load's starting sort is refused, the column becomes
  unavailable, the sort drops to none, and the table requests once more
  without one. A second failure is an ordinary failure.
- The remote `Table` returns `table.SortRefusedMsg{Column, Title, Err}`
  and posts nothing itself. tui-builder's screen turns it into a
  one-time notice (`can't sort by owner: HTTP 400 — …`), and
  `HTTPError.Detail()` goes to the output console.

## 3. Read and write groups in the action menu

From [Read and write groups in the action menu](https://github.com/jsdrews/tui-builder/issues/107).

**tuilib (`pkg/action`)**
- `Action.Group string`, with no name given special meaning.
- Headers are drawn only when a menu holds **two or more** groups.
  Entries are pulled together by group in binding order, groups in the
  order first seen, and ungrouped entries last under `other`. Header
  text is the name as written.
- The cursor, clicks and the half-page keys skip headers.
  `Keys.NextGroup` (`tab`) and `Keys.PrevGroup` (`shift+tab`) jump
  between groups and are listed in help.
- `Options.HeaderStyle`, plus an optional
  `Options.GroupStyle func(group string) lipgloss.Style`.
- The header and skip logic lives in an `internal/` helper that
  `pkg/menu` (§5) shares.

**tui-builder**
- `group:` on a **binding**. Without it the group comes from the action:
  push, http `GET` / `HEAD` → `read`; other http → `write`; exec → none
  (`other`). There's no opt-out; a binding names another group instead.
- `app.action_groups: [read, write]` sets the order. Unlisted groups
  follow in first-seen order. Naming a group that no binding uses is a
  load error.
- The theme colours the `write` header with its warning colour and
  other headers with its muted colour.

## 4. Env values from a file or a command

From [Env values from a file or a command](https://github.com/jsdrews/tui-builder/issues/108).

```yaml
app:
  env:
    - {name: BEARER_TOKEN, required: true, from: {file: ~/.tokens/x}}
    - {name: OTHER, from: {command: [pass, show, api], timeout: 90s}}
```

- `from:` holds exactly one of `file:` / `command:`. It can't be combined
  with `default:`; `required:` is allowed.
- A new `ResolveEnv()` runs at the entry points, not in `Load`:
  tui-builder calls it before `collectAppPrompts`, and wrangl calls it
  before `SubstituteEnv` only in modes that fetch. `Load` treats a
  `from:` var as provided, so `--check` and tests never run commands.
- Entries resolve in list order, once per process. A value already in
  the environment wins. A prompt with the same key is pre-filled with
  the resolved value.
- `file:` expands `~` and `${env.X}`, and a relative path is resolved
  against the config file's directory.
- `command:` runs as argv with no shell, and stdout is the value. stdin
  and stderr pass through so password prompts work. The timeout
  defaults to 60s.
- Values are trimmed. An empty value, a missing file, a non-zero exit
  or a timeout is a failure: an error naming the var and the cause if
  `required: true`, otherwise a one-line warning.
- Values are never printed; `--describe` shows only where one comes
  from.

## 5. A menu of screens

From [Navigating configs with many sections](https://github.com/jsdrews/tui-builder/issues/109).
tuilib: [pkg/menu: a grouped, filterable list of items to open](https://github.com/jsdrews/tuilib/issues/129).

**tuilib (`pkg/menu`)**
- Items `{Label, Group, Description, ID}` plus a payload. Choosing one
  emits a msg, and the caller decides what opening means.
- Group rules as in §3, through the shared helper.
- Each row shows the label and a dimmed description, cut off when space
  runs out.
- Keys: `enter` or a double-click chooses, `/` filters, and `[` / `]`
  jump between groups. `tab` is left alone.
- The filter matches label and group, and a group with nothing left
  hides its header.

**tui-builder**
```yaml
home_menu:
  type: menu
  title: Sections
  items:
    - {group: database, label: "postgres › instance", screen: db_pg_instance, description: "GET /…/instance"}
    - {group: database, label: "mysql › instance",    screen: generic_instance, bind: {engine: mysql}}
```
- Items are static, with no `source:`, so every `screen:` is checked at
  load.
- `bind:` fills the destination's `parameters:` as a push binding does,
  with literals or `${env.*}` only.
- Opening an item pushes its screen; `esc` goes back.
- A menu owns `enter`, so using it as a binding's `from:` is a load
  error.
- Rejected: a templated `push:` and a tab picker.

## 6. Dangling push targets

Decided while charting the map (no ticket). A `push:` naming a screen
that doesn't exist becomes a load error. Today it fails only when
fired. Menu items (§5) use the same check.

## 7. One filter rule per window

From [One filter rule per window](https://github.com/jsdrews/tui-builder/issues/110).
tuilib: [Comparison operators in the filter grammar (year>=1950)](https://github.com/jsdrews/tuilib/issues/128).

**tuilib (`pkg/query`)**
- `Term.Op`: `:` contains (the zero value, so callers are unaffected),
  `~` regex, and `=` `!=` `>` `>=` `<` `<=`. `year>=1950` reads as
  intended, and the coordinator translates it.
- Keys resolve by title prefix as today. `Parse` still never fails.
- `MatchAll` applies the op locally, so local and remote filtering agree
  on meaning. Completion recognises the new separators.

**tui-builder**
```yaml
window:
  search_param: name__icontains              # bare text, as today
  filter_ops:
    contains: "${column.value}__icontains"   # name:tolk
    eq:       "${column.value}"              # name=tolkien
    gte:      "${column.value}__gte"         # year>=1950
    lte:      "${column.value}__lte"
  filter_except: [Id, Created]
  filters:
    Year: {gte: year_from, lte: year_to}     # per-column override
```
- `filter_ops` maps an op (`contains eq ne gt gte lt lte`) to a param
  template using `${column.value}`. http only; a preset's `window:`
  carries it. A `~` value is sent under `contains`, as today.
- It covers every visible column of a bound table whose `value:` is one
  plain key. Nested paths, fallback chains, hidden columns and
  `filter_except` titles are skipped. Numbers aren't guessed; the
  generator writes `filter_except` from the spec's types.
- `filters:` keeps a string entry meaning `contains`. A map entry sets
  params per op. An explicit entry replaces the rule for that column.
- The rule expands into `filters:` at load, unioned across tables bound
  to the source, so everything downstream sees an ordinary map.
- An op the column can't send (`year>=1950` with no `gte`) is left out
  of the request with a one-time notice: "`>=` isn't supported on
  Year". It doesn't fall back to bare text.
- exec: `contains` only, through `${window.filters.NAME}`; other ops
  follow the notice rule.

## 8. Sortable columns listed on the source

From [A list of sortable columns on the source](https://github.com/jsdrews/tui-builder/issues/111).

```yaml
window:
  sort_param: order_by
  sort_desc_prefix: "-"
  sort_fields: [name, created_at]   # the spec's order_by enum
```

- The list names the API's sort fields. A column matches when its
  `sorts:` entry, or else its plain `value:` key, is listed. A matched
  column is sortable and sends that field.
- It adds to per-column `sortable`. `Column.Sortable` becomes `*bool`.
  An explicit `sortable: false` turns a matched column off, and
  `sortable: true` on an unmatched column is a load error.
- No deny-list: a spec that's wrong is caught at runtime by §2.
- It travels with a preset's `window:`. It's allowed on http and exec;
  on exec it only decides which columns are sortable.

## 9. Surfaces

Added after the map closed, with the tuilib v0.40.0 bump (B4), which
replaced border shapes with surfaces.

- `app.surface:` picks the starting surface by built-in name
  (`outlined`, `filled`, `banded`, `elevated`, `accented`); an unknown
  name is a load error. The start is the `TUI_BUILDER_SURFACE` env var,
  then `app.surface`, then the user's tuilib `surface:`, then
  `outlined`.
- `app.surface_key` (default `T`, `-` pins it) cycles all five, starting
  from the chosen one. It's a shell key like `theme_key`.
- `app.borders.active` / `overlay` shape the `outlined` surface wherever
  it's drawn; `slot_brackets` and `glyphs.rule` apply to every surface.
  `app.borders.inactive` is a load error: a surface draws the same lines
  focused or not.
- `app.colors: auto | truecolor | 256 | 16` (default `auto`) forces the
  colour depth, for terminals that support more than they say (tmux
  often hides `COLORTERM`). `TUI_BUILDER_COLORS` overrides it for one
  run. It's set before anything is drawn, so surfaces resolve against
  it too. An unknown value is a load error.
- Not here: a per-component `surface:`.

## 10. Validation

Each PR adds its own rows. All are load errors unless marked.

| Field | Error |
|---|---|
| `push:` | names a screen that doesn't exist |
| `group:` / `app.action_groups` | a listed group no binding uses |
| `app.env[].from` | both or neither of `file:` / `command:`; combined with `default:`; a bad `timeout:` |
| `app.env[].from` (at `ResolveEnv`) | a failure with `required: true` (a warning otherwise) |
| `type: menu` | an item's `screen:` doesn't exist; `bind:` names no parameter of it, or uses a token other than `${env.*}`; the menu used as a binding's `from:` |
| `window.filter_ops` | a template without `${column.value}`; an unknown op; set on exec |
| `window.filters` | a map entry with an unknown op; a map entry on exec |
| `window.filter_except` | a title no bound table has |
| `window.filter_ops` (expanded) | the same title with different `value:` keys across bound tables; a rule covering no column |
| `window.sort_fields` | without `sort_param` (exec: without `${window.sort}`); on an Anchored window; an empty field |
| `columns[].sortable` | `true` on a column `sort_fields` doesn't match |
| `app.surface` | not a built-in surface name |
| `app.colors` | not one of `auto`, `truecolor`, `256`, `16` |
| `app.surface_key` | collides with another shell key |
| `app.borders.inactive` | set at all |

## Implementation order

tuilib PRs are built in `~/dev/me/libs/menu-component` (`jsdrews/tuilib`).
tuilib is at v0.39.0; tui-builder is pinned to v0.35.0.

| # | Repo | PR | Sections | Needs |
|---|---|---|---|---|
| T1 | tuilib | **Table fixes**: caret width, hidden columns out of the sort cycle | 1 | — |
| T2 | tuilib | **Form title**: `form.Options.Title` and its pane | 1 | — |
| T3 | tuilib | **Sort refusal**: `remote.ErrRefused`, unavailable columns, `SortRefusedMsg` | 2 | T1 |
| T4 | tuilib | **Action groups**: `Action.Group`, the `internal/` group helper, keys, styles | 3 | — |
| T5 | tuilib | **`pkg/menu`** | 5 | T4 |
| T6 | tuilib | **Filter comparisons**: `Term.Op`, local matching, completion | 7 | — |
| R | tuilib | **Release v0.40.0** | — | T1–T6 |
| B1 | tui-builder | **Dangling push targets** load error | 6 | — |
| B2 | tui-builder | **Env from file or command**: `from:`, `ResolveEnv()` in both entry points | 4 | — |
| B3 | tui-builder | **Sortable fields on the source**: `sort_fields`, `*bool` sortable | 8 | — |
| B4 | tui-builder | **Bump tuilib to v0.40.0**: absorb v0.36–v0.39, pass the action label as the form title | 1 | R |
| B5 | tui-builder | **Sort refusal**: 400/422 marked refused, the notice | 2 | B4 |
| B6 | tui-builder | **Action groups**: `group:`, defaults, `app.action_groups`, theme colours | 3 | B4 |
| B7 | tui-builder | **Menu component** | 5 | B1, B6 |
| B8 | tui-builder | **Filter rule**: `filter_ops`, `filter_except`, per-op `filters:`, the unsupported-op notice | 7 | B4 |
| B9 | tui-builder | **Surfaces**: `app.surface`, `app.surface_key`, `app.colors`, chrome on every surface | 9 | B4 |

B1, B2 and B3 don't need the release and can start at once, alongside
the tuilib PRs. B4 does nothing but the bump and the form title, so that
breakage from v0.36–v0.39 is found apart from new features.

**Each PR ships** its rows of the validation table, tests (`httptest`
for requests; screen tests for notices, menus and groups),
`docs/reference/*.md` rows (`TestReferenceDocumentsEveryField`), and
`--describe` lines for its fields. tuilib PRs carry their own tests and
changelog entries.

## What changed while building it

**tuilib releases.** T1–T6 shipped together in v0.40.0. Four follow-ups
came out of building on it, each a tuilib issue and a release:
[#131](https://github.com/jsdrews/tuilib/issues/131) (v0.41.0) shell
glyphs, `ActionGroupStyle` and `Palette.Warning`;
[#133](https://github.com/jsdrews/tuilib/issues/133) (v0.42.0) filled
field forms for inputs, toggles and the filter bar;
[#134](https://github.com/jsdrews/tuilib/issues/134) (v0.42.1) modal
forms drawn with the overlay surface;
[#135](https://github.com/jsdrews/tuilib/issues/135) (v0.43.0) group
headers as thin dividers. tui-builder is on v0.43.0.

**B4 was a migration, not a bump.** v0.39 replaced border shapes with
surfaces in a clean break, so B4 moved tui-builder onto palettes and a
surface:
- `app.borders.active` / `overlay`, `slot_brackets` and `glyphs.rule`
  now build one Outlined surface (`build.ChromeSurface`).
- **`app.borders.inactive` is a load error.** A surface draws the same
  lines focused or not, so an unfocused shape can't be honoured; nothing
  used it.
- `app.glyphs` first had to be re-applied on every screen's `SetTheme`
  (the shell composed themes without glyphs); since v0.41.0 they're
  passed as `app.Options.Glyphs` and reach the shell's overlays too.
- The startup prompt form is titled with `app.title`, or **"Settings"**
  when there's none (the plan named only the action form's title).

**§9 Surfaces was added during the build** (B9), with `app.colors` to
force the colour depth, since tmux often hides `COLORTERM`.

**§2 Sort refusal.** The data layer can't import tuilib, so
`HTTPError.Refusal()` reports a 400 / 422 and the screen layer wraps it
with `remote.Refused`. The notice keeps the column title as written
(`can't sort by Title: …`).

**§3 Action groups: no per-group colours.** The plan drew the `write`
heading in the warning colour and the rest muted. Built that way (v0.41.0
made it possible), it read as noise, and bold headings as distracting.
tui-builder passes no group style now; tuilib draws every heading as a
thin divider (`── write ──`), the same for every group. An http action
with no `method:` is POST, so it lands in `write`; a templated method
does too.

**§4 Env from a file or a command.**
- `timeout:` with `file:` is an error (it only bounds a command).
- `--describe` showing where a value comes from wasn't built: nothing
  describes `app.env` today.

**§5 Menu.**
- `bind:` keys aren't checked against the destination's parameters at
  load, the same as push bindings: keys no source declares are ignored,
  and a missing required parameter fails at push time. Checking it means
  following the destination's pipelines back to their sources; worth
  doing for menus and pushes together if it bites.
- The menu is always filterable, and a screen-wide `enter` binding
  doesn't fire while a menu has focus.
- A menu's `items:` are maps, a list's strings, so `Component` decodes
  `items:` by `type:`.

**§7 Filter rule.**
- A rule that covers no column reports that, rather than the generic
  "filterable has nowhere to go".
- `filter_except` without `filter_ops` is an error.
- A comparison is sent with its value as typed (case kept); `contains`
  keeps today's lowercased value.
- The unsupported-comparison notice is shown per committed query, beside
  the bare-terms notice.
- `filters:` map entries share the key with string entries, so
  `WindowConfig` decodes `filters:` itself.

**The example launcher** needed fixing to show any of this:
- it never bound the action menu's key, so `a` did nothing under
  `task examples`;
- it ignored each example's `surface:` (it now switches the shell's
  surface as an example opens, and binds `T`);
- loading an example wrote env warnings to stderr over the running TUI.
  `cfg.LoadTo` sends them to a writer; the launcher posts them to the
  output console, and wires `datasource.Notify` as tui-builder does.

**Examples.** Every example sets `surface: filled`, and the offline
examples' bindings carry `group:` (headings show on `action_prompts` and
`marking`). `examples/menu.yaml` is new.

## Out of scope

- **The OpenAPI-to-config generator.** A future binary in this repo with
  its own map; it consumes this spec.
- **A JSON object / array input** for free-form request bodies.
- **Fetching every page to filter locally** for APIs that can't filter.
- **Refreshing an `app.env` token mid-session** (re-running `from:` on a
  401). `from:` resolves once per process.
- **Templating a filter's value side** (`?name=ilike.*x*`). `filter_ops`
  templates only the param name.
- **Multi-column sort** (`order_by=name,-created_at`). A new table
  feature: a sort stack in `pkg/table`, a list in `remote.Query`, and
  refusal per column. Nothing is broken without it.
- **A yes/no input that can be left unset.** Optional booleans with no
  default stay out of forms until a tri-state toggle is designed.
- **Comparisons on exec windows.** Exec windows are hand-written, and no
  gap came from them.
