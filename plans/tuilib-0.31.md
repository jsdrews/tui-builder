# Adopting tuilib v0.26 – v0.32

The bump from `v0.25.0` to `v0.32.0` is on this branch, and vet and tests
are green. This plan covers what to do with the new surface. It's locked:
every schema decision was made on the map
[Map: adopting tuilib v0.26–v0.31](https://github.com/jsdrews/tui-builder/issues/21),
and each workstream links the ticket that holds its detail. Ten PRs, in
the order at the bottom.

## The bump itself (done)

`go.mod` → `v0.32.0`. Three changes touched us:

- **`table.SetWindow` takes an `Answer`**, which names the query the rows
  answer. `windowFetchedMsg` carries it, built from the query that was
  sent; `ApplyWindow` and the rebuild in `internal/build/component.go`
  pass it through.
- **`source.Model.Deliver` returns `(bool, tea.Cmd)`**, and failures go
  through `Page.Err`. A failed fetch now calls `Deliver` with the error
  and `table.SetFailed`. The coordinator also schedules its own messages
  (`ViewportDelay` settling, polls), so `updateWindows` forwards every
  message to each coordinator. Without that, scrolled windows never
  fetched.
- **`screen.Stack` sends non-input messages to every stacked screen**, not
  just the top one. A pushed screen is built from the same config, with
  the same source names, so a covered screen would paint the top screen's
  fetch into its own table, or run the top screen's menu pick against its
  own selection. `internal/screen/scope.go` gives each `Model` an id and
  wraps its internal messages in a `scopedMsg`; `Update` drops other
  screens' messages. Runner outcomes are matched by the `*exec.Cmd` the
  screen launched. `TestStackedScreensIgnoreEachOthersMessages` covers it.

v0.32.0 adds one thing: query history messages carry the coordinator's
`Name` (asked for in
[jsdrews/tuilib#102](https://github.com/jsdrews/tuilib/issues/102)), which
Workstream 5 uses.

Everything below is additive.

## What v0.26–v0.32 give us

| Feature | Package | Workstream |
|---|---|---|
| Row activity: busy rows spin with their status; reads failing | `pkg/activity`, table/list/tree | 6 |
| Claims: a write marks its target rows busy until the truth catches up | `pkg/activity` (`Dispatch`, `Done`, `UnobservedMsg`) | 7 |
| Committed vs answered queries: stale rows stay dimmed, failures keep the input | `pkg/table` (`Answer`, `SetFailed`, `Stale`) | 5 |
| Query history in the console (answered / failed / cancelled / recovered) | `pkg/source`, `pkg/app` | 5 |
| Remote bindings that do the routing for a component | `pkg/remote` (`NewTable`, `NewEventlog`) | 5, 8 |
| `State()` / `Restore()` on every component | all components | 4 |
| Eventlog: paged Events and Lines, follow, search vs filter, inspector | `pkg/eventlog` | 8 |
| Anchored data: walk from an anchor by cursor, no offsets | `source.Anchored`, `remote.Anchored` | 9 |
| Table span mode | `table.Options.Anchored`, `AppendRows` / `PrependRows` | 10 |

Not adopted: run progress (`runner.CaptureStatus`); see Out of scope.

The vocabulary below is tuilib's (`CONTEXT.md` in tuilib): Activity,
Claim, Busy/Settled, Committed/Answered query, Stale, Seekable/Anchored,
Anchor, Span, Edge, Growing, Follow. Data is **Seekable** when item N can
be fetched directly with a total, and **Anchored** when it can only be
walked from an anchor (newest, oldest, or a given item) by cursor.

---

## Workstream 1 — the bump and this plan

This PR: [Bump tuilib to v0.32.0](https://github.com/jsdrews/tui-builder/pull/20).
Merging it locks the plan.

## Workstream 2 — reference docs

Decided in [Config schema reference docs](https://github.com/jsdrews/tui-builder/issues/30).

- `docs/reference/`, hand-written, one page per area and one section per
  kind:
  - `app.md`: top level, screens, layout, chrome, theme, env, params
  - `components.md`
  - `sources.md`: each kind, plus windowed and (later) Anchored
  - `pipelines.md`: operators
  - `actions.md`: actions, bindings, inputs/prompts
  - `templating.md`: every `${…}` token and where it's valid
- Each kind gets a table with the columns **Field | Type | Default | Valid
  on/with | What it does**, then its validation rules (exclusions,
  requirements) and one minimal YAML example.
- It documents the schema as it stands after the bump.
- It replaces `docs/components.md` and `docs/data-layer.md`:
  - The data-layer prose moves into `sources.md` / `pipelines.md`.
  - A short `docs/data-layer.md` concept page keeps the architecture and
    wrangl sections.
  - The gap-analysis history in `components.md` is deleted; git keeps it.
  - AGENTS.md points at `docs/reference/` instead.

**Tests.** A test walks the `yaml:` tags of every `internal/config`
struct and fails if a field is missing from the reference. From here on,
every feature PR updates the reference in the same PR, and this test
enforces it.

## Workstream 3 — docs site

Decided in [Config schema reference docs](https://github.com/jsdrews/tui-builder/issues/30).

- MkDocs with the Material theme, built from `docs/`. Keep `mkdocs.yml`
  compatible with Zensical.
- Nav:
  - **Home**
  - **Quick start**: install, a first ~15-line YAML, run it, then add a
    source and an action.
  - **Guide**: one page per config section, in this order: app &
    screens/layout → data (sources, pipelines, params) → components →
    actions → templating.
  - **Reference**: Workstream 2's pages.
  - **CLIs**: `tui-builder` (flags, multi-screen, env) and `wrangl`
    (commands, output contract).
  - **Examples**: a gallery. `example-launcher` gets one line here
    (`task examples`), no CLI page.
- Full examples are embedded from `examples/*.yaml` with snippet includes,
  so every complete config on the site is a file CI already loads. Short
  fragments stay inline.
- A workflow deploys to GitHub Pages on push to `main`. PR CI runs
  `mkdocs build --strict`, so broken links and missing snippets fail.

Feature PRs after this update the reference, and the guide pages where a
concept changes.

## Workstream 4 — `State()` / `Restore()`

Agreed on the map without a ticket. Internal, no schema.

Replace the hand-written rebuild in `internal/build/component.go`, which
copies view state field by field across a theme or config rebuild, with
each component's `State()` and `Restore()`.

**Tests.** The existing rebuild tests (marks survive reorder, filter and
theme rebuild) stay green unchanged.

## Workstream 5 — windowed tables onto `pkg/remote`

Decided in [Stale and failed queries on windowed tables](https://github.com/jsdrews/tui-builder/issues/24).
Facts from [What tuilib's paging can express that our window: config can't](https://github.com/jsdrews/tui-builder/issues/25).

Windowed tables are built with `remote.NewTable(Seekable)`, whose `Page`
wraps `ds.WindowedSource`. That replaces most of
`internal/screen/window.go`, and `remote` gives most of the behaviour
below directly.

**Behaviour (always on, no config):**

- **Committing a filter or sort** keeps the old rows visible and dimmed,
  and the border names the query that's loading. Remove the
  `SetCursor(0)` and `setLoading` in `handleWindowQuery`.
- **A failed query** keeps the rows stale, the border marks the query
  failed, the input keeps what was typed, and `r` retries. Delete our
  `windowError`; the console report comes only from query history.
- **Query history.** "answered in 1.2s", failed, cancelled and recovered
  lines go to the console, prefixed with the source name
  (`source.Options.Name`, v0.32), e.g. `books: filter x → answered in 1.2s`.
  Page and poll fetches aren't logged.
- **Cancellation.** `FetchWindow` gets `Query.Ctx`, not
  `context.Background()`, so a superseded fetch stops.

**Schema:**

```yaml
books:
  type: table
  source: books          # window: source
  sort_debounce: 300ms   # optional
```

- `sort_debounce:` is a duration. Omitted means tuilib's
  `DefaultSortDebounce` (400ms). `0` commits every sort change at once
  (tuilib's negative value).
- It's a validation error on a table that isn't windowed, because local
  sorts are instant.

**Tests.** The existing window e2e tests (scroll fetches the next window,
manual refresh keeps place, exec window) stay green. New ones:
- a commit leaves the old rows dimmed until the answer lands
- a failure keeps the rows and the typed input
- a superseded fetch's context is cancelled
- history lines carry the source name
- `sort_debounce` validation

## Workstream 6 — activity

Decided in [How a row says it's busy](https://github.com/jsdrews/tui-builder/issues/22).

```yaml
pods:
  type: table
  source: pods
  mark_key: metadata.uid          # required on polled tables with activity:
  activity:
    status: status.phase          # a path; the label shown while busy
    busy: [Pending, Terminating]  # exact values, or…
    # busy_when: 'observedGeneration < generation'   # …an expression; not both
    column: Status                # table only; optional
```

- **Without `activity:`**, nothing changes. **With it**, every read
  evaluates each row. A busy row shows a spinner plus its status value
  (`⣾ Pending`) in the activity column; other rows render normally.
- **Busy rule:** exactly one of `busy:` (exact values) or `busy_when:` (an
  `internal/expr` expression over the item).
- **`column:`** defaults to the column whose `value:` is the `status:`
  path. If none matches, `column:` is required. Lists and trees have no
  column; the spinner draws before the item.
- **Identity:** `mark_key:` keeps its name and is required on polled
  tables with `activity:`. Lists key on the item and trees on the node
  path, as today.
- **Allowed on** table, list and tree bound to a fetched or polled
  source. Streaming and windowed bindings are validation errors.
- **Reads failing:** every polled component shows "· reads failing" in its
  title once reads start failing. Always on, no config.

**Tests.** A row turns busy and settles across polls; `busy` vs
`busy_when` exclusivity; the `column:` default and its validation; the
streaming/windowed rejection; the "reads failing" title.

## Workstream 7 — claims

Decided in [Do actions claim their target rows?](https://github.com/jsdrews/tui-builder/issues/23).
Needs Workstream 6.

```yaml
actions:
  restart:
    run: [kubectl, rollout, restart, "deploy/${selection.name}"]
    claim: Restarting                 # shorthand = {label: Restarting, until: done}
  sync:
    method: POST
    url: ".../applications/${selection.name}/sync"
    claim: {label: Running, until: status, settle: 2}
```

- **Opt-in, on the action.** Actions without `claim:` behave exactly as
  today. `label` is required; use the server's own word for the work.
- **Targets** are the binding's selection (marked rows or the cursor row,
  keyed by `mark_key`). The component must have `activity:`; otherwise
  the binding fails validation.
- **`until: done`** (default, `activity.Held`): the row spins while the
  action runs and stops when it returns. **`until: status`**
  (`activity.Observed`): the action only acknowledges, the claim hands
  over to the polled status, and it gives up after `settle:` polls with no
  change. `settle:` defaults to 1 and is valid only with `until: status`.
- **Confirm:** the claim starts when the user accepts, not when they pick.
- **Fan-out** (`multi:`): one claim per row (`DispatchEach`), so a failure
  stops only that row. **Failure** (non-zero exit, HTTP error) withdraws
  that row's claim; the error is reported as today.
- **Finished between polls** (`UnobservedMsg`) is written to the console
  as info automatically.
- **`interactive:` and `push:` actions** can't have `claim:`.
- **Success message:** "<label> requested" for `until: status`, "<label>
  completed" for `until: done`. `message:` overrides both.

**Tests.** Held and Observed claims end correctly; fan-out claims per row
and a failure withdraws only its row; the claim starts after confirm; an
unobserved ending reaches the console; the validation rules above.

## Workstream 8 — eventlog (Seekable)

Decided in [eventlog component schema](https://github.com/jsdrews/tui-builder/issues/26).
Needs Workstream 5.

```yaml
sources:
  job_events:
    type: http
    url: .../jobs/${params.id}/job_events/
    window:
      offset_param: ...
      limit_param: page_size
      total_path: count
      growing: {source: job, while: 'status in ["pending","running"]'}  # or: true
      follow_every: 2s

components:
  events:
    type: eventlog
    source: job_events      # must declare window:
    key: id                 # required; identity for enter and de-dup
    text: stdout            # split on newlines; empty → "· no output"
    mark: created           # optional, shown as-is
    color_rules: [...]      # per line
    searchable: true        # "/" jumps among resident items
    filterable: true        # narrows via window search_param / filters
    max_items: 2000         # optional; default tuilib DefaultMaxItems
    start: oldest           # oldest | newest once not growing
```

- Built on `remote.NewEventlog(Seekable)`.
- **logview vs eventlog:** logview keeps everything streamed or held whole
  (`follow: true`, websocket, `format: text`). eventlog binds only to
  `window:` sources (Anchored too, from Workstream 9). Any other binding
  is a validation error.
- **Growing:** `growing: true` (always, while the screen is open) or
  `growing: {source, while}`, re-evaluated whenever that source answers,
  using the same expression syntax as `busy_when`. Going false triggers
  tuilib's final read. `follow_every:` is the poll interval while growing
  (default 2s). While growing, the view opens at newest and follows; away
  from the newest it counts "↓ N new" and `G` returns.
- **No rename:** the top-level `follow: true` keeps its Streamed meaning
  and stays exclusive with `window:`.
- **`refresh:` with `growing:`** is a validation error. **`growing:` on a
  source bound to a table** is a validation error.
- **Enter / double click** fires the `key: enter` action with the item as
  `${selection.*}`. With none bound, it opens the auto inspector modal on
  the raw item.
- **Search vs filter:** `searchable:` searches resident items only;
  `filterable:` narrows the query.
- **v1 limits:** positions are dense (no holes); the total comes from
  `total_path` only.

**Tests.** Item rendering (multi-line, 0-line); follow and the new count;
growing stops and does a final read; enter fallback to the inspector; the
binding and `growing:` / `refresh:` validation.

## Workstream 9 — Anchored sources

Decided in [Anchored source schema](https://github.com/jsdrews/tui-builder/issues/29).
Needs Workstream 8.

```yaml
sources:
  logs:
    type: http
    method: POST
    url: .../_search
    body: {query: {...}, size: ${window.limit}}
    window:
      cursor: sort            # presence ⇒ Anchored
      anchor: newest          # newest | oldest, default newest
      older: {sort: [{"@timestamp": desc}, {_id: desc}], search_after: ${window.cursor}}
      newer: {sort: [{"@timestamp": asc},  {_id: asc}],  search_after: ${window.cursor}}
      search_param: ...       # or ${window.search} / ${window.filters.X} in body / patches
      growing: true           # as Workstream 8; follow polls the newer edge
```

- **Shape:** `cursor:` inside `window:` makes the source Anchored, driven
  by `remote.Anchored`. `offset_param`, `total_path` and `sort_debounce`
  are then validation errors.
- **Key vs cursor:** the key handed to tuilib is the cursor plus the
  component's key (`key:` on eventlog, `mark_key:` on a table), so equal
  cursors (Prefect timestamps) don't collapse. Requests see only the
  cursor. Non-string cursors (ES `sort` arrays) are carried as JSON and
  substituted as raw JSON.
- **Request:** the `older:` / `newer:` patches are JSON-merged into
  `body:`, or into the query params on GET. A field whose value is
  `${window.cursor}` is dropped when the cursor is empty (the first
  request). exec gets `${window.cursor}`, `${window.dir}` (`older` /
  `newer`) and `${window.limit}`.
- **Order:** an older walk's reply is newest-first and is reversed
  client-side. exec scripts follow the same rule.
- **`more`:** `${window.limit}` is the page size + 1; the extra item says
  there's more and is trimmed.
- **Inclusive anchor:** no token. A repeated anchor item is dropped by key.
- **Filter:** the same `search_param` / `filters` as Seekable windows, plus
  `${window.search}` / `${window.filters.X}` tokens inside `body:` and the
  patches (e.g. Prefect `text.query`).
- This PR also binds eventlog to Anchored sources.

**Tests.** Patches merge per direction and drop an empty cursor; older
replies are reversed; limit+1 sets `more`; equal cursors with different
keys both survive; raw-JSON cursor substitution; exec tokens; validation.

## Workstream 10 — span tables

Decided in [Table span mode](https://github.com/jsdrews/tui-builder/issues/27).
Needs Workstream 9.

```yaml
audit:
  type: table
  source: audit_log        # window declares cursor: ⇒ span table
  mark_key: id             # required
  markable: true           # works on span tables
  max_items: 5000          # optional; span tables only
  filterable: true
  columns: [...]           # no sortable:, no initial_sort
```

- A table bound to an Anchored source is a span table
  (`remote.NewTable(Anchored)`), derived the same way `Windowed` is. No
  opt-in flag.
- **Order:** oldest at the top, newest at the bottom (fixed by tuilib);
  `anchor:` picks where the view starts.
- **Key:** `mark_key:` is required. It's the component half of the dedupe
  key, and it makes `markable:` work.
- **Cap:** `max_items:`, valid only on span tables, default 5,000. Not
  `max_rows`, which is the streaming ring buffer.
- **Sort:** none. `sortable:` columns, `initial_sort`, `sorts:` and
  `sort_debounce` on a span table are validation errors.
- **Filter:** remote, as on windowed tables. Committing a filter
  re-anchors at `anchor:`; the old rows stay dimmed until the answer lands.
- **Growing** stays a validation error on tables.

**Tests.** Rows extend at both edges and trim past `max_items`; marking by
key; a filter commit re-anchors; the validation rules above.

---

## Implementation order

Decided in [PR split and order](https://github.com/jsdrews/tui-builder/issues/31).

1 → 2 → 3, then two tracks that can run in parallel:

- **Remote data:** 4 → 5 → 8 → 9 → 10
- **Activity:** 6 → 7 (needs only 1)

Every feature PR updates the reference (Workstream 2's coverage test
enforces it) and the guide pages where a concept changes.

## Deferred

In scope for later, not v1:

- **Activity on streaming tables** (one event isn't a whole observation)
  **and on windowed or span tables.** Span rows are keyed, which removes
  the windowed objection for them, but it stays deferred.
- **logview extras** (`LineNumbers`, `Prepend`, `AppendMarker`) and
  `pkg/resume` for exact stream reconnects, when a streaming source needs
  them.
- **Eventlog extras:**
  - server-side Find (`n`/`N` past what's held, including turning a hit
    into a filtered position)
  - holes (missing positions)
  - totals from a second request (AWX's highest counter)
- **Anchored extras:**
  - starting at a cursor from params ("jump to time")
  - tail overlap for late arrivals (needs a time-typed cursor)
- **Terminal recordings** (e.g. VHS GIFs of examples) for the docs site.

## Out of scope

- **Progress from actions**
  ([Progress from actions](https://github.com/jsdrews/tui-builder/issues/28)).
  tuilib v0.31 removed claim relabelling and doesn't draw a run's status,
  so there's nothing to adopt. Claims keep a fixed label. Parked ideas,
  if a long-running exec action ever needs them: an exec stdout status
  line that relabels the claim (needs tuilib to re-add relabelling), or
  the same line shown in the console run header or the statusbar.
