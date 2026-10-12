# Docs audit: where the zensical site and reference are stale

Research for [#49](https://github.com/jsdrews/tui-builder/issues/49). Facts
only, with no fixes proposed. The docs were not edited.

Checked against `main` at `24b10d5` (tuilib pinned at v0.35.0). Sources of
truth: `internal/config` (structs and validators), `internal/datasource`,
`internal/expr`, `internal/build`, `cmd/*` flags, `examples/`, and tuilib
v0.35.0 from the module cache for theme names and table keys.

Legend: **missing** means the code has it and the docs don't. **Wrong** means
the docs contradict the code. **Stale** means the docs were true before a
later change.

## Headline

- The site builds cleanly. `zensical build --strict` (zensical 0.0.69)
  reports "No issues found", so every snippet include and internal link
  resolves.
- All 48 `examples/*.yaml` are on `docs/examples.md` and all of them load.
  Both facts are enforced by tests.
- Every config struct field has a reference row (the coverage test passes).
  The gaps below are in the **values, rules and prose**, which no test checks:
  - `max_pages: 0` is documented as unlimited, but it means 20.
  - `pipe:` is documented on every operator, but only a leaf or
    `passthrough` accepts it.
  - Binding `from:` omits `eventlog`.
  - The expression contexts and functions are incomplete.
  - `data-layer.md` predates windowed and Anchored sources.
- The guide pages, CLI pages, quickstart and data-layer page have no
  automated check at all.

## Build

```
python3 -m venv .venv-docs
.venv-docs/bin/pip install -r docs-requirements.txt   # zensical==0.0.69
.venv-docs/bin/zensical build --strict
→ Build started / No issues found / Build finished in 0.48s (exit 0)
```

This is the same command as `task docs:build` (`Taskfile.yml:60-64`) and
`.github/workflows/docs.yml:27`. `pymdownx.snippets.check_paths = true`
(`zensical.toml`) makes a missing include fail the build, so a clean strict
build means every `--8<--` include resolves.

## Reference-coverage test

`internal/config/reference_test.go`, `TestReferenceDocumentsEveryField`:

- **What it does:** it reflects over every struct reachable from
  `config.Config`. Each exported field's YAML name must appear as the first
  backticked cell of a table row (`^| \`name\` |`) on the page or pages that
  `referencePages` assigns to that struct. A struct that has no
  `referencePages` entry fails the test. Seven fields are exempt through
  `documentedInline`. The test passes on `main`.
- **What it doesn't cover:**
  - Only `docs/reference/*.md` is read. The guide pages, `quickstart.md`,
    `data-layer.md`, `cli/*.md` and `index.md` are never checked.
  - It checks that a row exists, not what the row says. Type, default,
    valid-on and description are unchecked, which is how the `max_pages`
    default below slipped through.
  - The match is page-wide, not section-wide. A name such as `label`,
    `source` or `value` passes if any table on the page has that row.
  - Enum values are unchecked: component kinds, source and operator kinds,
    paginate strategies, theme names, border shapes, sort comparators,
    `until`, `anchor` and `start`.
  - Validation rules are unchecked: the **Validation:** bullets are prose.
  - Template tokens, expression contexts and built-in functions are
    unchecked.
  - CLI flags are unchecked. Nothing compares the cobra flags in
    `cmd/wrangl/main.go:138-150` with `docs/cli/wrangl.md`.
  - Fields tagged `yaml:"-"` (`Component.Windowed`, `Component.Spanned`,
    `Growing.Always`) are skipped. So is the scalar `growing: true` form,
    which comes from a custom unmarshaller.
- **The neighbouring tests** are in `internal/config/examples_test.go`.
  `TestEveryExampleLoads` runs `config.Load` on every example.
  `TestExamplesGalleryListsEveryExample` requires
  `--8<-- "examples/NAME.yaml"` on `docs/examples.md` for each one. Inline
  YAML in the docs pages is never loaded. For this audit, the
  `quickstart.md` configs were loaded by hand with `wrangl --list` and
  `--list-actions`, and both loaded cleanly.

## Gaps by page

### reference/sources.md

| # | Status | Doc | Code |
|---|---|---|---|
| S1 | **wrong** | Line 218, `paginate.max_pages`: "`0` is unlimited". | `internal/datasource/http.go:171-174`: `if max == 0 { max = defaultMaxPages }` (20). `0` and unset both mean 20, and no value means unlimited. The struct comment at `internal/config/config.go:132` repeats the same error. |
| S2 | **wrong** (incomplete) | Line 376, window validation: on http, "at least one of `search_param` / `filters`" is required. | `internal/config/source.go:589-592`: an Anchored http window is also accepted when `${window.search}` appears in `body:` or `older:`/`newer:` (`bodySearch`). |
| S3 | missing | The `window.anchor` row says "with `cursor`". | `source.go` `validateShared` rejects `anchor:` without `cursor:` with an error that points to the component's `start:`. The validation list doesn't mention this. |

### reference/pipelines.md

| # | Status | Doc | Code |
|---|---|---|---|
| P1 | **wrong** | Line 40: `pipe` is a field "shared by operators", described as "Further stages applied to this operator's output". Line 250 says pipe is valid on "leaf kinds except `merge`; operators". | `internal/config/source_desugar.go:33-62` expands `pipe:` only on leaves (phase 1) and `passthrough` (phase 2). On any other operator, the stages are left unexpanded and fail validation. Reproduced with `type: filter` plus `pipe: [{type: filter, …}]`, which `wrangl` rejected with `validate …: data.sources.big.pipe[0].filter: \`from:\` is required`. |

### reference/components.md

| # | Status | Doc | Code |
|---|---|---|---|
| C1 | **wrong** (stale) | Line 50: "`markable` with a windowed source is an error: a window holds rows without keys." | `internal/config/load.go:274`: `if c.Windowed && !c.Spanned`. A span table (over an Anchored source) can be markable. The `table` section of the same page says "marking works", so the page contradicts itself. |

The rest of the page was checked against `load.go` and matched: the seven
kinds, the `eventlog`, `activity`, span-table, `max_items`, `sort_debounce`
and `on_cursor` rules, and the colors.

### reference/actions.md

| # | Status | Doc | Code |
|---|---|---|---|
| A1 | **wrong** (stale) | Line 162 (`from` row: "The list, table or tree…") and line 173 ("It must be a list, table or tree placed on this screen"). | `internal/config/action_validate.go:277`: `case "list", "table", "tree", "eventlog"`. `components.md`'s eventlog section documents `key: enter` bindings with the eventlog as `from:`, which contradicts this page. |

The kinds, claims (`label`, `until`, `settle`, default 1), inputs, reserved
keys and the 30s http timeout all match `action.go`, `action_validate.go`
and `internal/action/action.go:40`.

### reference/templating.md

| # | Status | Doc | Code |
|---|---|---|---|
| T1 | missing | Line 107, functions: "plus `lower`, `upper` and `now`". | `internal/expr/expr.go:136-152` also registers `parseTime` (RFC3339Nano, RFC3339, `2006-01-02T15:04:05Z`, `2006-01-02`). |
| T2 | missing (stale) | Lines 9 and 54 list the expression fields as `where`, `keep`, `compute`, `by`, `on` and `success`. | `activity.busy_when` (`load.go`, compiled with `expr.Compile`) and `window.growing.while` (`load.go` `validateGrowing`) are also expressions. Both shipped after this list was written. |
| T3 | **wrong** (stale) | Line 29: `${window.*}` is valid in "A windowed exec source's `command`". | Anchored http windows also substitute `${window.cursor}`, `${window.limit}`, `${window.search}` and `${window.filters.NAME}` in `body:`, `older:` and `newer:`. `sources.md` (Anchored windows) documents this, but the token table doesn't. |
| T4 | missing | Line 22: `${selection}` is "a list's item, a table's first cell, a tree node's label". | On an eventlog, `${selection}` is the item's key and `${selection.PATH}` reads the item's fields (`internal/build/template.go:431`, `ItemSelection`). `guide/templating.md` and `components.md` say this, but the reference table doesn't. |
| T5 | missing | Line 138, durations: "Used by `refresh`, `timeout`, `ttl` and `type: duration` inputs". | `sort_debounce` (`load.go:460`) and `window.follow_every` (`source.go:541`) are durations too. |

### reference/app.md

| # | Status | Doc | Code |
|---|---|---|---|
| L1 | **wrong** | Lines 224-225: `flex` and `fixed` are "set one of `flex` / `fixed`". | Nothing validates this. `internal/build/layout.go:113-121` uses `fixed` when it's positive and ignores `flex`, so setting both is silently accepted. |

The theme names match tuilib v0.35.0 `theme.All()`. The border shapes and
slot brackets match `config.go:457-462`, and the glyph list matches
`validateChrome`.

### reference/README.md

| # | Status | Doc | Code |
|---|---|---|---|
| R1 | stale | Line 11: `components.md` covers "list, table, logview, textview, tree, inspector, plus columns, color rules, colors and `on_cursor:`". | The page also covers `eventlog` and `activity`, which were added with the eventlog and activity PRs. |

### guide/components.md

| # | Status | Doc | Code |
|---|---|---|---|
| G1 | stale | Line 45: "`filterable: true` (list, table, inspector)". | Eventlogs are filterable too, with `f` (`reference/components.md` common fields, `internal/build/component.go:879`). The same page's eventlog example uses `filterable: true`. |
| G2 | stale | Line 55: "`searchable: true` (logview, textview, tree)". | Eventlogs are searchable too (`component.go:878`). |

The sort keys (`[`, `]`, `s`) and the mark keys (`x`, `X`, `A`, `D`) match
tuilib v0.35.0 `pkg/table/table.go:486-492`.

### guide/templating.md

| # | Status | Doc | Code |
|---|---|---|---|
| GT1 | stale | Line 51: "Pipeline operators and an action's `success:` take expressions". | `activity.busy_when` and `window.growing.while` are not mentioned (see T2). |
| GT2 | missing | The namespaces table has no `${window.*}` row. | The `${window.*}` tokens are part of every windowed exec source and every Anchored http body (see T3). |

### guide/app.md

| # | Status | Doc | Code |
|---|---|---|---|
| GA1 | missing | Lines 28-37, "The shell always provides": the key list has `q` but not `ctrl+c` or `ctrl+z`. | `action_validate.go` `reservedKeys` reserves `ctrl+c` (quit) and `ctrl+z` (suspend). `reference/actions.md` lists both. |
| GA2 | missing | Line 33: "`t` Cycles the theme". | Since `b057705` ("Carry component state across theme swaps with State / Restore"), a theme swap keeps the data, cursor, filter, sort, marks, expansion, scroll and remote query (per that commit message). No docs page says so. |
| GA3 | **wrong** (partial) | `reference/templating.md:134` says a theme token "Follows the active palette, including when themes cycle". `reference/components.md:363` says the same. | The `b057705` commit message: "Rendered cells keep the colors they were rendered with, so a `theme:` token in a color rule shows the old palette until the next fetch." |

### data-layer.md

| # | Status | Doc | Code |
|---|---|---|---|
| D1 | stale | Lines 27-43, "The model": shows only `Source` and `StreamingSource`, and says "The TUI and `wrangl` consume sources through these interfaces and nothing else." | `internal/datasource/window.go:28` (`WindowedSource`, `FetchWindow`) and `internal/datasource/anchored.go:19` (`AnchoredSource`, `FetchEdge`) are also consumed, by `internal/screen/window.go`. |
| D2 | stale | Lines 45-58, "Lifecycle": the cadences are streamed, polled and one-shot. | A windowed source with `growing:` polls at `window.follow_every` (default `2s`, `config.go:269`), and `refresh:` is rejected alongside it, so it isn't covered by "polled = `refresh:` is set". `wrangl --list` labels windowed sources with the same three cadences (`cmd/wrangl/main.go:388-404`). This page doesn't mention windowed sources at all. `cli/wrangl.md` does note that `wrangl` returns a windowed source's first page. |

### cli/tui-builder.md

| # | Status | Doc | Code |
|---|---|---|---|
| CL1 | missing | Line 50 mentions `example-launcher` but not its flag. | `cmd/example-launcher/main.go:33`: `-dir` (the directory of `*.yaml`, defaulting to `./examples` or `../examples`). |

"No flags" is correct for `tui-builder` (`cmd/tui-builder/main.go:39-43`
reads `os.Args[1]` only).

### cli/wrangl.md

No gaps. Every flag in `cmd/wrangl/main.go:138-150` has a row: `--list`,
`-a/--all`, `--describe`, `--describe-action`, `--list-actions`, `--param`,
`--pretty`, `--raw`, `--limit` and `--for`. "The default with no target" is
also correct (`main.go:217-220`).

### quickstart.md, index.md, examples.md

- `quickstart.md`: both configs (`hello.yaml`, and `users.yaml` with
  `biz_users` and `open_site` merged in) load cleanly with `wrangl`.
  `go 1.25.1` in `go.mod` agrees with "Go 1.25 or later". `task build` does
  produce the three binaries listed (`Taskfile.yml:9-24`).
- `examples.md`: lists all 48 examples, and each one is a snippet include
  that resolves. The new eventlog, anchored, span_table and activity
  examples are present.
- `index.md`: no gaps found.

## Behavior changes vs docs (tuilib v0.32–v0.35 and later PRs)

| Change | Where it's documented | Status |
|---|---|---|
| Windowed tables on tuilib `pkg/remote` (v0.33: dimmed rows while a query is in flight, `prefetch`) | `reference/components.md` (table, windowed), `reference/sources.md` (`prefetch`) | documented |
| `sort_debounce` | `reference/components.md` | documented (missing from the durations list, T5) |
| eventlog | `reference/components.md`, `guide/components.md`, `examples.md` | documented, but G1, G2, A1 and T4 above |
| Anchored windows and `window.anchor` (v0.34) | `reference/sources.md`, `guide/data.md` | documented, but S2, S3 and T3 above |
| Span tables | `reference/components.md`, `guide/components.md` | documented, but C1 above |
| `activity` and "reads failing" | `reference/components.md`, `guide/components.md` | documented, but T2 and R1 above |
| Claims with a per-claim settle (v0.35) | `reference/actions.md`, `guide/actions.md` | documented |
| State/Restore across theme swaps (`b057705`) | none | missing (GA2), and the theme-token claim is partly wrong (GA3) |
