# Data

Everything under `data.sources:` is one map of named entries. An entry is
either a **source**, which fetches from outside (an HTTP API, a command,
a file, a WebSocket, inline data), or a **pipeline** operator, which
reads other entries and reshapes them. Components and `wrangl` use
entries by name, and don't care which kind they are.

Every field is in [Reference → Sources](../reference/sources.md) and
[Reference → Pipelines](../reference/pipelines.md). For the model
underneath, see [How the data layer works](../data-layer.md).

## Sources

```yaml
data:
  sources:
    pods:
      type: http
      url: http://localhost:8001/api/v1/pods
      root: items          # the list lives under "items"
      refresh: 5s          # poll

    branches:
      type: exec
      command: [gh, api, repos/jsdrews/tui-builder/branches]

    logs:
      type: exec
      follow: true         # stream lines as they arrive
      command: [kubectl, logs, -f, deploy/web]
```

| Kind | For |
|---|---|
| `http` | REST and JSON APIs, kube proxies, streamed endpoints |
| `exec` | Any command that prints JSON, or streams lines |
| `file` | Fixtures and generated files, re-read on `refresh` |
| `websocket` | Live event streams |
| `static` | Inline data: lookup tables, fixtures, demos |
| `merge` | Several sources concatenated into one list |

How often a source delivers follows from its fields: `follow: true` (or
`websocket`) streams, `refresh:` polls, and otherwise it's fetched once
and again on `r`.

`root:` picks the list out of the response. `format: text` keeps the
response as a string, for logs.

### Query strings and request bodies

Build a query string with `query:` rather than by hand in the URL: each
value is escaped for you, a list repeats its key, and a parameter left
empty drops out. Send a body as `json:` (written as YAML) or `form:`
rather than a hand-quoted string; a request with a body is a POST
unless `method:` says otherwise.

```yaml
search:
  type: http
  url: ${env.ES_URL}/logs/_search
  query: {ignore_unavailable: "true"}
  json:
    size: "${params.limit}"           # a number, if limit is an int parameter
    query: {match: {message: "${params.q}"}}
```

Tokens written straight into `url:` are escaped by where they sit. The
details are in [Reference → Requests](../reference/sources.md#requests).

### Sharing settings with a preset

Sources against one API repeat its base URL, headers and timeout. Put
those in a **preset** under `data.presets:`, and have each source
`extends:` it:

```yaml
data:
  presets:
    github:
      type: http
      url: https://api.github.com
      headers: {Accept: application/vnd.github+json}
  sources:
    repo:
      extends: github
      url: /repos/${params.name}           # appended to the preset's url
      parameters:
        name: {type: string, default: jsdrews/tui-builder}
    my_repos:
      extends: github
      url: /users/jsdrews/repos
      refresh: 10m
```

A source's own fields win; `headers:` merge key by key, and `unset:
[field]` drops one it would inherit. A preset produces nothing itself,
so components bind to the sources. The merge rules are in
[Reference → Presets](../reference/sources.md#presets).

## Pipelines

Operators take `from:` (or several inputs) and produce a new entry:

```yaml
data:
  sources:
    pods: {type: http, url: http://localhost:8001/api/v1/pods, root: items, refresh: 5s}

    failing:
      type: filter
      from: pods
      where: "status.phase != 'Running'"

    slim:
      type: project
      from: failing
      keep:
        name: metadata.name
        phase: status.phase
        restarts: "status.containerStatuses[0].restartCount"
```

| Operator | Does |
|---|---|
| `filter` | keeps items matching `where:` |
| `project` | rebuilds items from `keep:` |
| `derive` | adds `compute:` fields |
| `sort` | orders by `by:` |
| `union` | concatenates several entries, tagging rows by origin |
| `compose` | bundles several entries into one object |
| `join` | enriches each row with a per-row lookup |
| `cache` | holds a snapshot for a TTL |
| `passthrough` | a second, stable name for an entry |

When the steps in between aren't worth naming, chain them inline with
`pipe:`:

```yaml
failing_slim:
  type: http
  url: http://localhost:8001/api/v1/pods
  root: items
  pipe:
    - {type: filter, where: "status.phase != 'Running'"}
    - {type: project, keep: {name: metadata.name, phase: status.phase}}
```

Expressions are [expr-lang](https://expr-lang.org/docs/language-definition);
see [Templating → Expressions](templating.md#expressions).

## Parameters

A source can declare typed inputs and reference them as
`${params.NAME}`. That makes one definition serve many uses: a drill-down
screen, a detail pane that follows a cursor, a join, or the command line.

```yaml
pod:
  type: http
  parameters:
    namespace: {required: true}
    name: {required: true}
  url: http://localhost:8001/api/v1/namespaces/${params.namespace}/pods/${params.name}
```

```sh
wrangl ops.yaml pod --param namespace=default --param name=web-0
```

In the TUI, a push binding's `bind:` or a component's `on_cursor.bind`
supplies them from the focused row.

## Big remote sets

For an API with more rows than you'd want in memory, there are two
options:

- **`paginate:`** walks every page up front into one list. The table
  then filters and sorts locally. Fine up to a few thousand rows.
- **`window:`** fetches only the rows on screen and sends the filter and
  sort to the server, so `author:tolkien` searches the whole set. Binding
  a table to a windowed source switches it into this mode.

```yaml
books:
  type: http
  url: https://openlibrary.org/search.json
  root: docs
  window:
    offset_param: offset
    limit_param: limit
    total_path: numFound
    search_param: q
    filters: {Author: author}
```

`window:` works with `exec` too, with the request templated into the
argv. See [Reference → `window`](../reference/sources.md#window).

### Walking from an edge

Some APIs can't jump to row N at all: Elasticsearch past 10,000 hits
(`search_after`), logs filtered by "before / after this timestamp", and
anything that pages with an `?after=<token>`. Declare `cursor:` in the
window and the source is *Anchored*: each request walks from the oldest
or newest item held, using that item's cursor.

```yaml
logs:
  type: http
  method: POST
  url: ${env.ES_URL}/app-logs/_search
  root: hits.hits
  body: '{"query": {"match_all": {}}, "size": ${window.limit}}'
  window:
    cursor: sort
    older: {sort: [{"@timestamp": desc}], search_after: "${window.cursor}"}
    newer: {sort: [{"@timestamp": asc}],  search_after: "${window.cursor}"}
```

`older:` and `newer:` are what the request changes for each direction,
merged into the body. An eventlog or a table can bind an Anchored source.
See [Reference → Anchored windows](../reference/sources.md#anchored-windows).

### Still growing

A running job keeps adding events. `window.growing:` says so, either
always (`true`) or while another source says the job is running:

```yaml
window:
  growing: {source: job, while: 'status in ["pending", "running"]'}
  follow_every: 2s
```

While it grows, a bound eventlog follows the newest item and polls; scroll
away and new items are counted instead of moving you.

