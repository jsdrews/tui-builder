# What tuilib's paging can express that our `window:` config can't

Research for [#25](https://github.com/jsdrews/tui-builder/issues/25), a child of
map [#21](https://github.com/jsdrews/tui-builder/issues/21). This doc gives facts
and options only. The schema is decided in
[#26](https://github.com/jsdrews/tui-builder/issues/26).

The shape terms used below (Record, Event, Line, Seekable, Anchored, Streamed,
Growing, Anchor, Span, Edge, Follow, Probe) come from tuilib's `CONTEXT.md`.

Sources:

- **tuilib v0.31.0**, read from the module cache: `CONTEXT.md`,
  `docs/remote-data.md`, `pkg/source/{source,anchored}.go`, `pkg/remote/*.go`,
  `pkg/table/span.go`, `pkg/eventlog/eventlog.go`, and the two worked examples,
  `examples/patterns/eventlog` (AWX) and `examples/patterns/anchored` (ES).
- **tui-builder**: `origin/main` at `c5e4379`, which pins tuilib v0.25.0, plus
  the open bump branch `bump-tuilib` (PR #20, v0.31.0). The window code is
  `internal/config/{config,source,load}.go`, `internal/datasource/{window,http,exec}.go`
  and `internal/screen/window.go`.

## What each side has today

### tuilib v0.31

| Piece | What it does |
|---|---|
| `source.Model` (`ByOffset`) | Seekable windows addressed by offset and limit. <br>`MaxHeld` holds a contiguous range instead of a single window (the eventlog's mode). <br>`Follow` sets the poll interval for Growing data. <br>`Find(term, dir, from)` runs a server-side search that returns a position. <br>`Poke()` triggers an early poll on a push hint. |
| `source.Model` (`ByCursor`) | Forward-only continuation tokens that build up into one growing window (`Page.Next`). |
| `source.Anchored` | Edges instead of offsets. <br>Anchors are `Newest()`, `Oldest()` and `At(cursor)`. <br>Each request carries `Dir`, `Cursor`, `Inclusive`, `FromAnchor`, and `Probe` for a count-only poll. <br>The reply says whether that edge has `More`. <br>The source never interprets a cursor: each item's key *is* its cursor. |
| `pkg/remote` | Binds a component to a source. The screen only supplies functions: <br>`Seekable{Page(ctx, Window) (items, total, err), Find(ctx, Find) (at, found, err)}` <br>`Anchored{Edge(ctx, Edge) (items, more, err), Find(ctx, Find) (cursor, found, err)}` <br>Both take `PageSize`, `Follow` and `ViewportDelay`. <br>`remote.NewTable` and `remote.NewEventlog` do all the routing: holes, probe counts (`SetNew`), re-anchoring on a find hit, and `Restyle`. |
| `table` span mode | `Options.Anchored`, `AppendRows` / `PrependRows`, merged by key and trimmed at the far edge. |
| `eventlog` | Each `Item{Key, Lines, Data, Hole, Mark}` draws as 0–n lines. <br>Search jumps to a match and filter narrows the list. `n`/`N` go to the server past the resident items. <br>Follow, `↓ N new` and an inspector on enter. |

### tui-builder `window:` (main and bump branch; same here)

- **Kinds.** The `window:` block is allowed only on `http` and `exec`, and only a `type: table` can bind it (`bindWindowed`). Operators can't consume it.
- **Request vocabulary** (`ds.WindowQuery`): `Offset`, `Limit`, `Search`, `Filters`, `Sort`, `Desc`. There is no cursor, direction, anchor, find term or poll/probe flag.
- **Reply** (`ds.WindowPage`): `Items []any` and `Total` (−1 when unknown). There is no `More`, `Next`, per-item key or hole.
- **http** writes the request into the query string only:
  - It uses `offset_param`, `limit_param`, `search_param`, `filters`, `sort_param`, `sorts` and `sort_desc_prefix`, plus `total_path` read from the raw response.
  - `method:` and `body:` are honoured, but the body is fixed at construction time (`${params}` and env only). **No window value can reach the body.**
- **exec** templates `${window.offset|limit|search|sort|sort_dir|filters.X}` into the argv. That makes a script a general escape hatch for anything offset-shaped.
- **Polling.** `refresh:` on a windowed source calls `coord.Refresh()`, which refetches the window on screen in place. `SetGrowing`, `Follow`, `Probe` and `Poke` are not used.
- **Coordinator options.** The coordinator is built with `PageSize` and `Prefetch` only. `MaxHeld` is 0 (one window) and `Mode` is `ByOffset`.
- **Context.** `handleWindowRequest` calls `FetchWindow(context.Background(), …)` and ignores `Query.Ctx`, so a superseded request is **not cancelled**. tuilib's "at most one request per source live on the server" guarantee doesn't hold here.
- **Name clash.** tui-builder's `follow: true` already means *Streamed* (hold the response open and read lines). tuilib's **Follow** means *pin to the newest item of Growing data and poll*. Both kinds reject `window:` together with `follow: true`.

## Shape × support

"Express" means a YAML user can reach the shape without writing Go.

| Shape (item kind) | tuilib v0.31 | tui-builder today | Gap |
|---|---|---|---|
| **Seekable / Record** | `table` + `source.Model` / `remote.NewTable(Seekable)` | ✓ `table` + `window:`, over http (query-string offset/limit) or exec (argv). | Small. Request cancellation isn't wired. No server-side Find, but tables don't use it. Only APIs that take a literal offset in the query string work over http (see AWX and Prefect). |
| **Seekable / Event or Line** | `eventlog` + `source.Model{MaxHeld}` / `remote.NewEventlog(Seekable)` | ✗ No eventlog component, and `window:` binds only to tables. | Needs an eventlog binding. On the request side it needs a held range (`MaxHeld`), holes (a position the reply lacks), item rendering (lines, key, mark), and server-side Find returning a position. |
| **Anchored (any kind)** | `source.Anchored` + table span or eventlog / `remote.*(Anchored)` | ✗ Nothing to carry a cursor, a direction or an "edge has more" signal, and the http body is never templated per request. | This is the largest gap. It needs cursor extraction per item, cursor injection per request (body or query), direction handling (reversed sort and client reversal), an anchor choice, `more` detection, and optionally tail overlap plus de-duplication by key. |
| **Streamed (Line, Event)** | `logview` (+ `pkg/resume`, `Prepend`, `AppendMarker`) | ✓ `http`/`exec` `follow: true`, `websocket` → `logview` (Lines), or → `table` with `max_rows` (Records). | None for this ticket. Exact resume after a reconnect (`pkg/resume`) is out of scope on map #21. |
| **Growing (flag on Seekable or Anchored)** | `SetGrowing(true)`, `Follow` interval, follow polls the tail, probe counts new items, a final read after `SetGrowing(false)`, and `Poke` | ◐ `refresh:` refetches the window on screen. A larger `total` then extends the scrollbar, but nothing pins to the newest item, there's no "N new" count, and nothing ever says growth has stopped. | It needs a Growing flag or condition (when growth starts and stops) and a follow interval. A probe is free once Follow exists, because tuilib issues it. A "growing until" signal needs a second request or an upstream source. |

### What the existing escape hatch (exec) covers

An exec script can turn `${window.offset}` / `${window.limit}` into any request, including a POST body or a counter range. So over exec, every **Seekable** API in this doc is reachable *as a Record table* today.

Exec can't cover:

- Anchored, because there are no cursor or direction tokens and no `more` in the reply.
- Holes, because items are positional and there's no way to say "position 37 is missing".
- Find, Follow and Probe.

## The minimal config surface each gap implies

Every item below lists options, not decisions. The names are placeholders.

### G1. Eventlog binding for Seekable data

- **Allowed binding.** Allow `window:` on an `eventlog` component, or give the eventlog its own block, so the source is driven by `remote.NewEventlog(Seekable)` and not by `screen/window.go`.
- **Held range.** Choose one:
  - (a) fixed: the eventlog default `MaxItems`
  - (b) `window.max_held:` / component `max_items:`
- **Item key / position for holes.** Choose one:
  - (a) `key_path:` alone: no holes, and positions are assumed dense
  - (b) `position_path:` plus a base, for example `counter` with base 1, so that missing positions turn into `Hole` items
  - (c) holes as exec-only, where the script emits a `{"hole": true}` marker
- **Item rendering** (lines, mark, inspector data) belongs to #26 (`lines:` template or path, `mark:` path).
- **Total from a different request.** AWX's total is the highest counter, which is a second query. Choose one:
  - (a) `total_path` only: AWX `count` works when unfiltered and dense, but is wrong mid-run when there are holes
  - (b) a `total:` sub-request (URL or command plus path)
  - (c) reference another source by name, for example `total_from: job_head`

### G2. Anchored data (cursor paging)

The request needs these values per call: `cursor` (opaque, from an edge item), `newer` (direction), `inclusive`, `limit`, and the committed filter. The reply needs items in oldest-first order plus `more`.

- **Where the cursor comes from on each item:**
  - `cursor_path:` (ES: `sort`, an array; Prefect: `timestamp`; generic: `next`/`id`)
  - an encoding for non-string cursors: JSON-encode the value, so `[1712, 33]` becomes `"[1712,33]"`, which is what tuilib suggests
- **How the cursor gets into the request:**
  - http query string: `cursor_param:` / `direction_param:` (enough for REST APIs that take `?before=`/`?after=` tokens)
  - **http body templating**: ES `search_after` and Prefect `logs.timestamp.after_`/`before_` exist **only in the POST body**. Choose one:
    - (a) `${window.*}` tokens inside `body:`, rendered per request, with raw-JSON substitution for array cursors
    - (b) a structured `body_patch:`, i.e. JSON-pointer → value mappings
    - (c) leave Anchored to exec
  - exec: new tokens such as `${window.cursor}`, `${window.dir}` (`newer`/`older`), `${window.inclusive}`
- **Direction:**
  - Older pages use the same query with the sort reversed, and the hits are reversed client-side.
  - Choose one:
    - (a) `sort_newer:` / `sort_older:` literal fragments
    - (b) a `${window.order}` token (`asc`/`desc`) plus an implicit reverse when walking older
    - (c) the script or server returns oldest-first itself
- **Inclusive anchor** (`At(cursor)` must include the anchor item exactly once):
  - ES: the example bumps the `_shard_doc` tiebreaker by 1 on a descending walk.
  - Prefect: `after_` is already inclusive ("at or after").
  - So "inclusive" is API-specific. Choose one:
    - (a) a token the template must honour
    - (b) de-duplication by key only, accepting a duplicate the merge already drops (`AppendRows` skips held keys)
- **`more` detection.** Choose one:
  - (a) ask for `limit+1` and compare (what the ES example does)
  - (b) `len(items) == limit`
  - (c) `more_path:` (an envelope flag or next token)
- **Starting anchor:**
  - `anchor: newest | oldest`
  - a later `At(...)` from a jump-to-time prompt or a link param, for example `anchor: ${params.since}`
- **Tail overlap** for late arrivals:
  - ES tailing "rewinds a few seconds past the edge" and drops repeats by key.
  - Prefect needs "a few seconds of overlap, and drop duplicates by log id".
  - Options: a `tail_overlap: 3s` setting, which assumes the cursor is decodable as a time, so it needs a `cursor_kind: time | opaque`; or leave it to the template or script.

### G3. Server-side Find (search jumps past resident items)

- tuilib asks for "the first match for `term` beyond a position or cursor, within the filter" with limit 1.
  - Seekable Find returns a **position**.
  - Anchored Find returns a **cursor**, and the view re-anchors there.
- The options:
  - (a) a `find:` request variant (a URL, body or command override) with a `${window.term}` token and `limit` fixed at 1
  - (b) reuse the main template with term folded into `search` and `limit=1`; this is only right when "search" and "filter" map to the same server field
  - (c) none: `n`/`N` search only resident items, which is what tuilib does when `Find` is nil
- **AWX caveat.** Under a filter, a found counter has to be turned into a filtered position with one more `count` request (see the AWX example). A declarative `find:` therefore also needs a "position of" sub-request, or it is Seekable-unfiltered only.

### G4. Growing / Follow / Probe

- **Naming.** `follow:` is taken (it means Streamed). Options:
  - (a) `growing:` + `follow_every:`
  - (b) `window.follow:`, which reuses the word inside the block only
  - (c) rename the existing streaming flag (a breaking change)
- **When growth starts and stops.** tuilib's `SetGrowing(bool)` is the screen's call. Options:
  - (a) always Growing while the screen is open
  - (b) `growing_while:` naming another source and path, for example a job detail source with `status in [pending, running]`, or AWX `event_processing_finished == false`
  - (c) a param, for example `${params.status}` pushed from the parent screen
- **Interval.** `follow_every:` maps to `Follow`. The default is 2s. Negative means never.
- **Probe** needs no new config. tuilib issues probes as normal requests with `Probe=true`, and the same template answers them. ES and Prefect probes are just an edge request.
- **Poke** from a websocket hint (AWX `job_events` group, Prefect `WS /logs/out`) is optional. Options: `poke_on: <websocket source>`, or nothing.
- **`refresh:`** keeps working for Seekable tables. Whether it conflicts with Growing on the same source is an open question.

### G5. Plumbing (not schema)

- Pass `Query.Ctx` through to `FetchWindow` so a superseded request is cancelled. Today it's `context.Background()`.
- Consider replacing `internal/screen/window.go` with `pkg/remote` bindings. The `Page`/`Edge`/`Find` funcs would wrap `ds.WindowedSource` (and an Anchored counterpart). `remote` already does the routing, holes, probes and re-anchoring that `window.go` would otherwise have to re-implement.

## Per-API notes

### Elasticsearch `_search` (Event / Anchored / Growing)

- `from`/`size` can't page past `index.max_result_window`. The docs say: "By default, you cannot use `from` and `size` to page through more than 10,000 hits." Past that the only way in is `search_after`, using the sort values of the last hit ([Paginate search results][es-paginate]).
- Without a PIT, "include a tiebreaker field in your `sort`". Under a PIT, `_shard_doc` is added automatically ([same page][es-paginate]).
- The docs describe only forward paging. Backwards paging (reverse the sort, pass the first hit's values, reverse client-side) is a client technique. tuilib's ES example does exactly that.
- **What works today over http `window:`** ([search API][es-search]):
  - `from`, `size`, `q` (Lucene) and `sort` (`field:dir`) are query parameters on `GET /<index>/_search`. So `offset_param: from`, `limit_param: size`, `search_param: q`, `sort_param: sort`, `root: hits.hits` and `total_path: hits.total.value` give a **Seekable Record table capped at 10k hits**.
  - `hits.total` is an object unless `rest_total_hits_as_int` is set, and `track_total_hits` defaults to counting up to 10,000.
  - Scoped `filters:` don't fit well, because ES has one `q` and not one parameter per field. Every scoped term has to go through `q`.
- **What doesn't work:**
  - `search_after` is documented only as a body field, and the http body is never templated per request (G2).
  - Tailing needs Growing, overlap and de-duplication by `_id` (G2, G4).
  - Find needs `size:1` + `search_after` + the term in `query` (G3).
- tuilib's own notes: [tuilib#72](https://github.com/jsdrews/tuilib/issues/72) and `docs/remote-data.md` § Elasticsearch.

### AWX job events (Event / Seekable / Growing)

- **Pagination** comes from `awx/api/pagination.py` ([source][awx-pagination]):
  - Job event lists use `UnifiedJobEventPagination` ([views][awx-views], `JobJobEventsList`).
  - That defaults to DRF page-number paging (`page`, `page_size`), and switches to `LimitPagination` (results only, no count) when `limit` is present. `count_disabled` drops the count.
  - **There is no offset parameter.**
  - `MAX_PAGE_SIZE = 200` ([settings][awx-settings]).
- **Consequence for http `window:`.** `offset_param` has nothing to bind to. A page number would need `offset/limit + 1`, and a counter range needs `counter__gt=offset` & `counter__lte=offset+limit`. Neither is expressible, because the http window only passes values through and can't do arithmetic. **Exec can** (a script computes the range).
- **Shape details** (from [tuilib#71](https://github.com/jsdrews/tuilib/issues/71), `docs/remote-data.md` § AWX, and `examples/patterns/eventlog`):
  - Counters are dense from 1 at the source.
  - The total is the highest counter (`order_by=-counter&page_size=1`), a separate request.
  - Holes appear mid-run, and the final count settles only once the job's `event_processing_finished` is true. That flag is a field on the job, not on the events.
  - Always send `order_by=counter`.
  - Find is `stdout__icontains` + `counter__gt` + `page_size=1`, plus a `count` request to turn the hit into a filtered position.
  - The websocket is lossy and serves only as a hint.
- Stdout is capped at `STDOUT_MAX_BYTES_DISPLAY = 1048576` ([settings][awx-settings]), so a large job can't be read through stdout and has to be paged as events.
- **Gaps hit:**
  - G1: eventlog binding, holes, total from a second request
  - G3: Find, with the filtered-position follow-up
  - G4: Growing until `event_processing_finished`, which comes from another resource
  - Over http, also the lack of arithmetic or an offset parameter

### Prefect `POST /logs/filter` (Line or Event / Anchored / Growing)

- **Request body** ([Read Logs][prefect-logs]):
  - `offset` (default 0) and `limit` (default `PREFECT_SERVER_API_DEFAULT_LIMIT`)
  - `sort`: `TIMESTAMP_ASC` (default) or `TIMESTAMP_DESC`
  - `logs` filter: `timestamp.before_`/`after_` (both **inclusive**: "at or before" / "at or after"), `flow_run_id.any_`, `task_run_id`, `level.ge_`/`le_`, and `text.query` (max 200 chars)
- **Response:** a **bare JSON array**, so there is no total and no `more`.
- **What works today:**
  - Nothing over http `window:`, because offset and limit live in the body.
  - Over exec, a `curl` with `${window.offset}`/`${window.limit}` in the JSON gives a Seekable table with `Total` −1.
  - tuilib's notes say offsets are **unstable while a run is live**: batches stamped at creation land mid-order and shift later offsets.
  - The sort has **no tiebreaker** (timestamp only), so equal timestamps at a page edge need overlap plus de-duplication by log `id` ([tuilib#73](https://github.com/jsdrews/tuilib/issues/73), `docs/remote-data.md` § Prefect).
- **Anchored mapping:**
  - The cursor is the item's `timestamp`, and the key is its `id`.
  - An older walk uses `before_` = edge timestamp with `TIMESTAMP_DESC` and is reversed client-side.
  - A newer walk uses `after_` = edge timestamp − overlap with `TIMESTAMP_ASC`.
  - Server-side Find is possible through `text.query`, limit 1.
  - The page size must stay ≤ the server default limit (200), because a larger limit gets a 422 (tuilib notes).
  - **Note:** key ≠ cursor here (key = `id`, cursor = `timestamp`). tuilib's `remote.Anchored` says "each item's key is its cursor", so an encoding such as `timestamp|id` is needed.
- **Gaps hit:**
  - G2: body-templated cursor, timestamp anchor, overlap, de-duplication
  - G3: Find via `text.query`
  - G4: keep polling briefly after the run ends, because late batches still arrive. tuilib's final read after `SetGrowing(false)` covers one poll.

## Open questions #26 must decide

1. **Which shapes `eventlog` binds in v1.** Options: Seekable only (AWX), Anchored only (ES, Prefect), or both. Does `table` also get Anchored spans now, or later?
2. **Where Anchored request templating lives.** Options: http body tokens (G2a/b), new query-string params, or exec-only at first.
3. **Cursor model.** Options: an opaque `cursor_path:` (JSON-encoded), or typed (`time` vs `opaque`) so that `tail_overlap` and "jump to time" anchors are possible. How key and cursor relate when they differ (Prefect).
4. **Direction handling.** Options: separate older/newer sort fragments, a single `${window.order}` token, or "the source returns oldest-first".
5. **`more` detection.** Options: limit+1, a full page, or `more_path`.
6. **Holes.** Options: a declared position field, which makes holes automatic, or not supported.
7. **Totals from a second request.** For AWX, the highest counter. Options: sub-request, sibling source, or `total_path` only.
8. **Find.** Options: a separate `find:` template, reuse of the filter template, or resident-only. For Seekable-under-filter, how a hit becomes a position.
9. **Search vs filter mapping.** `search_param`/`filters` narrow the list today. Which server field answers a *find* term: ES `query`, AWX `stdout__icontains`, Prefect `text.query`?
10. **Growing.** What turns it on and off (always, a sibling source's field, or a param)? The follow interval default? Does `refresh:` coexist with it?
11. **The `follow` name collision.** tui-builder's `follow: true` is Streamed, and tuilib's Follow is Growing. Rename one, scope one, or live with both?
12. **Starting anchor.** Newest by default? Is `At(...)` reachable from params, a prompt, or an `on_cursor` link?
13. **Item rendering.** Lines template or path, `mark:` path (server time only, per tuilib), and what the inspector shows on enter (`Data`).
14. **Line between `eventlog` and `logview`.** Per tuilib: Streamed → logview, and Seekable/Anchored Events and Lines → eventlog. Does tui-builder keep `format: text` + logview for small finished outputs (AWX stdout < 1 MiB)?
15. **Implementation route** (informs, doesn't decide schema). Should `screen/window.go` move onto `pkg/remote`, and gain `Query.Ctx` cancellation, before the eventlog lands?

[es-paginate]: https://www.elastic.co/docs/reference/elasticsearch/rest-apis/paginate-search-results
[es-search]: https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-search
[awx-pagination]: https://github.com/ansible/awx/blob/devel/awx/api/pagination.py
[awx-views]: https://github.com/ansible/awx/blob/devel/awx/api/views/__init__.py
[awx-settings]: https://github.com/ansible/awx/blob/devel/awx/settings/defaults.py
[prefect-logs]: https://docs.prefect.io/v3/api-ref/rest-api/server/logs/read-logs
