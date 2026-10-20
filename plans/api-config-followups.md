# Config gaps for large page-numbered APIs: the locked spec

What tui-builder needs so a config generated from an OpenAPI 3
description works against a large page-numbered REST API without
workarounds. The test case is a FastAPI-style service: lists answer
`GET /things?page=N&per_page=M&order_by=a,-b` with `{total, entries}`,
there is no search or filter parameter, auth is a bearer token, and
writes are POST / PATCH / DELETE with JSON bodies.

It's locked: every decision was made on the map
[Map: Config gaps for large page-numbered APIs](https://github.com/jsdrews/tui-builder/issues/89),
and each section links the ticket that holds its detail and reasoning.
Eight PRs, in the [order at the bottom](#implementation-order).

One rule shaped the window decisions: **sort and filter run where all
the rows are.** Locally over a `paginate:` walk, which holds every row;
remotely over a `window:`, which holds only the slice on screen. Never
locally over a window's slice — sorting 200 resident rows of 50,000 and
calling it sorted is a wrong answer, not a partial one.

Terms follow [`CONTEXT.md`](../CONTEXT.md). A page-numbered window is a
**seekable** window (item N is reachable directly, and there's a
total); no new term was needed. Docs call it "a seekable window
addressed by page number", never a "page source".

Every new field must map cleanly from OpenAPI 3:

| OpenAPI | tui-builder |
|---|---|
| a `page` / `per_page` query parameter pair | `window.page_param` / `limit_param` |
| the parameter's `minimum` (0 or 1) | `window.page_start` |
| the per-page parameter's `maximum` | `window.page_size` (at most that) |
| a `total` property in the list response schema | `window.total_path` |
| `in: path` parameter | `parameters.X.segment: true` (sources), `inputs.X.segment: true` (actions) |
| a JSON `requestBody` | `json:` on the action |
| `securitySchemes` | `auth:` on a preset that sources **and actions** extend |

## 1. Page-numbered windows

From [Page-numbered windows](https://github.com/jsdrews/tui-builder/issues/91),
built on
[How tuilib's remote table picks offset and limit](https://github.com/jsdrews/tui-builder/issues/90).

**Why one request per window doesn't work:** tuilib asks for
page-aligned windows that often span several pages — a screen
crossing a page edge asks for two even with `prefetch: 0`, and the
last window is cut to the total (`offset 200, limit 37`). Sending
`page = offset/limit + 1, per_page = limit` returns the wrong rows.

**Fields** (http windows only):

| Field | Default | What it does |
|---|---|---|
| `page_param` | — | The query parameter carrying the page number. Exactly one of `offset_param` / `page_param` on a seekable http window. |
| `page_start` | `1` | The first page's number, `>= 0`. Only with `page_param`; same name and meaning as `paginate.page_start`. |
| `limit_param` | — | Still required; carries `page_size` on every request. |
| `total_path` | — | Required (see [§2](#2-a-total-on-every-seekable-window)). |

**Fetching one window** (`offset`, `limit` from tuilib):

1. First page = `offset / page_size + page_start`, sent with
   `per_page = page_size`.
2. Fetch pages **sequentially**, in order; stop on a short or empty
   page, or once `limit` rows are held.
3. Drop the first `offset % page_size` rows (non-zero only for
   unaligned callers such as Find), then trim to `limit`.
4. `retry:` applies per request. If any page fails after its retries,
   the whole window fetch fails — the table never holds half a window.

The total is read from the first page fetched.

**A server clamping `per_page`:** not validated (the cap isn't
knowable from config); docs say `page_size` must not exceed the API's
maximum. At runtime, if a page returns more rows than `page_size`, or a
page that isn't the last (by the total) returns fewer, post one notice:
`api.example.com/items: page_size 200 exceeds what the server returns (100)`.

**Not here:** `${window.page}` on exec (scripts keep
`${window.offset}` / `${window.limit}`); a page number in a JSON body
(query string only, like `offset_param`).

## 2. A total on every seekable window

From [Total on offset windows](https://github.com/jsdrews/tui-builder/issues/101).

Without a total, tuilib's table counts only the rows it has loaded and
never asks past its first window — a config without `total_path`
silently shows one page as the whole set.

- `total_path` is **required on every seekable window**: http
  `offset_param`, http `page_param`, and exec. Anchored windows
  (`cursor:`) are exempt. One check, in the shared window validation.
- Set but missing from the response, or not a number: one notice,
  deduplicated like [§6](#6-saying-when-paginate-stopped-early)'s:
  `api.example.com/items: total_path "total" not found in the response — the table can't scroll past what's loaded`.

Every seekable example already sets it.

## 3. Filtering a window

### No server filter

From [Windows without a server filter](https://github.com/jsdrews/tui-builder/issues/92).

**No new field.** `filterable` already exists on components and
defaults to `false`; a windowed table honours it. A window over an API
with no server filter is bound to a table without `filterable: true`.

The "needs a filter destination" check **moves**: out of the source
validators (`validateHTTPWindow`, `validateExecWindow`), which can't see
the table and which wrangl hits with no table at all, and into
`bindWindowed`, firing only when a bound table or eventlog has
`filterable: true`. The exec check that a declared `filters.<title>`
token is referenced in `command:` stays at the source.

### Bare terms with nowhere to go

From [Bare filter terms with no search_param](https://github.com/jsdrews/tui-builder/issues/102).

A window with `filters:` but no search stays valid — field-filter-only
APIs (`?status=x&owner=y`) are common. Today a bare term is silently
dropped; instead:

- The committed query's scoped terms are sent as usual, and one status
  and console notice says what wasn't:
  `"foo" not sent — this table filters only by column: author:…, status:…`
  (titles from `window.filters:`).
- Such a table with no `filter_placeholder:` defaults its placeholder
  to the scoped syntax, `author:… status:…`.

**"Has search"** is one predicate, shared with the `bindWindowed`
check: `search_param` (http), `${window.search}` in `command:` (exec),
or `${window.search}` in an Anchored body or patches.

## 4. Sorting exec windows

From [Sortable exec windows](https://github.com/jsdrews/tui-builder/issues/93).

A validator bug: the runtime already fills `${window.sort}` (the column
title) and `${window.sort_dir}` (`asc` / `desc`), and the docs describe
them. `bindWindowed` rejects every sortable column without
`window.sort_param`, which exec can't set.

The rule becomes: a windowed table may have sortable columns when the
http source sets `sort_param`, or the exec source's `command:`
references **both** `${window.sort}` and `${window.sort_dir}`.
`sorts:` and `sort_desc_prefix` stay http-only.

## 5. Path parameters that stay one segment

From [Path parameters that must stay one segment](https://github.com/jsdrews/tui-builder/issues/100).

URL tokens in the path are path-escaped but keep `/`, so
`${params.full_name}` = `owner/repo` spans two segments. An OpenAPI
path parameter is one segment; an id containing `/` would hit the
wrong endpoint.

- `segment: true` on a parameter declaration — source `parameters:`
  and action `inputs:` alike (both are `Parameter`).
- In the path, its value is fully path-escaped: `a/b` → `a%2Fb`. Query
  positions and the base before the path are unchanged.
- No validation; the flag describes the value. Other tokens
  (`${env.*}`, `${input.*}`, `${window.*}`) keep today's rule.
- Docs caution that some servers and proxies decode `%2F` back into a
  separator, or reject it.

## 6. Saying when paginate stopped early

From [Telling the user paginate stopped at max_pages](https://github.com/jsdrews/tui-builder/issues/94).

Two silent truncations in `fetchPaginated` get a notice through
`datasource.Notify` (the output console; `notice:` on stderr in
wrangl). No new field.

**Hitting `max_pages`.** Every stop check runs before the loop bound, so
leaving the loop already means the last page signalled more. Post a
notice whenever the walk ends there, except an `offset` / `page` walk
with no `total_path` whose last page was short (likely the end):

- total known: `api.example.com/items: stopped at max_pages (20) — 2000 of 3500 rows; raise paginate.max_pages or use window:`
- no total: `api.example.com/items: stopped at max_pages (20) — 2000 rows, more remain; raise paginate.max_pages or use window:`

**`on_page_error: skip`** returning a partial walk:
`api.example.com/items: page 5 failed (HTTP 500) — kept the first 4 pages (400 rows)`.

**Once, not every refresh:** a source keeps the last truncation notice
it posted and posts again only when the text differs. A complete walk
clears it. wrangl's exit status doesn't change — the cap is a safety
bound, not an error.

## 7. Actions

### JSON and form bodies

From [JSON and form bodies on http actions](https://github.com/jsdrews/tui-builder/issues/96).

http actions get the source body model
([http-source §3](http-source.md#3-request-bodies-and-default-headers)),
reusing `substituteJSON` / `typeJSONParams` from `internal/config/request.go`:

- `json:` (any YAML value) and `form:` (a string or list per key).
  `json:`, `form:` and `body:` are mutually exclusive. Content-Type
  follows the kind; an explicit `headers:` entry wins; `body:` keeps
  its `application/json` default.
- Strings are templated before encoding. A string that is exactly one
  token (`"${inputs.count}"`) takes the input's declared type.
- An exact-token value that resolves empty **drops its key**, so blank
  optional inputs are omitted. To clear a field in a PATCH, write a
  literal `""` or `null`.
- The method default stays POST, with or without a body.

**Action URLs** are escaped by position (`substituteURL`), as source
URLs are — today `action.Resolve` substitutes them raw. `segment: true`
([§5](#5-path-parameters-that-stay-one-segment)) applies to them.

### Sharing the source request model

From [http actions share the source request model](https://github.com/jsdrews/tui-builder/issues/103).

A generated config writes one preset for the API, and both its reads
and its writes extend it.

- Actions get `extends: <preset>` and `unset:`, merged with the source
  rules (relative URL joins the base; `headers` / `query` merge,
  action wins; structured blocks replace whole). They also get their
  own `auth:` and `query:`.
- **Inherited:** `url`, `headers`, `query`, `auth`, `timeout`. Every
  other preset field (`format`, `root`, `paginate`, `window`,
  `refresh`, `retry`, `max_body`, `conditional`, …) is ignored for
  actions, without error — presets are shared with sources.
- **No retry.** Actions never inherit `retry:` and never retry; a POST
  or DELETE isn't safe to resend.
- Timeout: the action's, then the preset's, then 30s.
- The default `User-Agent` is sent on every action unless `headers:`
  sets one.
- Auth secrets are masked `•••` in console entries, error messages and
  confirm previews.
- oauth2 tokens are cached once per process, keyed by the resolved
  auth block, so sources and actions with the same auth share one
  token; a 401 refetches.

## 8. Display

### Bold columns

From [Bold table columns](https://github.com/jsdrews/tui-builder/issues/95).

- `bold: bool` on a column. `columnCell` and `staticTableRows` wrap the
  cell, after its color rules, as `\x1b[1m` … `\x1b[22m`. That covers
  static, whole-set and windowed tables and eventlogs. `CellColor`
  resets only the foreground, so the selected-row background passes
  through; sorting strips ANSI.
- Headers keep the theme's style.

### Color rules on auto inspectors

From [Color rules on auto inspectors](https://github.com/jsdrews/tui-builder/issues/97).

- Component-level `color_rules:` (today list and logview only) applies
  to an `auto: true` inspector. Each rule may carry `path:`.
- `path:` is a dot-path from the record root (`status`,
  `spec.replicas`, `items.0.state`); `*` matches any one segment
  (`items.*.state`). No `path:` applies to every leaf. Per leaf, the
  first rule whose path and `when:` both match wins. Only scalar leaves
  are colored.
- Built by walking the tree `inspector.FromAny` returns — no tuilib
  change. Declared inspectors keep per-field rules; `auto` with
  `fields` stays an error.

## 9. Pausing polls when hidden

From [Pausing polls on hidden tabs and covered screens](https://github.com/jsdrews/tui-builder/issues/98).

Today hidden tabs and screens under a push keep polling, so a config
with one tab per resource polls every endpoint continuously. Each tab
and pushed screen is its own `Model` with its own sources, and each
already tracks whether it's `covered`.

- Timers keep ticking. On a tick, a covered Model skips the fetch for
  any source without `poll_hidden: true` and marks it stale. When
  `covered` clears, every stale source refetches at once.
- All three timer paths: source pollers (`handlePoll`), windowed-table
  ticks, and a growing eventlog's `follow_every`. `cache:`-wrapped
  sources poll through the same path.
- Unaffected: streams (they stay connected and keep updating),
  cursor-driven sources (no timer), action runs, manual `r`.
- `poll_hidden: true` on a source keeps it polling. TUI-only; the data
  layer and wrangl ignore it.

## 10. Smaller items

Settled while charting the map, without a ticket:

- **`omitempty`** on `ActionBinding.Key`, `WindowConfig.OffsetParam`
  and `WindowConfig.LimitParam`, so a marshalled config carries no
  empty keys.
- **Presets stay type-matched.** A source's `type` must match its
  preset's — the decision from
  [http-source §1](http-source.md#1-presets) stands.

## 11. Validation

**Load-time errors:**

| Rule | Example message | PR |
|---|---|---|
| Both or neither of `offset_param` / `page_param` on a seekable http window | `window: offset_param and page_param are mutually exclusive` / `window: set offset_param (row offsets) or page_param (page numbers)` | 2 |
| `page_start` without `page_param`, or negative | `window.page_start: only with page_param` / `window.page_start: must be >= 0` | 2 |
| Seekable window without `total_path` | `window.total_path: required — without a total a seekable window never scrolls past its first window. Set it, or use paginate: for the whole set, or window.cursor: for an API that only walks from an edge` | 1 |
| `filterable: true` table over a window with no filter destination | `tui.components.X: filterable: true, but source "Y" gives the filter nowhere to go — set window.search_param or window.filters (exec: reference ${window.search}), or drop filterable` | 1 |
| Sortable column, exec command without `${window.sort}` | `tui.components.X: columns[i] ("Date") is sortable but source "Y"'s command never references ${window.sort} — a windowed table sorts remotely, so there's nowhere to send the request` | 1 |
| Exec command with `${window.sort}` but not `${window.sort_dir}` | `…references ${window.sort} but not ${window.sort_dir} — descending would sort the same as ascending` | 1 |
| More than one of `json:` / `form:` / `body:` on an action | `actions.X: json and body are mutually exclusive` | 5 |
| A preset field an action inherits uses `${params.*}` | `actions.X: preset "api" uses ${params.org}, which an action can't fill — declare it in the action's url or headers with ${inputs.org}` | 6 |
| `bold: true` on a metric column | `not with bold: true — drawn glyphs don't take text styles` | 7 |
| `color_rules[i].path` outside an auto inspector | `color_rules[i].path: only applies on an auto: true inspector — columns and fields already say where` | 7 |

Removed: the source-level "set `search_param:` or at least one
`filters:` entry" errors (http and exec), replaced by the
`bindWindowed` row above.

**Runtime notices** (`datasource.Notify` / status), each posted once
per change: `per_page` clamped (§1), `total_path` missing (§2), bare
terms not sent (§3), `max_pages` reached and `on_page_error: skip`
(§6).

**`--describe`** shows `Window: page (page_param, from 1)` or
`offset`, and an action's `Extends:` chain with `(from <preset>)` on
inherited fields, as sources do.

## Implementation order

| # | PR | Sections | Needs |
|---|---|---|---|
| 1 | **Window validation**: `total_path` required, the filter-destination check moved to `bindWindowed`, the shared "has search" predicate and bare-term notice / placeholder, exec sortability | 2, 3, 4 | — |
| 2 | **Page-numbered windows**: `page_param`, `page_start`, multi-page fetch, the clamp notice | 1 | 1 |
| 3 | **Paginate notices**: `max_pages`, `on_page_error: skip`, deduplication | 6 | — |
| 4 | **One-segment path parameters**: `segment: true` in `substituteURL` | 5 | — |
| 5 | **Action bodies and URLs**: `json:` / `form:`, URL escaping by position | 7 (bodies) | 4 |
| 6 | **Actions extend presets**: `extends:` / `unset:` / `auth:` / `query:`, redaction, User-Agent, the shared token cache | 7 (sharing) | 5 |
| 7 | **Display**: `bold:`, color rules on auto inspectors | 8 | — |
| 8 | **Pause hidden polls**: `poll_hidden`, the three timer paths | 9 | — |

PRs 1, 3, 4, 7 and 8 can start at once. The `omitempty` change (§10)
rides along with whichever PR touches `internal/config` first.

**Each PR ships** its rows of the validation table, tests (`httptest`
for requests; screen tests for notices, placeholders and polling),
`docs/reference/*.md` rows (`TestReferenceDocumentsEveryField`), and
`--describe` lines for its fields. **Docs guidance** — a short section
in `docs/guide/data.md` on when to use `paginate:` (the whole set,
local sort and filter, up to a few thousand rows) versus `window:`
(lazy, server sort and filter, needs a total) — lands with PR 2, once
both window shapes exist. New behaviour is proven in tests, not in
examples that need the network.

## Out of scope

- **The OpenAPI-to-config generator itself.** Its needs shaped these
  decisions; it consumes this spec.
- **Presets for non-http sources.** "Type must match" stands.
- **wrangl rejecting data-only configs.** A standalone bug.
- **Statusbar counts** (`${source.loaded}` / `${source.total}`). The
  truncation notice covers the need.
- **Per-page reuse for windows.** The table refetches pages it holds as
  it scrolls; an optimisation, nothing is broken without it.
- **Background full load** — a lazy first paint, then every page loaded
  in the background and a switch to local sort and filter. A new
  feature; it deserves its own map.
- **Exec `${window.page}`**, a page number in a JSON body, and
  automatic retries on actions.
- **Bold per color rule** or a wider `style:` set. Wait for demand.
