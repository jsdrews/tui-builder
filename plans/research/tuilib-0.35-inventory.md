# Inventory: tuilib v0.35 surface vs what tui-builder exposes

Research for [#48](https://github.com/jsdrews/tui-builder/issues/48). Facts
only. This doc doesn't say what to adopt.

Sources:

- **tuilib v0.35.0**, read from the module cache
  (`~/go/pkg/mod/github.com/jsdrews/tuilib@v0.35.0`): every `pkg/*/*.go`
  non-test file, `CONTEXT.md`, `docs/`. The exported fields and methods were
  pulled with a `go/ast` walk over `pkg/`, and the gists below are the first
  sentence of each field's doc comment. The cache also holds v0.31.0–v0.34.0,
  which were diffed the same way for the "added since" section.
- **tuilib release notes** for v0.32.0–v0.35.0 (`gh release view -R jsdrews/tuilib`).
- **tui-builder** `main` at `24b10d5` (pins `github.com/jsdrews/tuilib v0.35.0`).
  The files are `internal/config/{config,action}.go` (YAML schema),
  `internal/build/{component,chrome,mark,binding,layout}.go`,
  `internal/screen/{screen,window,actions_menu,claims}.go`, and
  `cmd/tui-builder/main.go`. `cmd/wrangl` imports no tuilib package.

Status key:

- **Exposed**: a YAML field sets it. The YAML path is given.
- **Indirect**: tui-builder sets or calls it, but no YAML field controls it.
  The call site is given.
- **Theme**: left at the value `theme.Theme.<Component>()` fills in. tui-builder
  starts every component from that builder and doesn't override the field.
- **Unused**: tui-builder never sets or calls it. If tuilib applies a default
  for the zero value, the table says so.

YAML paths are written relative to a component (`components.<name>.…`)
unless they start with `app.`, `actions.` or `data.`.

---

## 1. Package coverage

tuilib v0.35.0 has 38 packages under `pkg/`. The second column counts the
non-test tui-builder files that import each one (`grep` on import paths in
`cmd/` and `internal/`).

| Package | Imported by | Notes |
|---|---|---|
| action | 1 (`screen/actions_menu.go`, as `taction`) | `Set` / `Action` built per screen. `action.Options` (menu styling) isn't set. |
| activity | 4 | `Op`, `Mode`, `DispatchOptions`, `Read`, `UnobservedMsg`, `Outcome` |
| alert | 0 | Not imported. The app shell uses it internally. |
| ansi | 2 | `CellColor`, `Hyperlink` (table cells, column separators) |
| app | 7 | `app.New`, `Info`/`Error`/`InfoDetail`/`ErrorDetail` |
| breadcrumb | 0 | Not imported. The shell draws it from `screen.Title()`. |
| config | 0 | Not imported, but read by `app.New`. See §3. |
| confirm | 1 | Action `confirm:` modal (`screen.go:1665`) |
| eventlog | 3 | `components.*` `type: eventlog` |
| filter | 0 | Not imported. Reached only through `<component>.Options.Filter`. |
| focus | 1 | `RequestMsg`, `Focusable`, `Identified` (`screen.go:685,1097,1139`) |
| form | 2 | Boot prompts (`main.go:162`) and action input form (`screen.go:1521`) |
| geom | 3 | `geom.New`, `NextGen`, `Rect` |
| glyph | 1 | `app.glyphs` → `theme.Glyphs` (`build/chrome.go:51`) |
| help | 1 | `help.Section`, `Sections`, `Group` (`screen.go:989–1028`) |
| input | 0 | Not imported. Reached only through `form.Text`/`form.Password`. |
| inspector | 3 | `type: inspector`, `FromAny` for `auto: true` |
| layout | 3 | `vstack`/`hstack`/`zstack`/`flex`/`fixed` (`build/layout.go`), modals |
| list | 5 | `type: list` |
| logview | 1 | `type: logview` |
| metrics | 0 | Not imported |
| mouse | 2 | `mouse.Msg`, `NewTracker` (boot form) |
| output | 0 | Not imported. Console configured only by the `app.output_key` switch. |
| pane | 1 | Only the `SlotBracketStyle` constants (`build/chrome.go:106`) |
| poll | 0 | Not imported. tui-builder has its own `refresh:` timer. |
| query | 1 | `query.Term` (`screen/window.go:462`) |
| remote | 2 | `NewTable`, `NewEventlog`, `Seekable`, `Anchored` |
| resume | 0 | Not imported |
| runner | 2 | `Run`, `RunWithNotice`, `CaptureWith`, `GoWith`, result messages |
| screen | 4 | `screen.Screen`, `Push` |
| source | 1 | `source.Newest()` / `source.Oldest()` only (`screen/window.go:219`) |
| statusbar | 0 | Not imported. The shell owns it. |
| tab | 0 | Not imported |
| table | 5 | `type: table` |
| textview | 1 | `type: textview` |
| theme | 11 | `theme.All()`, `ByName`, every `th.<Component>()` builder |
| toggle | 0 | Not imported. Reached only through `form.Confirm`. |
| tree | 3 | `type: tree` |

**12 packages are never imported:** alert, breadcrumb, config, filter, input,
metrics, output, poll, resume, statusbar, tab, toggle. Of these,
breadcrumb, statusbar, alert, output and config run inside the app shell.
filter, input and toggle run inside components tui-builder does build.
metrics, poll, resume and tab aren't reached by any path.

---

## 2. Shared pane pass-throughs

Every pane-backed component (list, table, logview, textview, tree, inspector,
eventlog, plus confirm, the action menu and the help overlay) carries the
same block. The per-component tables below leave these rows out and refer
back here.

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `ActiveColor` / `InactiveColor` | Border colour focused / unfocused | **Exposed** `colors.border_active` / `colors.border_inactive` (list, table, logview, textview, tree, inspector, eventlog) | `build/component.go` `applyPaneColors` and the per-kind `Colors` blocks |
| `ActiveBorder` / `InactiveBorder` | Border shape focused / unfocused | **Exposed**, globally only: `app.borders.active` / `app.borders.inactive`. Overlays use `app.borders.overlay` → `Theme.BorderShapeOverlay`. | `build/chrome.go:27` `ApplyChrome` |
| `Glyphs` | Marks this component draws, plus its scrollbar | **Exposed**, globally only: `app.glyphs.*` (13 of 16 `glyph.Set` fields; see §4.13) | `build/chrome.go:51` |
| `SlotBrackets` | How the title meets the border | **Exposed**, globally only: `app.borders.slot_brackets` | `build/chrome.go:106` |
| `SpinnerStyle` | Spinner glyph style while loading | **Exposed** (foreground only) `colors.spinner` | `build/component.go` |
| `LoadingLabel` | Text beside the loading spinner | **Unused** | none |
| `Width` / `Height` | Outer size | **Indirect**: set by the layout through `SetRect` | `build/layout.go` (`layout.Sized`) |
| `Title` | Top-left border label | **Exposed** `title:` | each `build*` func |
| `Filter` (`filter.Options`) | Embedded filter's styling | **Theme**, except `Filter.Placeholder` **Exposed** `filter_placeholder:` | `build/component.go` (every kind) |
| `Keys` | The component's keymap | **Unused**. tuilib's `DefaultKeys()` applies. | none |

---

## 3. App shell: `app.Options` (`pkg/app`)

Built once at `cmd/tui-builder/main.go:95`.

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Root` | Root screen | **Indirect** | `main.go:95` (`tqscreen.New` / `NewMulti`) |
| `Themes` | List the user cycles through | **Indirect**: `theme.All()` after `ApplyChrome`, reordered so `app.theme` comes first | `main.go:50–80` |
| `Version` | Right side of the statusbar | **Exposed** `app.version` | `main.go:98` |
| `QuitKey` | Quits at stack depth 1 | **Unused**. Defaults to `q` / `ctrl+c`. | none |
| `ThemeKey` | Cycles themes | **Exposed** `app.theme_key` (default `t`, `-` = off) | `main.go:109` |
| `HelpKey` | Opens the `?` overlay | **Unused**. Defaults to `?`. | none |
| `SuspendKey` | ctrl+z suspend | **Unused**. Defaults to `ctrl+z`. | none |
| `DisableSuspend` | Turns suspend off | **Unused** | none |
| `OutputKey` | Opens the output console; zero turns the feature off | **Exposed** `app.output_key` (default `o`, `-` = off) | `main.go:103` |
| `ActionsKey` | Opens the action menu; zero turns the feature off | **Exposed** `app.actions_key` (default `a`, `-` = off) | `main.go:116` |
| `Actions` (`action.Options`) | Configures the menu | **Unused**. The shell merges the theme's `Actions()`. | none |
| `Output` (`output.Options`) | Configures the console | **Unused**. The shell merges `output.OptionsFrom(theme)`. | none |
| `HelpVerbose` | Legacy inline footer | **Unused**. `app.help_verbose` was removed in the 0.24 plan. | none |
| `DisableHelpSearch` | Turns off the overlay's search field | **Unused** (search stays on) | none |
| `Mouse` | Mouse mode | **Indirect**: hard-coded `app.MouseClick` | `main.go:123` |
| `DoubleClickInterval` | Double-click window | **Unused**. Falls back to the tuilib config file's `double_click_ms`, then the default. | none |
| `ThemeEnvVar` | Env var naming the initial theme | **Unused** | none |
| `SkipConfig` | Skips `theme.Resolve` / config load | **Unused** (false). `app.New` runs `theme.Resolve`, which reads `$XDG_CONFIG_HOME/tuilib/config.yaml` `theme:`. Per `pkg/theme/resolve.go`, that file's theme becomes `Themes[0]` and takes precedence over the order `main.go` set from `app.theme`. Tests set `SkipConfig: true`. | `app.go:443` |
| `DisableAutoEscPop` | Turns off esc→pop | **Unused** | none |

Package-level helpers used: `app.Info`, `app.Error`, `app.InfoDetail`,
`app.ErrorDetail` (`screen.go`, `claims.go`, `window.go`). Unused:
`app.SetTheme`, `app.ClearStatus`, `app.ErrorOf`.

`app.title` (YAML) maps to no tuilib field. Its only reader is the env
warning in `internal/config/env.go:101`. The breadcrumb shows `screen.Title()`
(`screen.go:335`).

`pkg/config.Config` (`Theme`, `DoubleClickMs`) is never imported, but
`app.New` reads it as described above.

---

## 4. Wrapped components

### 4.1 list (`list.Options`, built at `build/component.go:173`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Items` | Full item set | **Exposed** `items:` (static). With `source:`, **Indirect** via `SetKeyedItems` / `ApplyRead`. | `component.go:176`, `binding.go:302,377` |
| `Filterable` | Embeds a filter | **Exposed** `filterable:` | `component.go:182` |
| `HScrollbar` | Horizontal scroll row + ←/→ | **Theme** (`true`) | none |
| `SelectedStyle` | Highlighted row | **Exposed** (foreground) `colors.selected` | `component.go:197` |
| `Markable` | Mark column + x/X/A/D | **Exposed** `markable:` | `component.go:183` |
| `MarkStyle` | Colour of ✓ off-cursor | **Theme** | none |
| `Activity` (`activity.Options`) | Spinner/label config | **Theme**. Its `Settle` is overridden per claim; see §5. | none |
| `BusyWhen` | Which items are working | **Exposed** `activity.status` + `activity.busy` / `activity.busy_when` | `component.go:184` (`busyFunc`) |
| `Revision` | Value that moves when an operation finishes | **Unused** | none |
| (pass-throughs) | | see §2 | |

Setters and methods tui-builder calls: `SetKeyedItems`, `SetValue`
(`initial_filter`), `SetCursor` (`initial_cursor`), `SetTitle` (title suffix),
`SetLoading`, `State`/`Restore` (theme rebuild), `BeginRead`/`ApplyRead`,
`DispatchWith`/`Done`, `Selection`, `IsActivate`. Unused: `Deselect`,
`SetMarks`, `ClearMarks`, `ToggleMark*`, `MarkRange`,
`SetSelectedStyle`/`Set*Color` (rebuilds go through State/Restore instead),
`SetLoadingLabel`, `ActivityState`/`SetActivityState`.

### 4.2 table (`table.Options`, built at `build/component.go:233`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Columns` | Column layout | **Exposed** `columns:` | `component.go:283` |
| ↳ `Column.Title/Width/Flex/MaxWidth/Align/Sortable/Hidden` | | **Exposed** `columns[].title/width/flex/max_width/align/sortable/hidden` | `component.go:285` |
| ↳ `Column.Less` | Comparator | **Exposed** `columns[].sort: number\|si` | `component.go:292` (`parseLess`) |
| `Rows` | Full row set | **Exposed** `rows:` (static). With `source:`, **Indirect** via `SetKeyedRows` / `ApplyRead` / `remote`. | `component.go:296`, `binding.go:72,329,371` |
| `Filterable` | Embeds a filter | **Exposed** `filterable:` | `component.go:236` |
| `FilterMode` | Local vs remote filter | **Indirect**: `FilterRemote` when `Windowed` (and set by `remote.NewTable`) | `component.go:251` |
| `SortMode` | Local vs remote sort | **Indirect**: `SortRemote` when `Windowed` | `component.go:252` |
| `SortDebounce` | Quiet period before a remote sort commits | **Exposed** `sort_debounce:` | `component.go:318` |
| `Anchored` | Span mode | **Indirect**: set by `remote.NewTable` when the source's `window.cursor` is set | `screen/window.go:163` |
| `MaxItems` | Span's resident cap | **Exposed** `max_items:` (span tables only) | `component.go:315` |
| `Placeholder` | Cell text for not-yet-fetched rows | **Unused**. `app.glyphs.placeholder` sets the glyph-level equivalent. | none |
| `HScrollbar` | | **Theme** (`true`) | none |
| `HeaderStyle` | Header row | **Exposed** (fg) `colors.header` | `component.go:261` |
| `SelectedStyle` | Highlighted row | **Exposed** `colors.selected_fg` / `colors.selected_bg` | `component.go:264–268` |
| `Markable` | Mark gutter | **Exposed** `markable:` | `component.go:237` |
| `MarkStyle` | | **Theme** | none |
| `CellStyle` | Non-selected rows | **Exposed** (fg) `colors.cell` | `component.go:270` |
| `ActivityColumn` | Column replaced by the spinner | **Exposed** `activity.column` (`cfg.ActivityColumn`) | `component.go:239` |
| `Activity` | | **Theme** (`activityCell`) | none |
| `BusyWhen` | | **Exposed** `activity.*` | `component.go:240` |
| `Revision` | | **Unused** | none |
| `Borders.Vertical` | Column separator glyph | **Exposed** `colors.column_separator` (colour of a fixed `│`) | `component.go:276` |
| `Borders.HeaderRule` | Rule under the header | **Exposed** `colors.header_rule` (colour of a fixed `─`) | `component.go:279` |
| (pass-throughs) | | see §2 | |

Setters and methods tui-builder calls: `SetKeyedRows`, `SetValue`,
`SetCursor`, `SetSort` (`initial_sort`), `SetTitle`, `SetLoading`,
`State`/`Restore`, `BeginRead`/`ApplyRead`, `DispatchWith`/`Done`,
`Selection`, `Answered`/`Stale` (`LandAtNewest`), `Rows`. Unused:
`SetColumns`, `SetDistinct` (filter hint candidates), `SetStagedSort`,
`SetMarks`/`ClearMarks`/`ToggleMark*`/`MarkRange`, `SetWindow` (`remote`
calls it now), the `Set*Style`/`Set*Color` family.

### 4.3 logview (`logview.Options`, built at `build/component.go:388`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Searchable` | Embeds search | **Exposed** `searchable:` | `component.go:391` |
| `MaxLines` | Line cap | **Exposed** `max_lines:` | `component.go:392` |
| `MatchStyle` | Match highlight | **Exposed** (fg) `colors.match` | `component.go:399` |
| `CurrentLineStyle` | Current-match line | **Exposed** (bg) `colors.current_line_bg` | `component.go:402` |
| `LineNumbers` | Line-number gutter | **Unused** | none |
| `GutterStyle` | Gutter style | **Unused** | none |
| `FilterMode` | Initial filter-only state | **Exposed** `filter_mode:` | `component.go:393` |
| `HScrollbar` | | **Theme** (`true`) | none |
| (pass-throughs) | | see §2 | |

Setters and methods tui-builder calls: `AppendLines` (`lines:` and
`binding.go:295`), `Append` (stream, `screen.go:1812`), `Clear`,
`SetQuery` (`initial_query`), `SetTitle`, `SetLoading`, `State`/`Restore`.
Unused: `Prepend` (older-history loading, see `pkg/resume`), `AppendMarker`
(annotation lines such as "reconnected after 42s"), `SetFollow`,
`SetFilterMode`.

### 4.4 textview (`textview.Options`, built at `build/component.go:425`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Content` | Initial body | **Exposed** `content:` (also via `source:` → `SetContent`) | `component.go:428`, `binding.go:239` |
| `Wrap` | Word wrap | **Exposed** `wrap:` | `component.go:429` |
| `Searchable` | | **Exposed** `searchable:` | `component.go:430` |
| `MatchStyle` | | **Exposed** `colors.match` | `component.go:437` |
| `CurrentLineStyle` | | **Exposed** `colors.current_line_bg` | `component.go:440` |
| (pass-throughs) | | see §2 (no `HScrollbar` on textview) | |

Setters and methods tui-builder calls: `SetContent`, `SetQuery`, `SetTitle`,
`SetLoading`, `State`/`Restore`. Unused: `SetWrap` (the `w` key toggles it at
runtime).

### 4.5 tree (`tree.Options`, built at `build/component.go:494`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Root` | Top-level node | **Exposed** `root:` (static). With `source:` + `label`/`group_by`/`children`/`root_label`, **Indirect** via `SetRoot` / `ApplyRead`. | `component.go:508–526`, `binding.go:383,422` |
| `Searchable` | | **Exposed** `searchable:` | `component.go:505` |
| `InitialDepth` | Pre-expand depth | **Exposed** `initial_depth:` | `component.go:507` |
| `MatchStyle` / `CurrentLineStyle` | | **Exposed** `colors.match` / `colors.current_line_bg` | `component.go:532–536` |
| `HScrollbar` | | **Theme** (`true`) | none |
| `Markable` | | **Exposed** `markable:` | `component.go:506` |
| `MarkStyle` | | **Theme** | none |
| `Activity` | | **Theme** | none |
| `BusyWhen` | | **Exposed** `activity.*` | `component.go:497` |
| `Revision` | | **Unused** | none |
| (pass-throughs) | | see §2 | |

Setters and methods tui-builder calls: `SetRoot`, `SetQuery`, `SetCursor`,
`SetTitle`, `SetLoading`, `State`/`Restore`, `BeginRead`/`ApplyRead`,
`DispatchWith`/`Done`, `Selection`. Unused: `SetFilterMode`, `ExpandAll`/
`CollapseAll` (still reachable by key), the mark setters.

### 4.6 inspector (`inspector.Options`, built at `build/component.go:566`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Fields` | Top-level record | **Exposed** `fields:` (static, with `color_rules`). With `source:`, **Indirect** via `SetFields`, using `fields[].path` or `auto: true` → `inspector.FromAny`. | `component.go:572`, `binding.go:514–558` |
| `Filterable` | | **Exposed** `filterable:` | `component.go:569` |
| `InitialDepth` | | **Exposed** `initial_depth:` | `component.go:570` |
| `LabelStyle` / `ValueStyle` | | **Exposed** (fg) `colors.label` / `colors.value` | `component.go:579–583` |
| `MatchStyle` / `CurrentLineStyle` | | **Exposed** `colors.match` / `colors.current_line_bg` | `component.go:585–589` |
| `HScrollbar` | | **Theme** (`true`) | none |
| (pass-throughs) | | see §2 | |

Setters and methods tui-builder calls: `SetFields`, `SetQuery`, `SetCursor`,
`SetTitle`, `SetLoading`, `State`/`Restore`. Unused: `FromMap`,
`SetFilterMode`, `ExpandAll`/`CollapseAll`.

### 4.7 eventlog (`eventlog.Options`, built at `build/component.go:875`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Anchored` | Span vs logical-offset mode | **Indirect**: set by `remote.NewEventlog` from the shape (`window.cursor` present → Anchored) | `screen/window.go:132–155` |
| `Filterable` | Filter that narrows the query | **Exposed** `filterable:` | `component.go:879` |
| `Searchable` | Search that jumps to matches | **Exposed** `searchable:` | `component.go:878` |
| `MaxItems` | Resident cap | **Exposed** `max_items:` | `component.go:880` |
| `Placeholder` | Drawn for an unfetched item | **Unused** | none |
| `MatchStyle` | | **Exposed** (fg) `colors.match` | `component.go:894` |
| `SelectedStyle` | Cursor item | **Theme** (no `colors.*` key) | none |
| `CellStyle` | | **Theme** | none |
| `GutterStyle` | Gutter of positions/marks | **Theme** | none |
| `NoGutter` | Hide the gutter | **Unused** | none |
| `NewStyle` | Marks just-loaded items | **Theme** | none |
| `NewFor` | How long items stay marked new | **Unused** (tuilib default) | none |
| `HScrollbar` | | **Theme** (`true`) | none |
| (pass-throughs) | | see §2 (`colors.border_*`, `colors.spinner`) | `component.go:885–893` |

`eventlog.Item`:

| Field | Status | tui-builder location |
|---|---|---|
| `Key` | **Exposed** `key:` (dot-path). Anchored keys are prefixed with the cursor (`AnchoredKey`). | `component.go:931,948` |
| `Lines` | **Exposed** `text:` (split on `\n`, `color_rules` applied per line) | `component.go:925` |
| `Data` | **Indirect**: the raw item, for enter/inspector | `component.go:933` |
| `Mark` | **Exposed** `mark:` | `component.go:934` |
| `Hole` | **Unused** (read at `screen.go:1771`, never produced) | none |

Setters and methods tui-builder calls: `SetFollow` (`start: newest` for
Seekable, and on growing at `window.go:348`), `State`/`Restore` (stand-in
only). Every other paging setter goes through `remote.Eventlog`. Unused
directly: `SetMatchTotal`, `SetNew`, `Found`/`FoundAt`.

### 4.8 remote bindings (`remote.Seekable` / `remote.Anchored`, `screen/window.go:132–178`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Seekable.Page` | Fetch a window + total | **Indirect** (`windowPage` / `eventlogPage` over `data.sources.*.window`) | `window.go:146,171` |
| `Seekable.Find` | Server-side search; nil searches only what is loaded | **Unused** (nil) | none |
| `Seekable.Name` | Labels query-history lines (v0.32) | **Indirect**: the source name | `window.go:149,172` |
| `Seekable.PageSize` | | **Exposed** `data.sources.*.window.page_size` | `window.go:152,176` |
| `Seekable.Prefetch` | Extra pages beyond the viewport (v0.33) | **Exposed** `window.prefetch` | `window.go:152,176` |
| `Seekable.Follow` | Poll interval while Growing | **Exposed** `window.growing` + `window.follow_every`. Eventlog only; the Seekable table branch doesn't set it. | `window.go:153` |
| `Seekable.ViewportDelay` | Scroll settle time | **Unused** | none |
| `Seekable.Context` | Parent context | **Unused** | none |
| `Anchored.Edge` | Fetch an edge | **Indirect** (`eventlogEdge` / `tableEdge`) | `window.go:138,164` |
| `Anchored.Find` | Server search that re-anchors | **Unused** (nil) | none |
| `Anchored.Anchor` | Where the view starts (v0.34) | **Exposed** `window.anchor: newest\|oldest`. `source.At(cursor)` is never built. | `window.go:219` |
| `Anchored.Name` / `PageSize` | | **Indirect** / **Exposed** `window.page_size` | `window.go:139–140,165–166` |
| `Anchored.Follow` | | **Exposed** `window.growing`/`follow_every` (eventlog only; span tables don't set it) | `window.go:143` |
| `Anchored.ViewportDelay` / `Context` | | **Unused** | none |

Methods tui-builder calls on `remote.Table` / `remote.Eventlog`: `Update`,
`Refresh`, `Restyle`, `SetGrowing` (eventlog). Unused: `SetAnchor` (v0.34,
"jump to time"), `remote.Table.SetGrowing`.

### 4.9 form, input and toggle (prompts and action inputs)

There are two call sites. Boot prompts: `app.prompts` → `cmd/tui-builder/main.go:162`
(`collectAppPrompts`). Action inputs: `actions.*.inputs` → `screen.go:1521`
(`newInputForm`). Both choose the widget by type: `options:` → Select,
`type: bool` → Confirm, `mask: true` → Password, otherwise Text.

`form.Options`:

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Width` / `Height` | | **Indirect**: `SetRect` (boot: full screen minus margin; action: a 60-wide centred modal) | `main.go:288`, `screen.go:454` |
| `Fields` | | **Indirect**: built from the prompts / inputs | as above |
| `SubmitText` | Submit label | **Indirect**: hard-coded `"Run"` for action inputs. Boot prompts use the tuilib default. | `screen.go:1569` |
| `FieldSpacing` | Blank lines between fields | **Unused** | none |
| `Styles` | | **Theme** (`th.Form()`) | none |

Per-field option structs:

| Field | Status (boot prompts) | Status (action inputs) |
|---|---|---|
| `TextOptions.Key` / `Label` / `Placeholder` / `Initial` | **Exposed** `app.prompts[].key/label/placeholder/default`. `Initial` prefers the env var. | **Exposed** `actions.*.inputs.<name>` / `.label` / `.placeholder` / `.default` |
| `TextOptions.Required` | **Unused** | **Exposed** `inputs.*.required` |
| `TextOptions.Validate` | **Unused** | **Indirect**: derived from `inputs.*.type` (`int`, `duration`) (`screen.go:1580`) |
| `PasswordOptions.Key/Label/Placeholder/Initial` | **Exposed** (`mask: true`) | **Exposed** (`mask: true`) |
| `PasswordOptions.MaskChar` | **Unused** | **Unused** |
| `PasswordOptions.Required` / `Validate` | **Unused** | **Unused** (deliberately, per comment at `screen.go:1539`) |
| `SelectOptions.Key/Label/Options/Initial` | **Exposed** `options:` + `default:` | **Exposed** `options:` + `default:` |
| `SelectOptions.Height` | **Unused** | **Unused** |
| `SelectOptions.RequirePick` | **Unused** | **Unused** |
| `SelectOptions.Validate` | **Unused** | **Unused** |
| `ConfirmOptions.Key/Label/Initial` | **Exposed** `type: bool` + `default: "true"` | **Exposed** same |
| `ConfirmOptions.Validate` | **Unused** | **Unused** |

`input.Options` and `toggle.Options` are never built directly. tui-builder
reaches them only through `form.Text`/`form.Password` and `form.Confirm`, so
`input.CharLimit`, `input.Prompt`, `input.Echo`, `toggle.YesLabel`/`NoLabel`
and the rest are **Unused**.

### 4.10 confirm (`confirm.Options`, built at `screen.go:1665`)

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Width` / `Height` | | **Indirect**: measured from the wrapped message (minimum 60×7), applied through `layout.Center` | `screen.go:1683–1690`, `screen.go:441` |
| `Title` | | **Exposed**: the binding's `label:`, or `"Confirm"` | `screen.go:1667` |
| `Message` | | **Exposed** `confirm:` on the screen action binding (word-wrapped to 72) | `screen.go:1691` |
| `Confirm` / `Cancel` | Button labels | **Indirect**: hard-coded `"Yes"` / `"No"` | `screen.go:1692` |
| `Initial` | Which button starts highlighted | **Unused** | none |
| `SelectedStyle` / `UnselectedStyle` / `MessageStyle` | | **Theme** | none |
| (pass-throughs) | | see §2 (`app.borders.overlay` via theme) | |

This modal is for the screen's own paths: `enter` activation and runs that
needed an input form first (`screen.go:1292,1317,1333`). For menu picks, the binding's
`confirm:` instead goes to `taction.Action.Confirm` (`actions_menu.go:153`),
and the app shell draws its own confirm from the theme.

### 4.11 action menu (`pkg/action`, `screen/actions_menu.go`)

`action.Action`, built per binding in `menuAction` (`actions_menu.go:86`):

| Field | tuilib gist | Status | tui-builder location |
|---|---|---|---|
| `Label` | Verb name | **Exposed** binding `label:`. Falls back to `open <screen>` or the action name. | `actions_menu.go:196` |
| `Desc` | Gloss beside the label | **Exposed** `actions.*.description` | `actions_menu.go:89` |
| `Key` | Optional shortcut | **Unused**. `actions_menu.go` never sets it, and there's no `key.NewBinding` in `internal/` apart from `enter` at `screen.go:1018`. A binding's `key:` only takes effect when it is `enter` (`screen.go:540`, `activate()` at `:1207`). Other values pass validation but neither fire directly nor reach the menu. | none |
| `Receipt` | Statusbar/log text on return | **Indirect**: `resolved.Receipt()` gives `"<claim.label> requested\|completed"` when `claim:` is set and `message:` isn't (`internal/action/action.go:213`) | `actions_menu.go:174` |
| `Confirm` | Yes/no between pick and run | **Exposed** binding `confirm:` (with a fan-out row count appended) | `actions_menu.go:140–153` |
| `Disabled` | Dimmed row + reason | **Indirect**: "focus <pane>", or a resolve error | `actions_menu.go:95,137` |
| `Multi` | Accepts a multi-selection | **Exposed** `actions.*.multi` | `actions_menu.go:104` |
| `Exclusive` | One run per target | **Exposed** `actions.*.exclusive` (`IsExclusive`) | `actions_menu.go:108` |
| `ID` | Scopes the Exclusive check | **Unused** (falls back to `Ident()` = label) | none |
| `Run` | Background work | **Indirect** (`runFunc`) | `actions_menu.go:187,218` |
| `Do` | Escape hatch | **Indirect**: push, prompted inputs, fan-out, interactive | `actions_menu.go:113–184` |

`action.Set`: `Target` and `Count` are **Indirect** (`selectionTarget`,
`actions_menu.go:53`). `Targets` is **Unused**. `action.Options` (menu
`Title`, styles, `MultiReason`, `RunningReason`, `Keys`) is **Unused**,
because `app.Options.Actions` is never set and the shell uses theme defaults.

### 4.12 help (`pkg/help`)

| Item | Status | tui-builder location |
|---|---|---|
| `?` overlay (`help.Overlay`) | **Indirect**: always on. The shell owns it, and `HelpKey` defaults to `?`. | `app.New` defaults |
| `help.Section` content | **Indirect**: `Model.HelpSections()` composes the component sections and the screen's verbs | `screen.go:989–1028` |
| `help.Options` (`KeyStyle`, `DescStyle`, `ShortSeparator`, `Minimal`) | **Unused** / **Theme**. The shell builds it from `th.Help()`. | none |
| `help.OverlayOptions` (`Title`, `Searchable`, styles, `Filter`, `Keys`) | **Unused** / **Theme** (`th.HelpOverlay()`). `Searchable` follows `DisableHelpSearch` (unset, so on). | none |
| `help.Qualify` | **Unused**, on purpose (comment at `screen.go:987`) | none |

### 4.13 glyph and theme (`app.glyphs`, `app.borders`, `app.theme`)

`glyph.Set` has 16 fields. `app.glyphs` exposes 13 of them: `cursor`, `mark`,
`expand_open`, `expand_closed`, `rule`, `scroll_thumb`, `scroll_track`,
`h_scroll_thumb`, `h_scroll_track`, `sort_asc`, `sort_desc`, `column_sep`,
`placeholder` (`build/chrome.go:51`). **Unused:** `ActivityOK`,
`ActivityFail`, `ActivityChanged`.

`theme.Theme`: `BorderShapeActive`/`Inactive`/`Overlay` and `SlotBrackets`
are **Exposed** through `app.borders.*`. The colour tokens are readable as
`theme:<token>` in any `colors.*` / `color_rules` spec (`component.go:743`).
`theme.Theme` has no YAML of its own: there's no custom palette and no
`FromBase16` entry point. `app.theme` picks one by `Name` from `theme.All()`.

---

## 5. Supporting packages tui-builder uses

| Package / item | Status | tui-builder location |
|---|---|---|
| `activity.DispatchOptions.Settle` (v0.35) | **Exposed** `actions.*.claim.settle` | `build/mark.go:193–203` (`DispatchWith`) |
| `activity.Options.Settle` (component default) | **Theme** | none |
| `activity.Mode` (`Held` / `Observed`) | **Exposed** `actions.*.claim.until` | `screen/claims.go:78` |
| `activity.Busy` / `Settled` helpers | **Unused**. tui-builder's own `busyFunc` does this. | none |
| `runner.Run` / `RunWithNotice` | **Exposed** binding `interactive:` + `notice:` | `screen.go:1498` |
| `runner.Options.NoClear` | **Unused** | none |
| `runner.CaptureWith` | **Indirect** (`CaptureOptions.Cmd` only. `Label` and `Tag` aren't set.) | `screen.go:1502` |
| `runner.GoWith` | **Indirect** (fan-out / keyboard runs, `Tag` = `RunKey`) | `actions_menu.go:299`, `screen.go:1310` |
| `runner.CaptureStatus` / `Progress` | **Unused** (named out of scope in the 0.31 plan) | none |
| `layout.VStack/HStack/ZStack/Flex/Fixed/Sized/Center` | **Exposed** `layout:` `vstack`/`hstack`/`zstack`/`flex`/`fixed` | `build/layout.go` |
| `screen.Push` | **Exposed** `actions.*.push` | `screen.go:1737` |
| `screen.Pop` / `PopTo` / `Replace` | **Unused** (esc→pop comes from the shell) | none |
| `source.Newest` / `Oldest` | **Exposed** `window.anchor` | `screen/window.go:219` |
| `source.At(cursor)` | **Unused** | none |
| `focus.Group` | **Unused**. The screen keeps its own focus order. | none |

## 6. Packages tui-builder doesn't wrap

For completeness, this is each one's Options surface. None of it is reachable
from YAML.

| Package | Options / main API | Reached indirectly? |
|---|---|---|
| tab | `Tabs`, `Theme`, `Initial`, `PrevKey`/`NextKey`, `DisableNumberKeys`, `StripPos`, `*Style`, `Separator` | No. See `plans/tabs.md`. |
| metrics | `Badge`, `Ratio`, `Bar`, `BarStyled`, `Spark`, `SparkStyled` (functions, no Options) | No |
| breadcrumb | `Crumbs`, `Separator`, `Ellipsis`, `Bar*`, `*Style` | Yes, through the shell, from screen titles |
| alert | `Title`, `Message`, `OK`, styles, `Autosize` | Yes, the shell uses it internally |
| input | `Placeholder`, `Initial`, `Prompt`, `CharLimit`, `Echo`, `MaskChar`, styles | Only via `form.Text` / `form.Password` |
| toggle | `Initial`, `YesLabel`/`NoLabel`, styles | Only via `form.Confirm` |
| filter | `Title`, `Prompt`, `Placeholder`, `CharLimit`, styles | Only via `<component>.Options.Filter` (`Placeholder` exposed) |
| statusbar | `Left`/`Right`, `Bar*`, `*Style` | Yes, through the shell (`app.version` → right slot) |
| output | `MaxRecords`, `ExportDir`, `SourceWidth`, colours, `Badge*Style`, `Logview`, `Confirm`, `Picker`, `Keys` | Yes, through the shell when `app.output_key` is bound. All Options are left at their defaults. |
| config | `Theme`, `DoubleClickMs` | Yes, `app.New` reads the user's file (§3) |
| poll | `Interval`, `Paused` | No. tui-builder runs its own `refresh:` timer. |
| resume | `Granularity`; `Tracker` (`Observe`, `Since`, `Lost`, `Resume`, `Drop`, `OlderCut`) | No |

---

## 7. What tuilib added in v0.32–v0.35

The 0.31 plan (`plans/tuilib-0.31.md`) bumped to v0.32.0 and lists only the
v0.32 `Name` addition. Here is everything exported that changed between
v0.31.0 and v0.35.0, from a `go/ast` diff of `pkg/` plus the release notes.
There are no new packages. `diff -rq` shows changed non-test files only in
activity, app, eventlog, list, remote, source, table and tree.

| Version | PR | Addition | tui-builder today |
|---|---|---|---|
| v0.32.0 | [tuilib#103](https://github.com/jsdrews/tuilib/pull/103) | `source.Options.Name`, `source.AnchoredOptions.Name`, `remote.Seekable.Name`, `remote.Anchored.Name`. `Name` on `QueryAnswered/Cancelled/Failed/RecoveredMsg`. The app shell prefixes query-history lines with `name: ` (`app.go` `sourcePrefix`). | Set to the source name (`window.go:139,149,165,172`) |
| v0.33.0 | [tuilib#105](https://github.com/jsdrews/tuilib/pull/105) | `remote.Seekable.Prefetch`, passed through to `source.Options.Prefetch` | **Exposed** `window.prefetch` (`window.go:152,176`) |
| v0.34.0 | [tuilib#107](https://github.com/jsdrews/tuilib/pull/107) | `source.AnchoredOptions.Anchor` and `remote.Anchored.Anchor` (start anchor; `NewAnchored` now starts at `opts.Anchor`, not always Newest). `source.Anchor.IsOldest()`, `Anchor.Cursor()`. `remote.Eventlog.SetAnchor`, `remote.Table.SetAnchor` ("jump to time"). `eventlog.Model.Reanchor`, `table.Model.Reanchor` (the next page replaces the span). | `Anchor` **Exposed** as `window.anchor` (newest/oldest). `SetAnchor`, `source.At` and `Reanchor` are **Unused**. |
| v0.35.0 | [tuilib#109](https://github.com/jsdrews/tuilib/pull/109) | `activity.DispatchOptions{Settle *int}`. `activity.Set.DispatchWith`. `DispatchWith` / `DispatchEachWith` on list, table and tree. | `DispatchWith` used for `claim.settle` (`build/mark.go:193`). `DispatchEachWith` is **Unused**. |

---

## 8. Biggest gaps per component

These are counts of the fields marked **Unused** or **Theme** above, minus the
§2 pass-throughs. They aren't recommendations.

- **app**: `QuitKey`, `HelpKey`, `SuspendKey`/`DisableSuspend`,
  `DisableAutoEscPop`, `DoubleClickInterval`, `ThemeEnvVar`, `SkipConfig`,
  `DisableHelpSearch`, `Actions` (menu options), `Output` (console options).
  `app.title` reaches no tuilib field.
- **list / table / tree**: `Revision`, `MarkStyle`, `LoadingLabel`, `Keys`.
  On table, also `Placeholder`. `SetDistinct` is never called, so a remote
  filter's `key:value` hints have no candidates.
- **logview**: `LineNumbers`, `GutterStyle`, `Prepend`, `AppendMarker`,
  `SetFollow`.
- **eventlog**: `Placeholder`, `NoGutter`, `NewFor`, and the `SelectedStyle`/
  `CellStyle`/`GutterStyle`/`NewStyle` colours (no `colors.*` keys).
  `Item.Hole` is never produced.
- **remote**: `Find` on both shapes, so search covers only loaded items.
  `ViewportDelay`, `Context`, `SetAnchor`, `source.At`. `Follow` isn't set
  for windowed tables.
- **form / prompts**: `FieldSpacing`, `MaskChar`, `RequirePick`,
  `SelectOptions.Height`, every non-Text `Validate`. Boot prompts don't wire
  `Required` / `Validate` at all.
- **confirm**: `Initial`, and button labels hard-coded to Yes/No.
- **action menu**: `Action.Key`. A non-`enter` binding `key:` does nothing
  at runtime.
  `Action.ID`, `Set.Targets`, all of `action.Options`.
- **glyph**: `ActivityOK`, `ActivityFail`, `ActivityChanged`.
- **textview / inspector**: nothing component-specific beyond `LoadingLabel`
  and `Keys`. Inspector also leaves `FromMap` and `SetFilterMode` uncalled.
