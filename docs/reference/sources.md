# Sources

Every entry in `data.sources:` has a `type:`. **Leaf** kinds fetch from
outside the program and are covered here: `http`, `exec`, `file`,
`websocket`, `static` and `merge`. **Operator** kinds transform other
entries and are covered in [pipelines.md](pipelines.md). Both share one
map, so a component or `wrangl` addresses either by name.

How a source delivers data over time follows from its fields; there is
no `lifecycle:` setting:

| Cadence | When |
|---|---|
| streamed | `websocket`, or `http` / `exec` with `follow: true`: events arrive as they happen |
| polled | `refresh:` is set: fetched again on that interval |
| one-shot | neither: fetched once (and again on `r`) |

A source that declares a required parameter with no default "needs
params": it can only run once a caller binds them (a push binding,
`on_cursor`, a join, or `wrangl --param`).

## Fields shared by leaf sources

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | — | required | `http`, `exec`, `file`, `websocket`, `static` or `merge` (or an operator, see [pipelines.md](pipelines.md)). |
| `parameters` | map | — | any kind | Typed inputs the source references as `${params.NAME}`. See [parameters](#parameters). |
| `root` | string | — (whole value) | — | Dot-path selecting the iterable inside the response, e.g. `items` or `data.results`. |
| `format` | string | `json` | — | `json` parses the response; `text` keeps it as a raw string (for logs and plain-text endpoints). |
| `refresh` | duration | — (fetch once) | not with `follow` | Poll interval. |
| `timeout` | duration | `10s` | http, exec, websocket | Per-fetch limit. On websocket it bounds only the initial connect; ignored with `follow: true`. |
| `cache` | map | — | sources with `parameters:` | Memoises fetches per parameter combination. See [`cache`](#cache). |
| `pipe` | list | — | — | Transform stages applied to this source's output. See [pipelines.md](pipelines.md#pipe). |
| `resume` | map | — | streams: websocket, or exec / http with `follow: true` | Reconnects exactly: tracks each line's timestamp, resumes from the last one and skips repeats. See [`resume`](#resume). |

**Validation:** `format` is `json` or `text`; `refresh`, `timeout` and
`cache.ttl` parse as durations.

## `http`

Requests a URL and parses the response. For REST and JSON APIs, GraphQL
(via a POST body), kube proxies, and streamed endpoints with
`follow: true`.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `url` | template | — | required | The endpoint. |
| `method` | template | `GET` | — | HTTP method. |
| `headers` | map of template | — | — | Request headers, e.g. `Authorization: "Bearer ${env.TOKEN}"`. |
| `body` | template | — | — | Request body, sent as written after substitution. |
| `follow` | bool | `false` | not with `paginate`, `window` | Streams: holds the response open and emits one event per line (kube `?follow=true`, SSE, NDJSON feeds). |
| `paginate` | map | — | json; not with `follow`, `window` | Walks every page up front into one list. See [`paginate`](#paginate). |
| `window` | map | — | json; not with `follow`, `paginate` | Fetches only the rows on screen and lets the server filter and sort. See [`window`](#window). |

```yaml
releases:
  type: http
  url: https://api.github.com/repos/jsdrews/tui-builder/releases
  refresh: 5m
  timeout: 15s
  headers:
    Accept: application/vnd.github+json
    Authorization: "Bearer ${env.GITHUB_TOKEN}"
```

## `exec`

Runs a command and consumes its stdout. Any CLI that prints JSON becomes
a source (`kubectl get -o json`, `gh api`, scripts); with
`follow: true`, any command that streams lines (`kubectl logs -f`,
`tail -f`).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `command` | list of template | — | required | The argv. The first element is looked up in `$PATH`. Not run through a shell: use `[sh, -c, "…"]` when you want one. |
| `env` | map of template | — | — | Variables added to (or overriding) the inherited environment. |
| `follow` | bool | `false` | not with `window` | Streams: reads stdout line by line as it arrives. |
| `window` | map | — | json; not with `follow` | Runs one command per window of a larger set, with offset, limit, filter and sort templated into the argv. See [`window`](#window). |

```yaml
recent_commits:
  type: exec
  command: [sh, -c, "git log -20 --format='{\"sha\":\"%h\",\"subject\":\"%s\"}' | jq -s ."]
  refresh: 30s
```

## `file`

Reads a file from disk.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `path` | template | — | required | Path to the file. Re-read on each fetch, so with `refresh:` edits show up live. |

```yaml
people:
  type: file
  path: ./examples/fixtures/people.json
  refresh: 5s
```

## `websocket`

Connects to a WebSocket and emits one event per frame. Always streamed.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `url` | template | — | required | `ws://` or `wss://` endpoint. |
| `headers` | map of template | — | — | Sent on the upgrade request (auth). |
| `initial_messages` | list of template | — | — | Text frames sent, in order, right after connecting, for protocols that need a subscribe message first. |

```yaml
trades:
  type: websocket
  url: wss://ws.bitstamp.net
  initial_messages:
    - '{"event":"bts:subscribe","data":{"channel":"live_trades_btcusd"}}'
```

## `static`

Inline data in the YAML. No I/O. For fixtures, lookup tables, offline
demos and building a pipeline before pointing it at a real source.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `data` | any | — | required | The value: a list, an object or a scalar. `root:` slices into it. |

```yaml
regions:
  type: static
  data:
    - {code: us-east-1, name: N. Virginia}
    - {code: eu-west-1, name: Ireland}
```

## `merge`

Fetches several sources and concatenates their results into one list,
optionally tagging each row with where it came from. Streams when every
child streams; otherwise polls. Prefer the [`union`](pipelines.md#union)
operator in new configs; `merge` remains for its leaf fields (`refresh`,
`timeout`).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `sources` | list of string | — | not with `children` | Child entries, by name. |
| `tag_field` | string | — | `sources` | Each row gets a tag under this key whose value is the child's name. |
| `children` | list | — | not with `sources` | Child entries with per-child tags. See [merge children](#merge-children). |
| `meta_key` | string | `_meta` | — | Where tags are written on each row. `""` writes them at the top level of the row instead. |
| `on_error` | string | `fail` | — | `fail` aborts on any child error; `skip` drops failed children and returns the rest. |

**Validation:** exactly one of `sources` and `children`; `tag_field`
only with `sources`; `on_error` is `fail` or `skip`.

### Merge children

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `source` | string | — | required | The child entry's name. |
| `tags` | map of string | — | — | Key/value tags added to every map-shaped row from this child. Existing keys are kept. |

```yaml
pods_all:
  type: merge
  children:
    - {source: pods_prod,    tags: {cluster: prod,    cluster_url: "http://localhost:8001"}}
    - {source: pods_staging, tags: {cluster: staging, cluster_url: "http://localhost:8002"}}
  on_error: skip
  refresh: 5s
```

## Parameters

Typed inputs on a source, referenced as `${params.NAME}` in its
templated fields (`url`, `method`, `body`, `headers`, `path`, `command`,
`env`, `initial_messages`). Callers supply values: a push binding's
`bind:`, `on_cursor.bind`, a join lookup's `on:`, or
`wrangl --param NAME=VALUE`. On an operator, values are available in its
expressions as `params.NAME` (see
[pipelines.md](pipelines.md#parameters)).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | `string` | — | `string`, `int`, `bool` or `duration`. |
| `required` | bool | `false` | not with `default` | The source can't run until a caller supplies a value. |
| `default` | string | — | not with `required` | Used when no caller supplies one. |
| `description` | string | — | — | One-line summary, shown by `wrangl --describe`. |

The form fields (`label`, `placeholder`, `options`, `mask`, `order`) are
accepted too but only matter where a parameter is rendered as a form
field, which is in [action inputs](actions.md#inputs) and
[boot prompts](app.md#appprompts).

Resolution: the caller's value, else `default`, else an error if
`required`, else empty. A supplied name the source doesn't declare is an
error, so typos surface. A `${params.X}` with no declared parameter is
left literal, so it shows up in the URL rather than vanishing.

```yaml
pod:
  type: http
  parameters:
    namespace: {required: true}
    name:      {required: true}
  url: http://localhost:8001/api/v1/namespaces/${params.namespace}/pods/${params.name}
```

## `paginate`

Walks every page of an http endpoint before returning, concatenating
each page's items (after `root:`) into one list. For sets of up to a few
thousand rows; for bigger ones use [`window`](#window).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `strategy` | string | — | required | How to find the next page. `link`: the response carries the next page's URL. |
| `next_path` | string | — | required for `link` | Dot-path into the raw response (before `root:`) to the next page's URL, e.g. `next` or `links.next`. The walk stops when it's empty. |
| `max_pages` | int | `20` | — | Caps the walk. `0` or unset means the default of 20; there is no unlimited setting (one runaway API could exhaust memory), so set a larger number if you need more pages. |
| `on_page_error` | string | `fail` | — | `fail` returns the error; `skip` returns the pages fetched so far. |

```yaml
jobs:
  type: http
  url: ${env.AWX_HOST}/api/v2/jobs/
  root: results
  paginate: {strategy: link, next_path: next, max_pages: 50}
```

## `window`

Fetches only the rows a table is showing, and fetches again as the user
scrolls, filters or sorts, so the **server** answers the filter and sort
over the whole set.

| | `paginate:` | `window:` |
|---|---|---|
| Requests before the first frame | one per page | one |
| Rows held | all of them | the window on screen |
| Who filters and sorts | the table, over what it holds | the server, over everything |

Supported on `http` (the request goes in the query string) and `exec`
(the request is templated into the argv).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `page_size` | int | `100` | — | Rows per request. |
| `prefetch` | int | `0` | — | Extra pages fetched beyond the screen. `1` hides the placeholder flash at page boundaries, for one extra request. |
| `total_path` | string | — | — | Dot-path into the raw response (before `root:`) to the total match count. Without it the table can't size its scrollbar, and treats the end of what has loaded as the end. |
| `filters` | map of string | — | — | Maps a column **title** to where a scoped `title:value` filter term goes: a query parameter on http, a `${window.filters.NAME}` token name on exec. Terms on unmapped columns are treated as bare terms. |
| `offset_param` | string | — | required on http; not on exec | Query parameter for the first row wanted (`offset`, `_start`, `skip`). |
| `limit_param` | string | — | required on http; not on exec | Query parameter for the page size (`limit`, `per_page`). |
| `search_param` | string | — | http only | Query parameter for bare filter terms, joined with spaces. |
| `sort_param` | string | — | http only | Query parameter for the sort field. |
| `sorts` | map of string | — | http only; needs `sort_param` | Maps a column title to the sort field the API expects. Unlisted columns send their title. |
| `sort_desc_prefix` | string | — | http only; needs `sort_param` | Prepended for a descending sort, e.g. `-` for Django REST (`ordering=-created`). Empty means both directions send the same value. |
| `cursor` | string | — | eventlog and span-table sources | Makes the source **Anchored**: walked from an edge instead of paged by offset. The dot-path to each item's cursor, the value the next request walks from (ES `sort`, a timestamp, an id). See [Anchored windows](#anchored-windows). |
| `older` | map | — | http, with `cursor` | What the request changes to walk towards older items, merged into `body:` or set as query parameters. |
| `newer` | map | — | http, with `cursor` | The same, walking towards newer items. |
| `anchor` | string | `newest` | with `cursor` | Where an Anchored view starts: `newest` (and follows), `oldest` (and walks newer as you scroll down), or a template that resolves to a cursor, `${input.jump_to}`, to jump there. See [jumping](#jumping-to-a-cursor). |
| `find` | map | — | http | Searches past what's loaded: `n` / `N` ask the source where the next match is. See [`find`](#find). |
| `viewport_delay` | duration | tuilib's | — | How long scrolling must pause before rows are requested. `-` asks at once. |
| `growing` | `true` or map | — | eventlog only; not with `refresh` | The set is still gaining items at its newest end. A bound eventlog follows the newest item and the source is polled every `follow_every`; when growth stops, one final read picks up the last items. `true` grows for as long as the screen is open. The map form ties it to another source; see [growing](#growing). |
| `follow_every` | duration | `2s` | needs `growing` | Poll interval while growing. |

On exec, `command:` uses these tokens instead of the `*_param` fields:

| Token | Value |
|---|---|
| `${window.offset}` | First row wanted. Required. |
| `${window.limit}` | Page size. Required. |
| `${window.search}` | Bare filter terms joined with spaces; empty when none. |
| `${window.filters.NAME}` | A scoped term's value, where `NAME` is what `filters:` maps the column to. |
| `${window.sort}` | The sort column's title; the command maps it itself. |
| `${window.sort_dir}` | `asc` or `desc`; empty when unsorted. |
| `${window.cursor}` | Anchored only: the edge item's cursor; empty on the first request. |
| `${window.dir}` | Anchored only: `older` or `newer`. |

An argv element whose window tokens all resolve empty is dropped, so
`--author=${window.filters.author}` disappears when no author term is
typed. Substitution is literal, so in a `sh -c` script the quoting
around a token is what keeps it safe; plain argv elements are passed as
single arguments.

Query parameters already on an http `url:` are kept on every request; a
parameter the window also sets is overwritten, so `?q=x` on the URL acts
as a default search the user's filter replaces. `refresh:` and `r`
refetch the window on screen in place.

### Anchored windows

Some APIs can't jump to row N: Elasticsearch past 10,000 hits
(`search_after`), log APIs filtered by timestamp, and any API that pages
with an `?after=<token>`. They're **Anchored**: read by walking from an
edge item to the items before or after it. Setting `cursor:` makes a
window Anchored. An eventlog bound to one opens at the newest item and
walks older as you scroll up, or, with `anchor: oldest`, opens at the
oldest and walks newer.

Each request carries the edge item's cursor, which direction to walk,
and the page size:

- **http:** the `older:` or `newer:` map is merged into `body:` (a JSON
  object), or, for a request with no body, set as query parameters. A
  value that is exactly `${window.cursor}` takes the cursor's JSON value
  (an ES sort array stays an array) and is left out of the first request,
  which has no cursor yet. Inside `body:` text, `${window.limit}` is a
  number and `${window.search}` / `${window.filters.NAME}` are escaped for
  a JSON string. `search_param` and, with no body, `filters:` still name
  query parameters.
- **exec:** the command reads `${window.cursor}` (a string cursor
  unquoted, anything else as JSON), `${window.dir}` (`older` or `newer`)
  and `${window.limit}`, plus the usual filter tokens.

Answer an older walk newest first, as a descending sort does; tui-builder
reverses it. `${window.limit}` is one more than the page size: whether the
extra item comes back is how tui-builder knows that edge has more.

Items are held by cursor plus the component's `key:`, so two items with
the same cursor (log lines stamped the same millisecond) both stay.

```yaml
logs:
  type: http
  method: POST
  url: ${env.ES_URL}/app-logs/_search
  root: hits.hits
  body: '{"query": {"query_string": {"query": "${window.search}*"}}, "size": ${window.limit}}'
  window:
    cursor: sort
    older: {sort: [{"@timestamp": desc}, {_id: desc}], search_after: "${window.cursor}"}
    newer: {sort: [{"@timestamp": asc},  {_id: asc}],  search_after: "${window.cursor}"}
```

An Anchored window has no offsets, total or user sort, so `offset_param`,
`total_path`, `sort_param`, `sorts`, `sort_desc_prefix` and `prefetch`
don't apply. Where it opens is `anchor:`; the eventlog's `start:` is for
paged sources and is rejected here. An eventlog or a table can bind one;
a table over an Anchored source is a span table (see
[components.md](components.md#table)).

#### Jumping to a cursor

`anchor:` can be a template instead of `newest` / `oldest`. It resolves
to a cursor (a timestamp, an id), and the view opens there. When it
reads an [input](components.md#input) (`${input.NAME}`), committing that
input jumps again. A template that resolves empty means `newest`.
Templates take `${input.*}` and `${env.*}`.

The value is sent as the cursor: text a JSON parser can't read is sent
as a JSON string, so a typed `2026-10-11T09:00:00Z` reaches
`search_after` as a string, and `[1760173200000]` stays an array.

The first request walks older from the cursor, which most APIs answer
*excluding* the item at the cursor itself. That item arrives with the
newer side, so the view settles a row from where it jumped. For a
typed time, which rarely matches an item exactly, that's the item you
wanted anyway.

```yaml
jump_to: {type: input, title: Jump to (RFC 3339)}

logs:
  # …as above…
  window:
    cursor: sort
    anchor: "${input.jump_to}"
```

### `find`

Without `find:`, `/` searches only the items loaded, and `n` / `N` stop
at the edge of what's held. With it, they ask the source where the next
match is. `find:` is a patch over the source's request, like `older:` /
`newer:`:

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `params` | map | — | one of `params` / `body` | Query parameters set on the search request. |
| `body` | map | — | one of `params` / `body` | Fields merged into the request's JSON body. |
| `position` | path | — | required on a paged source; not on an Anchored one | The path in the matching item to its 0-based position in the (filtered) set. |

Its tokens are `${window.term}` (what was searched for), `${window.dir}`
(`older` / `newer`: before or after the current item),
`${window.cursor}` (the current item's cursor) and `${window.from}` (its
position, on a paged source), plus the view's filter as on any window
request. `${window.limit}` is `1`.

The reply's first item is the match; none means there's no further
match. An Anchored view re-anchors at the match's cursor; a paged one
scrolls to its position.

```yaml
window:
  cursor: sort
  older: {…}
  newer: {…}
  find:
    body:
      query: {query_string: {query: "${window.term}"}}
      search_after: "${window.cursor}"
      sort: [{"@timestamp": desc}]
```

**Validation:** `find` needs `params` or `body`; `position` is required on
a paged source and an error on an Anchored one; `find` is http-only (an
exec window searches what's loaded).

### Growing

`growing:` as a map names another source and a condition on its value,
re-evaluated every time that source answers. That source needn't be shown
anywhere: the screen fetches it when it opens, and polls it on its own
`refresh:`, so give it one.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `source` | string | — | required | The entry whose value decides, e.g. the job a list of events belongs to. |
| `while` | expr | — | required | True while items are still arriving, over that source's value. |

```yaml
data:
  sources:
    job:
      type: http
      url: ${env.AWX_HOST}/api/v2/jobs/${params.id}/
      refresh: 5s
    job_events:
      type: exec
      command: [awx-events, --job, "${params.id}", --offset, "${window.offset}", --limit, "${window.limit}", --search, "${window.search}"]
      window:
        total_path: count
        growing: {source: job, while: 'status in ["pending", "waiting", "running"]'}
        follow_every: 2s
```

**Validation:**

- `window:` only on `http` and `exec`, with json format, not with
  `follow` or `paginate`.
- Only a `type: table` or an `eventlog` may bind a windowed source, and
  an operator can't consume one (it would see one page and present it as
  everything). `growing:` is eventlog-only, can't be combined with
  `refresh:`, and its `source:` must exist and its `while:` compile;
  `follow_every` needs `growing`.
- http: `offset_param` and `limit_param` are required, and at least one
  of `search_param` / `filters` (otherwise the filter bar has nowhere to
  send input). An Anchored http window can instead carry the filter as
  `${window.search}` in `body:`, `older:` or `newer:`. `sorts` and `sort_desc_prefix` need `sort_param`.
- exec: the http-only fields are errors. `command:` must reference
  `${window.offset}`, `${window.limit}` and at least one filter token;
  every `filters:` entry must be referenced; `command[0]` can't use a
  window token.
- A `sortable` column on the bound table needs `sort_param` (http).
- `page_size` and `prefetch` are not negative.
- With `cursor:`: http needs both `older:` and `newer:`, which are
  http-only; exec must reference `${window.cursor}`, `${window.dir}` and
  `${window.limit}`; the offset, total and sort fields are rejected.
- `anchor:` needs `cursor:`. A paged window starts where the bound
  component's `start:` says.

`wrangl` on a windowed source returns its first page.

```yaml
books:
  type: http
  url: https://openlibrary.org/search.json?q=tolkien&fields=title,author_name
  root: docs
  window:
    prefetch: 1
    offset_param: offset
    limit_param: limit
    total_path: numFound
    search_param: q          # "hobbit"         → ?q=hobbit
    filters: {Author: author} # "author:tolkien" → ?author=tolkien
```

## `resume`

A stream that drops, or a follow that stops with its container, can be
reconnected with `r`. On its own that starts over from the live tail, so
lines are missed or shown twice. With `resume:`, tui-builder reads each
line's timestamp, and a reconnect asks the source for the lines since the
last one shown, through `${resume.since}` in the request, and skips the
ones it already has. A stream with `resume:` also reconnects by itself
when it ends: after a second, then backing off to 30s while it keeps
ending with nothing new.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `timestamp` | regex | — | required | A regular expression whose first capture group is each line's timestamp, in RFC 3339 (fractional seconds allowed). Lines it doesn't match are shown but not tracked. |
| `granularity` | duration | `1s` | — | The resolution the source's "since" parameter honours. The Kubernetes API truncates to a second; lines within the resumed second are matched up by count. |

`${resume.since}` is the time to ask from, RFC 3339 in UTC, and is empty
on the first connect. It can appear in `command:`, `env:`, `url:`,
`headers:`, `body:` and `initial_messages:`. Each reconnect marks the log
`— reconnected after 12s —`: lines sent while the stream was down are
fetched, but nothing can say whether the source kept them all.

```yaml
pod_logs:
  type: exec
  follow: true
  parameters: {pod: {required: true}}
  command:
    - sh
    - -c
    - 'since="${resume.since}"; kubectl logs -f --timestamps ${since:+--since-time=$since} "${params.pod}"'
  resume:
    timestamp: '^(\S+) '
```

tui-builder fills in `${resume.since}` before the shell runs; the shell's
`${since:+…}` then leaves `--since-time` off the first connect, when it's
empty.

**Validation:** `resume` only on a stream; `timestamp` compiles and has a
capture group; `granularity` is a positive duration; a source that reads
`${resume.since}` must declare `resume:`.

## `cache`

Memoises a source's fetches, keyed on its bound parameter values: many
calls with the same values within the TTL make one upstream request.
For sources fed by `on_cursor` or join lookups, so sweeping a cursor
over rows already visited doesn't refetch them. Streamed events pass
through uncached, and errors aren't cached. Distinct from the
[`type: cache`](pipelines.md#cache) operator, which holds one snapshot.

Join lookups get a cache automatically (60s TTL, 100 entries); declare
`cache:` to change the numbers.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `ttl` | duration | — | required | How long an entry stays fresh. |
| `size` | int | `100` | — | Maximum entries; the least recently used is evicted. Negative is unbounded. |

```yaml
user_posts:
  type: http
  parameters: {user_id: {type: int, required: true}}
  url: https://api.example.com/users/${params.user_id}/posts
  cache: {ttl: 5m, size: 200}
```
