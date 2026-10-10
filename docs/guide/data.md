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
